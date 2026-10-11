package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const authTestPassword = "test-password-123"

type authTestSession struct {
	cookie *http.Cookie
	csrf   string
	user   User
}

type authTestInvitation struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	MaxUses   int    `json:"max_uses"`
	UseCount  int    `json:"use_count"`
	Used      bool   `json:"used"`
	Revoked   bool   `json:"revoked"`
}

func authTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(DevConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if err = s.CreateAdmin("admin", authTestPassword); err != nil {
		t.Fatal(err)
	}
	return s
}

func authTestRequest(s *Server, method, path, body string, session *authTestSession, origin, csrf, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	r.RemoteAddr = "127.0.0.1:23456"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if session != nil {
		r.AddCookie(session.cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func authTestJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func authTestStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("HTTP status = %d; want %d; response: %s", w.Code, want, w.Body.String())
	}
}

func authTestDecodeSession(t *testing.T, s *Server, w *httptest.ResponseRecorder) authTestSession {
	t.Helper()
	authTestStatus(t, w, http.StatusOK)
	var payload struct {
		User User   `json:"user"`
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.User.ID == "" || len(payload.CSRF) != 43 {
		t.Fatal("login/register did not supply a user and CSRF token")
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == s.cookieName() {
			cookie = c
		}
	}
	if cookie == nil || len(cookie.Value) != 43 {
		t.Fatal("login/register did not issue the session cookie")
	}
	return authTestSession{cookie: cookie, csrf: payload.CSRF, user: payload.User}
}

func authTestLogin(t *testing.T, s *Server, username string) authTestSession {
	t.Helper()
	body := authTestJSON(t, map[string]string{"username": username, "password": authTestPassword})
	w := authTestRequest(s, "POST", "/api/login", body, nil, s.cfg.Scheme+"://"+s.cfg.AdminHost, "", s.cfg.AdminHost)
	return authTestDecodeSession(t, s, w)
}

func authTestInvite(t *testing.T, s *Server, admin authTestSession) (string, string) {
	t.Helper()
	invite, token := authTestCreateInvite(t, s, admin, "")
	return invite.ID, token
}

func authTestCreateInvite(t *testing.T, s *Server, admin authTestSession, body string) (authTestInvitation, string) {
	t.Helper()
	w := authTestRequest(s, "POST", "/api/invites", body, &admin, s.cfg.Scheme+"://"+s.cfg.AdminHost, admin.csrf, s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusCreated)
	var payload authTestInvitation
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "url", "expires_at", "max_uses", "use_count", "used", "revoked"} {
		if _, exists := fields[field]; !exists {
			t.Fatalf("new invitation missing %q", field)
		}
	}
	u, err := url.Parse(payload.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("invite")
	if len(token) != 43 || payload.ID == "" || u.Host != s.cfg.AdminHost {
		t.Fatal("invalid invite ID, URL or token")
	}
	return payload, token
}

func authTestInviteState(t *testing.T, s *Server, admin authTestSession, inviteID, token string, wantMax, wantCount int, wantRevoked bool) {
	t.Helper()
	var storedMax, storedCount int
	var storedUsed, storedRevoked bool
	if err := s.db.QueryRow("SELECT max_uses,use_count,used,revoked FROM invites WHERE id=?", inviteID).Scan(&storedMax, &storedCount, &storedUsed, &storedRevoked); err != nil {
		t.Fatal(err)
	}
	wantUsed := wantCount >= wantMax
	wantStoredUsed := wantMax > 1 || wantUsed
	if storedMax != wantMax || storedCount != wantCount || storedUsed != wantStoredUsed || storedRevoked != wantRevoked {
		t.Fatalf("stored invitation state = max %d, count %d, used %v, revoked %v; want %d, %d, %v, %v", storedMax, storedCount, storedUsed, storedRevoked, wantMax, wantCount, wantStoredUsed, wantRevoked)
	}
	w := authTestRequest(s, "GET", "/api/invites", "", &admin, "", "", s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), tokenHash(token)) {
		t.Fatal("listing exposes invitation secrets")
	}
	var payload struct {
		Invites []authTestInvitation `json:"invites"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, value := range payload.Invites {
		if value.ID != inviteID {
			continue
		}
		if value.MaxUses != wantMax || value.UseCount != wantCount || value.Used != wantUsed || value.Revoked != wantRevoked || value.URL != "" {
			t.Fatal("listed invitation state differs from storage or exposes the signup URL")
		}
		return
	}
	t.Fatal("invitation missing from admin listing")
}

func authTestRegister(t *testing.T, s *Server, token, username string) *httptest.ResponseRecorder {
	t.Helper()
	body := authTestJSON(t, map[string]string{"token": token, "username": username, "password": authTestPassword})
	return authTestRequest(s, "POST", "/api/register", body, nil, s.cfg.Scheme+"://"+s.cfg.AdminHost, "", s.cfg.AdminHost)
}

func authTestMember(t *testing.T, s *Server, admin authTestSession, username string) authTestSession {
	t.Helper()
	_, token := authTestInvite(t, s, admin)
	return authTestDecodeSession(t, s, authTestRegister(t, s, token, username))
}

func authTestSeedSite(t *testing.T, s *Server, owner User, id string, size int64) {
	t.Helper()
	dir := s.releasePath(id, "release1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>public website</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.Exec("INSERT INTO sites(id,owner_id,name,slug,release_id,size_bytes,updated_at) VALUES(?,?,?,?,?,?,?)", id, owner.ID, "Website "+id, "site-"+id, "release1", size, now())
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthLoginSessionLogout(t *testing.T) {
	s := authTestServer(t)
	for _, path := range []string{"/api/me", "/api/sites", "/api/invites"} {
		authTestStatus(t, authTestRequest(s, "GET", path, "", nil, "", "", s.cfg.AdminHost), http.StatusUnauthorized)
	}
	for _, username := range []string{"admin", "unknown"} {
		body := authTestJSON(t, map[string]string{"username": username, "password": "incorrect-password"})
		authTestStatus(t, authTestRequest(s, "POST", "/api/login", body, nil, "http://app.localhost", "", s.cfg.AdminHost), http.StatusUnauthorized)
	}
	session := authTestLogin(t, s, "admin")
	if !session.user.Admin || session.user.Username != "admin" {
		t.Fatal("admin identity lost in login response")
	}
	if session.cookie.Secure || !session.cookie.HttpOnly || session.cookie.Domain != "" || session.cookie.Path != "/" || session.cookie.SameSite != http.SameSiteStrictMode || session.cookie.MaxAge != 7*24*3600 {
		t.Fatal("development session cookie does not have the expected scope and protection")
	}
	w := authTestRequest(s, "GET", "/api/me", "", &session, "", "", s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusOK)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated responses must not be cached")
	}
	var token string
	if err := s.db.QueryRow("SELECT token_hash FROM sessions WHERE user_id=?", session.user.ID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token != tokenHash(session.cookie.Value) {
		t.Fatal("session secret is not stored as its digest")
	}
	w = authTestRequest(s, "POST", "/api/logout", "", &session, "http://app.localhost", session.csrf, s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusOK)
	if cookies := w.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Value != "" {
		t.Fatal("logout did not expire the session cookie")
	}
	authTestStatus(t, authTestRequest(s, "GET", "/api/me", "", &session, "", "", s.cfg.AdminHost), http.StatusUnauthorized)
}

func TestAuthOriginAndCSRFBoundaries(t *testing.T) {
	s := authTestServer(t)
	body := authTestJSON(t, map[string]string{"username": "admin", "password": authTestPassword})
	for _, origin := range []string{"", "null", "http://site-one.localhost", "https://app.localhost", "http://app.localhost.attacker.test"} {
		t.Run("login-origin-"+origin, func(t *testing.T) {
			authTestStatus(t, authTestRequest(s, "POST", "/api/login", body, nil, origin, "", s.cfg.AdminHost), http.StatusForbidden)
		})
		t.Run("register-origin-"+origin, func(t *testing.T) {
			authTestStatus(t, authTestRequest(s, "POST", "/api/register", body, nil, origin, "", s.cfg.AdminHost), http.StatusForbidden)
		})
	}
	session := authTestLogin(t, s, "admin")
	for _, endpoint := range []struct{ method, path string }{
		{"POST", "/api/logout"}, {"POST", "/api/invites"}, {"DELETE", "/api/invites/unknown"},
		{"POST", "/api/sites"}, {"PATCH", "/api/sites/unknown"}, {"DELETE", "/api/sites/unknown"}, {"POST", "/api/sites/unknown/upload"},
	} {
		t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
			for _, origin := range []string{"", "http://site-one.localhost", "https://app.localhost"} {
				authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, "{}", &session, origin, session.csrf, s.cfg.AdminHost), http.StatusForbidden)
			}
			for _, csrf := range []string{"", "wrong-csrf-token"} {
				authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, "{}", &session, "http://app.localhost", csrf, s.cfg.AdminHost), http.StatusForbidden)
			}
		})
	}
	var sessions, invites int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM invites").Scan(&invites); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || invites != 0 {
		t.Fatal("a rejected mutation modified the database")
	}
	// SameSite alone cannot isolate sibling sites: the explicit Origin checks above do.
	authTestStatus(t, authTestRequest(s, "GET", "/api/me", "", &session, "", "", s.cfg.AdminHost), http.StatusOK)
}

func TestAuthProductionCookieAndHostIsolation(t *testing.T) {
	cfg := Config{DataDir: t.TempDir(), AdminHost: "app.example.com", BaseDomain: "example.com", Scheme: "https"}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if err = s.CreateAdmin("admin", authTestPassword); err != nil {
		t.Fatal(err)
	}
	session := authTestLogin(t, s, "admin")
	if session.cookie.Name != "__Host-guiweb_session" || !session.cookie.Secure || !session.cookie.HttpOnly || session.cookie.Domain != "" || session.cookie.Path != "/" || session.cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("production session cookie is not secure and scoped to the admin host")
	}
	authTestSeedSite(t, s, session.user, "public", 25)
	w := authTestRequest(s, "GET", "/", "", nil, "", "", "site-public.example.com")
	authTestStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "public website") {
		t.Fatal("public website cannot be served anonymously")
	}
	for _, host := range []string{"site-public.example.com", "unknown.example.com", "example.com", "attacker.test", "app.example.com.attacker.test", "app.example.com:bad:port"} {
		for _, path := range []string{"/api/me", "/api/sites", "/api/invites", "/api/health"} {
			t.Run(host+path, func(t *testing.T) {
				authTestStatus(t, authTestRequest(s, "GET", path, "", &session, "", "", host), http.StatusNotFound)
			})
		}
	}
	for _, origin := range []string{"", "http://app.example.com", "https://site-public.example.com", "https://app.example.com:443"} {
		authTestStatus(t, authTestRequest(s, "POST", "/api/invites", "", &session, origin, session.csrf, s.cfg.AdminHost), http.StatusForbidden)
	}
	w = authTestRequest(s, "GET", "/api/me", "", &session, "", "", "APP.EXAMPLE.COM")
	authTestStatus(t, w, http.StatusOK)
	if w.Header().Get("Strict-Transport-Security") == "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("admin host protection headers missing")
	}
}

func TestInviteLifecycleAndRegistrationRollback(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	id, token := authTestInvite(t, s, admin)
	var digest string
	if err := s.db.QueryRow("SELECT token_hash FROM invites WHERE id=?", id).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest != tokenHash(token) {
		t.Fatal("invite token is not stored as its digest")
	}
	w := authTestRequest(s, "GET", "/api/invites", "", &admin, "", "", s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), digest) {
		t.Fatal("invite list exposes a token or its digest")
	}
	// A duplicate username must roll back consuming the invitation.
	authTestStatus(t, authTestRegister(t, s, token, "admin"), http.StatusConflict)
	var used int
	if err := s.db.QueryRow("SELECT used FROM invites WHERE id=?", id).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatal("failed registration consumed a reusable invitation")
	}
	member := authTestDecodeSession(t, s, authTestRegister(t, s, token, "member"))
	if member.user.Admin {
		t.Fatal("an invitation granted administrator privileges")
	}
	authTestStatus(t, authTestRegister(t, s, token, "member-two"), http.StatusBadRequest)
	if err := s.db.QueryRow("SELECT used FROM invites WHERE id=?", id).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatal("successful registration did not consume invitation")
	}
	for _, mode := range []string{"expired", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			inviteID, inviteToken := authTestInvite(t, s, admin)
			if mode == "expired" {
				_, err := s.db.Exec("UPDATE invites SET expires_at=? WHERE id=?", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), inviteID)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				authTestStatus(t, authTestRequest(s, "DELETE", "/api/invites/"+inviteID, "", &admin, "http://app.localhost", admin.csrf, s.cfg.AdminHost), http.StatusOK)
			}
			authTestStatus(t, authTestRegister(t, s, inviteToken, "member-"+mode), http.StatusBadRequest)
		})
	}
	authTestStatus(t, authTestRegister(t, s, "short-token", "member-bad"), http.StatusBadRequest)
	authTestStatus(t, authTestRegister(t, s, strings.Repeat("x", 43), "member-bad"), http.StatusBadRequest)
	authTestStatus(t, authTestRequest(s, "DELETE", "/api/invites/missing", "", &admin, "http://app.localhost", admin.csrf, s.cfg.AdminHost), http.StatusNotFound)
}

func TestMemberAndAdminOwnershipBoundaries(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	alice := authTestMember(t, s, admin, "alice")
	bob := authTestMember(t, s, admin, "bob")
	authTestSeedSite(t, s, admin.user, "admin-site", 100)
	authTestSeedSite(t, s, alice.user, "alice-site", 200)
	authTestSeedSite(t, s, bob.user, "bob-site", 300)
	for _, test := range []struct {
		session authTestSession
		count   int
		usage   int64
	}{{admin, 3, 600}, {alice, 1, 200}, {bob, 1, 300}} {
		t.Run(test.session.user.Username, func(t *testing.T) {
			w := authTestRequest(s, "GET", "/api/sites", "", &test.session, "", "", s.cfg.AdminHost)
			authTestStatus(t, w, http.StatusOK)
			var payload struct {
				Sites []site `json:"sites"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Sites) != test.count {
				t.Fatalf("visible websites = %d; want %d", len(payload.Sites), test.count)
			}
			if !test.session.user.Admin {
				for _, value := range payload.Sites {
					if value.OwnerID != test.session.user.ID {
						t.Fatal("member can see another member's website in management list")
					}
				}
			}
			w = authTestRequest(s, "GET", "/api/me", "", &test.session, "", "", s.cfg.AdminHost)
			authTestStatus(t, w, http.StatusOK)
			var profile struct {
				Usage int64 `json:"usage_bytes"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
				t.Fatal(err)
			}
			if profile.Usage != test.usage {
				t.Fatalf("visible usage = %d; want %d", profile.Usage, test.usage)
			}
		})
	}
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/invites"}, {"POST", "/api/invites"}, {"DELETE", "/api/invites/anything"}} {
		authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, "", &alice, "http://app.localhost", alice.csrf, s.cfg.AdminHost), http.StatusForbidden)
	}
	for _, endpoint := range []struct{ method, suffix string }{{"PATCH", ""}, {"DELETE", ""}, {"POST", "/upload"}} {
		authTestStatus(t, authTestRequest(s, endpoint.method, "/api/sites/bob-site"+endpoint.suffix, `{"name":"Hacked"}`, &alice, "http://app.localhost", alice.csrf, s.cfg.AdminHost), http.StatusNotFound)
	}
	for _, test := range []struct {
		session authTestSession
		id      string
	}{{alice, "alice-site"}, {admin, "bob-site"}} {
		authTestStatus(t, authTestRequest(s, "PATCH", "/api/sites/"+test.id, `{"name":"Renamed"}`, &test.session, "http://app.localhost", test.session.csrf, s.cfg.AdminHost), http.StatusOK)
	}
	var slug string
	if err := s.db.QueryRow("SELECT slug FROM sites WHERE id='alice-site'").Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if slug != "site-alice-site" {
		t.Fatal("renaming changed the share link")
	}
	authTestStatus(t, authTestRequest(s, "DELETE", "/api/sites/alice-site", "", &alice, "http://app.localhost", alice.csrf, s.cfg.AdminHost), http.StatusOK)
	var remaining int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sites").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 {
		t.Fatal("deleting an owned website changed other websites")
	}
}

func TestPasswordResetAndExpiredSession(t *testing.T) {
	s := authTestServer(t)
	first := authTestLogin(t, s, "admin")
	second := authTestLogin(t, s, "admin")
	if _, err := s.db.Exec("UPDATE sessions SET expires_at=? WHERE token_hash=?", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), tokenHash(first.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	authTestStatus(t, authTestRequest(s, "GET", "/api/me", "", &first, "", "", s.cfg.AdminHost), http.StatusUnauthorized)
	authTestStatus(t, authTestRequest(s, "GET", "/api/me", "", &second, "", "", s.cfg.AdminHost), http.StatusOK)
	newPassword := "new-password-456"
	if err := s.ResetPassword("admin", newPassword); err != nil {
		t.Fatal(err)
	}
	authTestStatus(t, authTestRequest(s, "GET", "/api/me", "", &second, "", "", s.cfg.AdminHost), http.StatusUnauthorized)
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("password reset left device sessions active")
	}
	body := authTestJSON(t, map[string]string{"username": "admin", "password": authTestPassword})
	authTestStatus(t, authTestRequest(s, "POST", "/api/login", body, nil, "http://app.localhost", "", s.cfg.AdminHost), http.StatusUnauthorized)
	body = authTestJSON(t, map[string]string{"username": "admin", "password": newPassword})
	authTestDecodeSession(t, s, authTestRequest(s, "POST", "/api/login", body, nil, "http://app.localhost", "", s.cfg.AdminHost))
}

func TestLoginAttemptRateLimit(t *testing.T) {
	s := authTestServer(t)
	body := authTestJSON(t, map[string]string{"username": "admin", "password": "incorrect-password"})
	for i := 0; i < 5; i++ {
		authTestStatus(t, authTestRequest(s, "POST", "/api/login", body, nil, "http://app.localhost", "", s.cfg.AdminHost), http.StatusUnauthorized)
	}
	w := authTestRequest(s, "POST", "/api/login", body, nil, "http://app.localhost", "", s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusTooManyRequests)
	if w.Header().Get("Retry-After") != "300" {
		t.Fatal("rate limiting does not advertise its retry interval")
	}
	// Expiring the existing limiter window must restore the ability to log in.
	s.rateMu.Lock()
	for key, value := range s.attempts {
		value.start = time.Now().Add(-6 * time.Minute)
		s.attempts[key] = value
	}
	s.rateMu.Unlock()
	authTestLogin(t, s, "admin")
}

func TestAuthRejectsMalformedJSONAndCredentials(t *testing.T) {
	s := authTestServer(t)
	for _, body := range []string{`{"username":"admin","password":"test-password-123","admin":true}`, `{"username":"admin"} {}`, `{"username":"` + strings.Repeat("a", 5000) + `"}`, `{`} {
		authTestStatus(t, authTestRequest(s, "POST", "/api/login", body, nil, "http://app.localhost", "", s.cfg.AdminHost), http.StatusBadRequest)
	}
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"test-password-123"}`))
	r.Host = s.cfg.AdminHost
	r.Header.Set("Origin", "http://app.localhost")
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	authTestStatus(t, w, http.StatusUnsupportedMediaType)
	for _, credential := range []struct{ username, password string }{{"ab", authTestPassword}, {"space name", authTestPassword}, {"admin", "short"}, {"admin", strings.Repeat("x", 73)}} {
		if validCredentials(credential.username, credential.password) {
			t.Fatal("invalid credentials accepted")
		}
		if err := s.CreateAdmin(credential.username, credential.password); err == nil {
			t.Fatal("admin CLI accepted invalid credentials")
		}
		if err := s.ResetPassword(credential.username, credential.password); err == nil {
			t.Fatal("password reset CLI accepted invalid credentials")
		}
	}
	if err := s.CreateAdmin("admin", authTestPassword); err == nil {
		t.Fatal("duplicate administrator was accepted")
	}
	if err := s.ResetPassword("missing-user", authTestPassword); err == nil {
		t.Fatal("password reset for missing member succeeded")
	}
}

func TestCloudflareAddressTrustAndRateLimit(t *testing.T) {
	for _, test := range []struct {
		name, peer, visitor, expected string
		trust                         bool
	}{
		{"trusted-ipv4", "127.0.0.1:23456", "203.0.113.10", "203.0.113.10", true},
		{"trusted-ipv6", "[::1]:23456", "2001:db8::10", "2001:db8::10", true},
		{"disabled", "127.0.0.1:23456", "203.0.113.10", "127.0.0.1", false},
		{"nonlocal-spoof", "192.0.2.25:23456", "203.0.113.10", "192.0.2.25", true},
		{"invalid-header", "127.0.0.1:23456", "not-an-address", "127.0.0.1", true},
		{"multiple-address-header", "127.0.0.1:23456", "203.0.113.10, 192.0.2.25", "127.0.0.1", true},
		{"loopback-header", "127.0.0.1:23456", "127.0.0.2", "127.0.0.1", true},
		{"unspecified-header", "127.0.0.1:23456", "0.0.0.0", "127.0.0.1", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := DevConfig(t.TempDir())
			cfg.TrustCloudflare = test.trust
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.RemoteAddr = test.peer
			r.Header.Set("CF-Connecting-IP", test.visitor)
			if !s.allowAttempt(r, "alice") {
				t.Fatal("first attempt rejected")
			}
			s.rateMu.Lock()
			entry, exists := s.attempts["ip:"+test.expected]
			count := len(s.attempts)
			s.rateMu.Unlock()
			if !exists || entry.count != 1 || count != 2 {
				t.Fatal("limiter trusted an unexpected visitor address")
			}
		})
	}
	// Rotating a spoofed header and username from a remote peer cannot evade the IP cap.
	cfg := DevConfig(t.TempDir())
	cfg.TrustCloudflare = true
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 31; i++ {
		r := httptest.NewRequest("POST", "/api/login", nil)
		r.RemoteAddr = "192.0.2.25:23456"
		r.Header.Set("CF-Connecting-IP", "203.0.113."+strconv.Itoa(i+1))
		allowed := s.allowAttempt(r, "member-"+strconv.Itoa(i+1))
		if allowed != (i < 30) {
			t.Fatalf("attempt %d allowed = %v; expected IP cap of 30", i+1, allowed)
		}
	}
	s.rateMu.Lock()
	count := len(s.attempts)
	peerCount := s.attempts["ip:192.0.2.25"].count
	s.rateMu.Unlock()
	if count != 31 || peerCount != 30 {
		t.Fatal("spoofed headers created visitor counters or exceeded the IP limit")
	}
}

func TestInviteOptionsAndDefaults(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	for _, test := range []struct {
		name, body string
		maxUses    int
		duration   time.Duration
	}{
		{"legacy-empty", "", 1, 48 * time.Hour},
		{"empty-options", "{}", 1, 48 * time.Hour},
		{"explicit-single-use", `{"max_uses":1}`, 1, 48 * time.Hour},
		{"reusable-minimum", `{"max_uses":2}`, 2, 7 * 24 * time.Hour},
		{"reusable-ui-default", `{"max_uses":20}`, 20, 7 * 24 * time.Hour},
		{"reusable-maximum", `{"max_uses":1000}`, 1000, 7 * 24 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := time.Now().UTC()
			invite, token := authTestCreateInvite(t, s, admin, test.body)
			if invite.MaxUses != test.maxUses || invite.UseCount != 0 || invite.Used || invite.Revoked {
				t.Fatal("new invitation capacity/state differs from its request")
			}
			expiry, err := time.Parse(time.RFC3339, invite.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			if delta := expiry.Sub(before.Add(test.duration)); delta < -time.Second || delta > time.Second {
				t.Fatalf("invitation expiry differs from duration %s by %s", test.duration, delta)
			}
			authTestInviteState(t, s, admin, invite.ID, token, test.maxUses, 0, false)
		})
	}
}

func TestInviteRejectsInvalidOptionsWithoutCreatingRecords(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	for _, body := range []string{
		`{"max_uses":0}`, `{"max_uses":-1}`, `{"max_uses":1001}`,
		`{"max_uses":1.5}`, `{"max_uses":20.0}`, `{"max_uses":2e1}`,
		`{"max_uses":"20"}`, `{"max_uses":true}`, `{"max_uses":null}`, `{"max_uses":[]}`, `{"max_uses":{}}`,
		`{"max_uses":999999999999999999999999}`, `{"unknown":20}`, `{"max_uses":2,"admin":true}`,
		`null`, `[]`, `true`, `20`, `"20"`, `{`, `{} {}`, " ",
		`{"max_uses":` + strings.Repeat("1", 5000) + `}`,
	} {
		t.Run(body[:min(len(body), 70)], func(t *testing.T) {
			w := authTestRequest(s, "POST", "/api/invites", body, &admin, "http://app.localhost", admin.csrf, s.cfg.AdminHost)
			authTestStatus(t, w, http.StatusBadRequest)
		})
	}
	r := httptest.NewRequest("POST", "/api/invites", strings.NewReader(`{"max_uses":20}`))
	r.Host = s.cfg.AdminHost
	r.Header.Set("Origin", "http://app.localhost")
	r.Header.Set("X-CSRF-Token", admin.csrf)
	r.Header.Set("Content-Type", "text/plain")
	r.AddCookie(admin.cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	authTestStatus(t, w, http.StatusUnsupportedMediaType)
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM invites").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid invitation options created records")
	}
}

func TestReusableInviteForwardingExhaustionAndRollback(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	invite, token := authTestCreateInvite(t, s, admin, `{"max_uses":3}`)
	alice := authTestDecodeSession(t, s, authTestRegister(t, s, token, "alice"))
	if alice.user.Admin {
		t.Fatal("first recipient became an admin")
	}
	authTestInviteState(t, s, admin, invite.ID, token, 3, 1, false)
	// Alice forwards the exact URL she received. No member-only invitation API
	// or new token is necessary, and forwarding grants no administrative role.
	forwarded, err := url.Parse(invite.URL)
	if err != nil {
		t.Fatal(err)
	}
	forwardedToken := forwarded.Query().Get("invite")
	bob := authTestDecodeSession(t, s, authTestRegister(t, s, forwardedToken, "bob"))
	if bob.user.Admin {
		t.Fatal("forwarded link granted admin privileges")
	}
	authTestInviteState(t, s, admin, invite.ID, token, 3, 2, false)
	for _, endpoint := range []struct{ method, path, body string }{
		{"GET", "/api/invites", ""}, {"POST", "/api/invites", `{"max_uses":1000}`}, {"DELETE", "/api/invites/" + invite.ID, ""},
	} {
		authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, endpoint.body, &alice, "http://app.localhost", alice.csrf, s.cfg.AdminHost), http.StatusForbidden)
	}
	// A username conflict on the last slot must roll back the capacity change.
	authTestStatus(t, authTestRegister(t, s, forwardedToken, "bob"), http.StatusConflict)
	authTestInviteState(t, s, admin, invite.ID, token, 3, 2, false)
	charlie := authTestDecodeSession(t, s, authTestRegister(t, s, forwardedToken, "charlie"))
	if charlie.user.Admin {
		t.Fatal("last recipient became an admin")
	}
	authTestInviteState(t, s, admin, invite.ID, token, 3, 3, false)
	authTestStatus(t, authTestRegister(t, s, forwardedToken, "david"), http.StatusBadRequest)
	authTestInviteState(t, s, admin, invite.ID, token, 3, 3, false)
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatal("rejected registrations created users")
	}
}

func TestReusableInviteExpiryAndRevocation(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	for _, mode := range []string{"expired", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			invite, token := authTestCreateInvite(t, s, admin, `{"max_uses":3}`)
			authTestDecodeSession(t, s, authTestRegister(t, s, token, mode+"-first"))
			if mode == "expired" {
				if _, err := s.db.Exec("UPDATE invites SET expires_at=? WHERE id=?", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), invite.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				authTestStatus(t, authTestRequest(s, "DELETE", "/api/invites/"+invite.ID, "", &admin, "http://app.localhost", admin.csrf, s.cfg.AdminHost), http.StatusOK)
			}
			authTestStatus(t, authTestRegister(t, s, token, mode+"-second"), http.StatusBadRequest)
			authTestInviteState(t, s, admin, invite.ID, token, 3, 1, mode == "revoked")
		})
	}
}

func TestReusableInviteConcurrentRegistrationIsBounded(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	invite, token := authTestCreateInvite(t, s, admin, `{"max_uses":2}`)
	// The router deliberately rejects extra bcrypt work instead of accumulating
	// expensive concurrent hashing. A busy response must not consume a slot.
	s.passwordMu.Lock()
	blocked := authTestRegister(t, s, token, "blocked-member")
	s.passwordMu.Unlock()
	authTestStatus(t, blocked, http.StatusTooManyRequests)
	authTestInviteState(t, s, admin, invite.ID, token, 2, 0, false)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 8)
	var tasks sync.WaitGroup
	for i := 0; i < 8; i++ {
		body := authTestJSON(t, map[string]string{"token": token, "username": "concurrent-" + strconv.Itoa(i), "password": authTestPassword})
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			<-start
			results <- authTestRequest(s, "POST", "/api/register", body, nil, "http://app.localhost", "", s.cfg.AdminHost)
		}()
	}
	close(start)
	tasks.Wait()
	close(results)
	succeeded := 0
	for response := range results {
		switch response.Code {
		case http.StatusOK:
			member := authTestDecodeSession(t, s, response)
			if member.user.Admin {
				t.Fatal("concurrent registration granted admin privileges")
			}
			succeeded++
		case http.StatusTooManyRequests, http.StatusBadRequest:
		default:
			t.Fatalf("concurrent registration returned unexpected status %d", response.Code)
		}
	}
	if succeeded < 1 || succeeded > 2 {
		t.Fatalf("concurrent signup succeeded %d times for two slots", succeeded)
	}
	authTestInviteState(t, s, admin, invite.ID, token, 2, succeeded, false)
	for i := succeeded; i < 2; i++ {
		authTestDecodeSession(t, s, authTestRegister(t, s, token, "sequential-"+strconv.Itoa(i)))
	}
	authTestStatus(t, authTestRegister(t, s, token, "after-exhaustion"), http.StatusBadRequest)
	authTestInviteState(t, s, admin, invite.ID, token, 2, 2, false)
	var members int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users WHERE admin=0").Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 2 {
		t.Fatalf("created %d members for two invite slots", members)
	}
}

func TestReusableInviteCountersSurviveRestart(t *testing.T) {
	cfg := DevConfig(t.TempDir())
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	if err := s.CreateAdmin("admin", authTestPassword); err != nil {
		t.Fatal(err)
	}
	admin := authTestLogin(t, s, "admin")
	invite, token := authTestCreateInvite(t, s, admin, `{"max_uses":2}`)
	authTestDecodeSession(t, s, authTestRegister(t, s, token, "before-restart"))
	authTestInviteState(t, s, admin, invite.ID, token, 2, 1, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The existing admin session remains valid, just like the stored counters.
	authTestInviteState(t, s, admin, invite.ID, token, 2, 1, false)
	authTestDecodeSession(t, s, authTestRegister(t, s, token, "after-restart"))
	authTestInviteState(t, s, admin, invite.ID, token, 2, 2, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	authTestStatus(t, authTestRegister(t, s, token, "after-second-restart"), http.StatusBadRequest)
	authTestInviteState(t, s, admin, invite.ID, token, 2, 2, false)
}

func TestReusableInviteRemainsClosedToLegacyBinary(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	invite, token := authTestCreateInvite(t, s, admin, `{"max_uses":3}`)
	assertLegacyClosed := func() {
		t.Helper()
		// This is the exact eligibility predicate used by v0.1.1. Rolling back
		// must not admit an uncounted member through a reusable invitation.
		var eligible int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM invites WHERE token_hash=? AND expires_at>? AND used=0 AND revoked=0", tokenHash(token), now()).Scan(&eligible); err != nil {
			t.Fatal(err)
		}
		if eligible != 0 {
			t.Fatal("legacy binary considers a reusable invitation valid")
		}
		res, err := s.db.Exec("UPDATE invites SET used=1 WHERE token_hash=? AND expires_at>? AND used=0 AND revoked=0", tokenHash(token), now())
		if err != nil {
			t.Fatal(err)
		}
		if affected, err := res.RowsAffected(); err != nil || affected != 0 {
			t.Fatalf("legacy registration consumed reusable invitation: %d rows, %v", affected, err)
		}
	}
	assertLegacyClosed()
	authTestInviteState(t, s, admin, invite.ID, token, 3, 0, false)
	authTestDecodeSession(t, s, authTestRegister(t, s, token, "new-recipient"))
	assertLegacyClosed()
	authTestInviteState(t, s, admin, invite.ID, token, 3, 1, false)
}

func TestLegacyConsumedSingleInviteIsNotReusable(t *testing.T) {
	s := authTestServer(t)
	admin := authTestLogin(t, s, "admin")
	invite, token := authTestCreateInvite(t, s, admin, "")
	// An older binary can mark a single-use invite consumed without updating
	// the additive counter. The new eligibility guard still respects used.
	if _, err := s.db.Exec("UPDATE invites SET used=1 WHERE id=?", invite.ID); err != nil {
		t.Fatal(err)
	}
	authTestStatus(t, authTestRegister(t, s, token, "blocked-recipient"), http.StatusBadRequest)
	w := authTestRequest(s, "GET", "/api/invites", "", &admin, "", "", s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusOK)
	var payload struct {
		Invites []authTestInvitation `json:"invites"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Invites) != 1 || !payload.Invites[0].Used {
		t.Fatal("API reopened a legacy-consumed single invitation")
	}
	var members int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users WHERE admin=0").Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatal("legacy-consumed invitation admitted another member")
	}
}
