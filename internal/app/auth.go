package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)
var errAuth = errors.New("unauthenticated")

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func equalToken(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func validCredentials(username, password string) bool {
	// bcrypt consumes up to 72 bytes. Restrict input rather than silently truncate it.
	return usernamePattern.MatchString(username) && len(password) >= 12 && len(password) <= 72
}

func (s *Server) cookieName() string {
	if s.cfg.Dev {
		return "guiweb_dev_session"
	}
	return "__Host-guiweb_session"
}

func (s *Server) session(r *http.Request) (User, string, error) {
	var u User
	var csrf string
	cookie, err := r.Cookie(s.cookieName())
	if err != nil || len(cookie.Value) != 43 {
		return u, "", errAuth
	}
	err = s.db.QueryRowContext(r.Context(), `SELECT u.id,u.username,u.admin,s.csrf FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, tokenHash(cookie.Value), now()).Scan(&u.ID, &u.Username, &u.Admin, &csrf)
	if err != nil {
		return User{}, "", errAuth
	}
	return u, csrf, nil
}
func (s *Server) user(r *http.Request) (User, error) {
	if u, ok := r.Context().Value(userContextKey{}).(User); ok {
		return u, nil
	}
	u, _, err := s.session(r)
	return u, err
}

func (s *Server) newSession(w http.ResponseWriter, u User) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return "", err
	}
	expiry := time.Now().UTC().Add(7 * 24 * time.Hour)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM sessions WHERE expires_at<=?", now()); err != nil {
		return "", err
	}
	// Keep a small bounded number of device sessions per member.
	if _, err = tx.Exec("DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM sessions WHERE user_id=? ORDER BY expires_at DESC LIMIT -1 OFFSET 4)", u.ID); err != nil {
		return "", err
	}
	if _, err = tx.Exec("INSERT INTO sessions(token_hash,user_id,csrf,expires_at) VALUES(?,?,?,?)", tokenHash(token), u.ID, csrf, expiry.Format(time.RFC3339)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: token, Path: "/", Expires: expiry, MaxAge: 7 * 24 * 3600, HttpOnly: true, Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode})
	return csrf, nil
}

func (s *Server) mePayload(u User, csrf string) (map[string]any, error) {
	var used int64
	var err error
	if u.Admin {
		err = s.db.QueryRow("SELECT COALESCE(SUM(size_bytes),0) FROM sites").Scan(&used)
	} else {
		err = s.db.QueryRow("SELECT COALESCE(SUM(size_bytes),0) FROM sites WHERE owner_id=?", u.ID).Scan(&used)
	}
	return map[string]any{"user": u, "csrf": csrf, "limits": map[string]any{"upload_bytes": s.cfg.UploadBytes, "site_bytes": s.cfg.SiteBytes, "total_bytes": s.cfg.TotalBytes, "user_bytes": s.cfg.UserBytes}, "usage_bytes": used}, err
}

func (s *Server) respondMe(w http.ResponseWriter, u User, csrf string) {
	v, err := s.mePayload(u, csrf)
	if err != nil {
		fail(w, 500, "Không đọc được thông tin tài khoản.")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, csrf, err := s.session(r)
	if err != nil {
		fail(w, 401, "Vui lòng đăng nhập.")
		return
	}
	s.respondMe(w, u, csrf)
}

type attempt struct {
	count int
	start time.Time
}

func (s *Server) allowAttempt(r *http.Request, username string) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	// Only an explicitly configured, local Tunnel may supply the visitor address.
	if s.cfg.TrustCloudflare {
		peer := net.ParseIP(ip)
		visitor := net.ParseIP(r.Header.Get("CF-Connecting-IP"))
		if peer != nil && peer.IsLoopback() && visitor != nil && !visitor.IsLoopback() && !visitor.IsUnspecified() {
			ip = visitor.String()
		}
	}
	keys := []string{"ip:" + ip, "user:" + strings.ToLower(username)}
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	t := time.Now()
	for k, a := range s.attempts {
		if t.Sub(a.start) > 5*time.Minute {
			delete(s.attempts, k)
		}
	}
	// Never evict live counters to let an attacker bypass the cap with new names.
	if len(s.attempts) > 1024 {
		return false
	}
	for i, k := range keys {
		a := s.attempts[k]
		limit := 5
		if i == 0 {
			limit = 30
		}
		if a.count >= limit {
			return false
		}
	}
	for _, k := range keys {
		a := s.attempts[k]
		if a.start.IsZero() {
			a.start = t
		}
		a.count++
		s.attempts[k] = a
	}
	return true
}

func (s *Server) clearUserAttempts(username string) {
	s.rateMu.Lock()
	delete(s.attempts, "user:"+strings.ToLower(username))
	s.rateMu.Unlock()
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !validCredentials(body.Username, body.Password) {
		fail(w, 401, "Tên đăng nhập hoặc mật khẩu không đúng.")
		return
	}
	if !s.allowAttempt(r, body.Username) {
		w.Header().Set("Retry-After", "300")
		fail(w, 429, "Bạn đã thử nhiều lần. Vui lòng chờ vài phút.")
		return
	}
	if !s.passwordMu.TryLock() {
		fail(w, 429, "Hệ thống đang xử lý đăng nhập. Vui lòng thử lại.")
		return
	}
	defer s.passwordMu.Unlock()
	var u User
	var hash string
	err := s.db.QueryRowContext(r.Context(), "SELECT id,username,admin,password_hash FROM users WHERE username=?", body.Username).Scan(&u.ID, &u.Username, &u.Admin, &hash)
	if err != nil {
		hash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	}
	cmp := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password))
	if err != nil || cmp != nil {
		fail(w, 401, "Tên đăng nhập hoặc mật khẩu không đúng.")
		return
	}
	s.clearUserAttempts(u.Username)
	csrf, err := s.newSession(w, u)
	if err != nil {
		fail(w, 500, "Không tạo được phiên đăng nhập.")
		return
	}
	s.respondMe(w, u, csrf)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(s.cookieName())
	if err == nil {
		if _, err = s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", tokenHash(c.Value)); err != nil {
			fail(w, 500, "Không đăng xuất được.")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if !validCredentials(b.Username, b.Password) {
		fail(w, 400, "Tên đăng nhập 3–32 ký tự chữ/số/_/-. Mật khẩu từ 12 đến 72 byte.")
		return
	}
	if len(b.Token) != 43 {
		fail(w, 400, "Link mời không hợp lệ hoặc đã hết hạn.")
		return
	}
	if !s.allowAttempt(r, b.Username) {
		fail(w, 429, "Vui lòng chờ vài phút trước khi thử lại.")
		return
	}
	if !s.passwordMu.TryLock() {
		fail(w, 429, "Hệ thống đang xử lý tài khoản. Vui lòng thử lại.")
		return
	}
	defer s.passwordMu.Unlock()
	var exists int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM invites WHERE token_hash=? AND expires_at>? AND used=0 AND revoked=0", tokenHash(b.Token), now()).Scan(&exists); err != nil || exists == 0 {
		fail(w, 400, "Link mời không hợp lệ hoặc đã hết hạn.")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), 10)
	if err != nil {
		fail(w, 500, "Không tạo được tài khoản.")
		return
	}
	id, err := randomToken(16)
	if err != nil {
		fail(w, 500, "Không tạo được tài khoản.")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "Không tạo được tài khoản.")
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), "UPDATE invites SET used=1 WHERE token_hash=? AND expires_at>? AND used=0 AND revoked=0", tokenHash(b.Token), now())
	if err != nil {
		fail(w, 500, "Không tạo được tài khoản.")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		fail(w, 400, "Link mời không hợp lệ hoặc đã hết hạn.")
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash,admin) VALUES(?,?,?,0)", id, b.Username, string(hash)); err != nil {
		fail(w, 409, "Tên đăng nhập đã được sử dụng.")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "Không tạo được tài khoản.")
		return
	}
	s.clearUserAttempts(b.Username)
	u := User{ID: id, Username: b.Username}
	csrf, err := s.newSession(w, u)
	if err != nil {
		fail(w, 500, "Tài khoản đã tạo. Vui lòng đăng nhập.")
		return
	}
	s.respondMe(w, u, csrf)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	u, _ := s.user(r)
	if !u.Admin {
		fail(w, 403, "Chỉ quản trị viên được tạo lời mời.")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		fail(w, 500, "Không tạo được lời mời.")
		return
	}
	id, err := randomToken(16)
	if err != nil {
		fail(w, 500, "Không tạo được lời mời.")
		return
	}
	expiry := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	// Expired invites need not grow without bound on the router.
	if _, err = s.db.ExecContext(r.Context(), "DELETE FROM invites WHERE expires_at<?", time.Now().UTC().Add(-30*24*time.Hour).Format(time.RFC3339)); err != nil {
		fail(w, 500, "Không tạo được lời mời.")
		return
	}
	if _, err = s.db.ExecContext(r.Context(), "INSERT INTO invites(id,token_hash,created_by,expires_at) VALUES(?,?,?,?)", id, tokenHash(token), u.ID, expiry); err != nil {
		fail(w, 500, "Không tạo được lời mời.")
		return
	}
	origin := s.cfg.Scheme + "://" + s.cfg.AdminHost
	if s.cfg.Dev {
		origin = "http://" + r.Host
	}
	writeJSON(w, 201, map[string]any{"id": id, "url": origin + "/?invite=" + token, "expires_at": expiry})
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	u, _ := s.user(r)
	if !u.Admin {
		fail(w, 403, "Chỉ quản trị viên được xem lời mời.")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,expires_at,used,revoked FROM invites ORDER BY expires_at DESC LIMIT 100")
	if err != nil {
		fail(w, 500, "Không đọc được lời mời.")
		return
	}
	defer rows.Close()
	type invitation struct {
		ID        string `json:"id"`
		ExpiresAt string `json:"expires_at"`
		Used      bool   `json:"used"`
		Revoked   bool   `json:"revoked"`
	}
	invites := []invitation{}
	for rows.Next() {
		var i invitation
		if err = rows.Scan(&i.ID, &i.ExpiresAt, &i.Used, &i.Revoked); err != nil {
			fail(w, 500, "Không đọc được lời mời.")
			return
		}
		invites = append(invites, i)
	}
	if err = rows.Err(); err != nil {
		fail(w, 500, "Không đọc được lời mời.")
		return
	}
	writeJSON(w, 200, map[string]any{"invites": invites})
}
func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	u, _ := s.user(r)
	if !u.Admin {
		fail(w, 403, "Chỉ quản trị viên được thu hồi lời mời.")
		return
	}
	res, err := s.db.ExecContext(r.Context(), "UPDATE invites SET revoked=1 WHERE id=?", r.PathValue("id"))
	if err != nil {
		fail(w, 500, "Không thu hồi được lời mời.")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "Không tìm thấy lời mời.")
		return
	}
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) CreateAdmin(username, password string) error {
	if !validCredentials(username, password) {
		return errors.New("tên đăng nhập 3–32 ký tự ASCII chữ/số/_/-, mật khẩu 12–72 byte")
	}
	id, err := randomToken(16)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO users(id,username,password_hash,admin) VALUES(?,?,?,1)", id, username, string(hash))
	return err
}

func (s *Server) ResetPassword(username, password string) error {
	if !validCredentials(username, password) {
		return errors.New("tên đăng nhập 3–32 ký tự ASCII chữ/số/_/-, mật khẩu 12–72 byte")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRow("SELECT id FROM users WHERE username=?", username).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE users SET password_hash=? WHERE id=?", string(hash), id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
