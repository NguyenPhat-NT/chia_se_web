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
	"testing"
	"time"
)

const authTestPassword = "test-password-123"

type authTestSession struct {
	cookie *http.Cookie
	csrf   string
	user   User
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
	w := authTestRequest(s, "POST", "/api/invites", "", &admin, s.cfg.Scheme+"://"+s.cfg.AdminHost, admin.csrf, s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusCreated)
	var payload struct{ ID, URL, ExpiresAt string }
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(payload.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("invite")
	if len(token) != 43 || payload.ID == "" || u.Host != s.cfg.AdminHost {
		t.Fatal("invalid invite ID, URL or token")
	}
	return payload.ID, token
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
