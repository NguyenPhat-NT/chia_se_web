package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func keysTestSession(t *testing.T, s *Server, user User) authTestSession {
	t.Helper()
	w := httptest.NewRecorder()
	csrf, err := s.newSession(w, user)
	if err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("fixture did not create a session")
	}
	return authTestSession{cookie: cookies[0], csrf: csrf, user: user}
}

func keysTestFixture(t *testing.T) (*Server, site, site, authTestSession, authTestSession, authTestSession) {
	t.Helper()
	s, owner, other, admin := newSitesServer(t)
	first := createTestSite(t, s, owner, "owner-vault", testUploadFile{"index.html", []byte("<p>owner</p>")})
	second := createTestSite(t, s, other, "other-vault", testUploadFile{"index.html", []byte("<p>other</p>")})
	return s, first, second, keysTestSession(t, s, owner), keysTestSession(t, s, other), keysTestSession(t, s, admin)
}

func keysTestSave(t *testing.T, s *Server, session authTestSession, siteID, name, kind, value, chatID string, status int) *httptest.ResponseRecorder {
	t.Helper()
	body := authTestJSON(t, map[string]string{"name": name, "kind": kind, "value": value, "chat_id": chatID})
	w := authTestRequest(s, "POST", "/api/sites/"+siteID+"/keys", body, &session, "http://app.localhost", session.csrf, s.cfg.AdminHost)
	authTestStatus(t, w, status)
	if len(value) > 20 && strings.Contains(w.Body.String(), value) {
		t.Fatal("vault response disclosed the secret")
	}
	return w
}

func keysTestMetadata(t *testing.T, raw []byte) siteKeyMetadata {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 {
		t.Fatal("key metadata contains unexpected fields")
	}
	for _, field := range []string{"name", "kind", "chat_id", "created_at", "updated_at", "endpoint"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("key metadata missing %s", field)
		}
	}
	var key siteKeyMetadata
	if err := json.Unmarshal(raw, &key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSiteKeysOwnerOnlyOriginCSRFAndHostBoundaries(t *testing.T) {
	s, first, _, owner, other, admin := keysTestFixture(t)
	const secret = "owner-only-secret-distinct-marker-123456"
	keysTestSave(t, s, owner, first.ID, "API_KEY", "generic", secret, "", http.StatusCreated)
	base := "/api/sites/" + first.ID + "/keys"
	for _, outsider := range []authTestSession{other, admin} {
		for _, endpoint := range []struct{ method, path, body string }{
			{"GET", base, ""}, {"POST", base, `{"name":"API_KEY","kind":"generic","value":"attempted replacement"}`}, {"DELETE", base + "/API_KEY", ""},
		} {
			authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, endpoint.body, &outsider, "http://app.localhost", outsider.csrf, s.cfg.AdminHost), http.StatusNotFound)
		}
	}
	for _, endpoint := range []struct{ method, path, body string }{{"GET", base, ""}, {"POST", base, `{}`}, {"DELETE", base + "/API_KEY", ""}} {
		authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, endpoint.body, nil, "http://app.localhost", "", s.cfg.AdminHost), http.StatusUnauthorized)
	}
	for _, endpoint := range []struct{ method, path, body string }{{"POST", base, `{"name":"API_KEY","kind":"generic","value":"attempted replacement"}`}, {"DELETE", base + "/API_KEY", ""}} {
		for _, origin := range []string{"", "null", "http://owner-vault.localhost", "https://app.localhost"} {
			authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, endpoint.body, &owner, origin, owner.csrf, s.cfg.AdminHost), http.StatusForbidden)
		}
		for _, csrf := range []string{"", "incorrect-token"} {
			authTestStatus(t, authTestRequest(s, endpoint.method, endpoint.path, endpoint.body, &owner, "http://app.localhost", csrf, s.cfg.AdminHost), http.StatusForbidden)
		}
	}
	for _, host := range []string{"owner-vault.localhost", "unknown.localhost", "attacker.test"} {
		authTestStatus(t, authTestRequest(s, "GET", base, "", &owner, "", "", host), http.StatusNotFound)
		status := http.StatusNotFound
		if host == "owner-vault.localhost" {
			status = http.StatusMethodNotAllowed
		}
		authTestStatus(t, authTestRequest(s, "POST", base, `{}`, &owner, "http://app.localhost", owner.csrf, host), status)
	}
	_, value, _, err := s.loadSiteSecret(first.ID, "API_KEY")
	if err != nil || value != secret {
		t.Fatal("rejected request changed the owner's secret")
	}
	adminSite := createTestSite(t, s, admin.user, "admin-vault", testUploadFile{"index.html", []byte("admin")})
	keysTestSave(t, s, admin, adminSite.ID, "ADMIN_KEY", "generic", "admin-owned-secret", "", http.StatusCreated)
}

func TestSiteKeysEncryptedWriteOnlyUpsertAndDelete(t *testing.T) {
	s, first, _, owner, _, _ := keysTestFixture(t)
	const secret = "generic-secret-at-rest-marker-THE-END-7z9"
	const token = "123456:telegram_token_at_rest_distinct_marker"
	w := keysTestSave(t, s, owner, first.ID, "API_KEY", "generic", secret, "", http.StatusCreated)
	var created struct {
		Key json.RawMessage `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	metadata := keysTestMetadata(t, created.Key)
	if metadata.Endpoint != "" || metadata.Kind != "generic" || metadata.ChatID != "" {
		t.Fatal("generic key metadata is invalid")
	}
	keysTestSave(t, s, owner, first.ID, "TELEGRAM_BOT", "telegram", token, "-100123456789", http.StatusCreated)
	var encrypted []byte
	var id, createdAt string
	if err := s.db.QueryRow("SELECT id,ciphertext,created_at FROM site_secrets WHERE site_id=? AND name='API_KEY'", first.ID).Scan(&id, &encrypted, &createdAt); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) || len(encrypted) <= len(secret) {
		t.Fatal("database stores a plaintext or unprotected secret")
	}
	for _, name := range []string{"guiweb.sqlite", "guiweb.sqlite-wal"} {
		raw, err := os.ReadFile(filepath.Join(s.cfg.DataDir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte(token)) {
			t.Fatal("SQLite or its WAL contains a plaintext secret")
		}
	}
	keyPath := filepath.Join(s.cfg.DataDir, ".secrets-key")
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != 32 {
		t.Fatal("master key has incorrect permissions or length")
	}
	kind, value, chatID, err := s.loadSiteSecret(first.ID, "API_KEY")
	if err != nil || kind != "generic" || value != secret || chatID != "" {
		t.Fatal("encrypted generic secret did not decrypt")
	}
	// Even an unchanged value receives a fresh random nonce on replacement.
	keysTestSave(t, s, owner, first.ID, "API_KEY", "generic", secret, "", http.StatusOK)
	var replacement []byte
	var newID, newCreated string
	if err := s.db.QueryRow("SELECT id,ciphertext,created_at FROM site_secrets WHERE site_id=? AND name='API_KEY'", first.ID).Scan(&newID, &replacement, &newCreated); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encrypted, replacement) || id != newID || createdAt != newCreated {
		t.Fatal("upsert reused its nonce or changed key identity/creation time")
	}
	base := "/api/sites/" + first.ID + "/keys"
	authTestStatus(t, authTestRequest(s, "POST", base, `{"name":"API_KEY","kind":"generic"}`, &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost), http.StatusBadRequest)
	w = authTestRequest(s, "GET", base, "", &owner, "", "", s.cfg.AdminHost)
	authTestStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), secret[len(secret)-8:]) {
		t.Fatal("list exposes a secret or its tail")
	}
	var listing struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Keys) != 2 {
		t.Fatal("upsert duplicated a key")
	}
	for _, raw := range listing.Keys {
		key := keysTestMetadata(t, raw)
		if key.Name == "TELEGRAM_BOT" && (key.Endpoint != "/__guiweb/telegram/TELEGRAM_BOT" || key.ChatID != "-100123456789") {
			t.Fatal("telegram endpoint metadata is invalid")
		}
	}
	authTestStatus(t, authTestRequest(s, "DELETE", base+"/API_KEY", "", &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost), http.StatusOK)
	if _, _, _, err := s.loadSiteSecret(first.ID, "API_KEY"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted key is still available")
	}
	authTestStatus(t, authTestRequest(s, "DELETE", base+"/API_KEY", "", &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost), http.StatusNotFound)
	if _, value, _, err := s.loadSiteSecret(first.ID, "TELEGRAM_BOT"); err != nil || value != token {
		t.Fatal("deleting a key affected another key")
	}
}

func TestSiteKeysValidationAndEscapedValueLimits(t *testing.T) {
	s, first, _, owner, _, _ := keysTestFixture(t)
	for _, test := range []struct{ name, kind, value, chat string }{
		{"", "generic", "value", ""}, {"lowercase", "generic", "value", ""}, {"_KEY", "generic", "value", ""}, {"A-B", "generic", "value", ""}, {strings.Repeat("A", 65), "generic", "value", ""},
		{"KEY", "", "value", ""}, {"KEY", "unsupported", "value", ""}, {"KEY", "generic", "", ""}, {"KEY", "generic", strings.Repeat("x", 4097), ""}, {"KEY", "generic", "value", "12345"},
		{"KEY", "telegram", "not-a-token", "12345"}, {"KEY", "telegram", "123:valid_suffix", ""}, {"KEY", "telegram", "123:has/slash", "12345"}, {"KEY", "telegram", "123:has space", "12345"},
		{"KEY", "telegram", "123:valid_suffix", "https://attacker.test"}, {"KEY", "telegram", "123:valid_suffix", "123 456"}, {"KEY", "telegram", "123:valid_suffix", "99999999999999999999999"},
		{"KEY", "telegram", "123:valid_suffix", "@"}, {"KEY", "telegram", "123:valid_suffix", "@channel/other"}, {"KEY", "telegram", "123:valid_suffix", "@" + strings.Repeat("a", 33)},
	} {
		keysTestSave(t, s, owner, first.ID, test.name, test.kind, test.value, test.chat, http.StatusBadRequest)
	}
	base := "/api/sites/" + first.ID + "/keys"
	for _, body := range []string{`null`, `[]`, `{}`, `{"name":"KEY","kind":"generic","value":123}`, `{"name":"KEY","kind":"generic","value":"secret","unknown":true}`, `{"name":"KEY","kind":"generic","value":"secret"} {}`, `{"value":"` + strings.Repeat("x", 33000) + `"}`} {
		authTestStatus(t, authTestRequest(s, "POST", base, body, &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost), http.StatusBadRequest)
	}
	// Maximal decoded values are valid even when JSON escapes are much longer.
	maximum := strings.Repeat("\x00", 4096)
	keysTestSave(t, s, owner, first.ID, strings.Repeat("A", 64), "generic", maximum, "", http.StatusCreated)
	if _, value, _, err := s.loadSiteSecret(first.ID, strings.Repeat("A", 64)); err != nil || value != maximum {
		t.Fatal("4096-byte escaped secret did not survive encryption")
	}
	for i, chat := range []string{"123456", "-100123456789", "@channel_name"} {
		keysTestSave(t, s, owner, first.ID, "TELEGRAM_"+strconv.Itoa(i), "telegram", "12345:valid_urlsafe-token", chat, http.StatusCreated)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM site_secrets WHERE site_id=?", first.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatal("invalid values created secret records")
	}
}

func TestSiteKeysCapacityConcurrencyAndDeletionCascade(t *testing.T) {
	s, first, _, owner, _, _ := keysTestFixture(t)
	for i := 0; i < 19; i++ {
		keysTestSave(t, s, owner, first.ID, "KEY_"+strconv.Itoa(i), "generic", "fixture-value", "", http.StatusCreated)
	}
	var tasks sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for i := 19; i < 21; i++ {
		body := authTestJSON(t, map[string]string{"name": "KEY_" + strconv.Itoa(i), "kind": "generic", "value": "fixture-value"})
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			<-start
			results <- authTestRequest(s, "POST", "/api/sites/"+first.ID+"/keys", body, &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost)
		}()
	}
	close(start)
	tasks.Wait()
	close(results)
	created, conflicts := 0, 0
	for result := range results {
		switch result.Code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent save status %d", result.Code)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatal("concurrent saves did not enforce the 20-key limit")
	}
	keysTestSave(t, s, owner, first.ID, "OVER_LIMIT", "generic", "fixture-value", "", http.StatusConflict)
	keysTestSave(t, s, owner, first.ID, "KEY_0", "generic", "replacement-value", "", http.StatusOK)
	base := "/api/sites/" + first.ID + "/keys"
	authTestStatus(t, authTestRequest(s, "DELETE", base+"/KEY_0", "", &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost), http.StatusOK)
	keysTestSave(t, s, owner, first.ID, "REPLACEMENT", "generic", "fixture-value", "", http.StatusCreated)
	authTestStatus(t, authTestRequest(s, "DELETE", "/api/sites/"+first.ID, "", &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost), http.StatusOK)
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM site_secrets WHERE site_id=?", first.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("deleting a website left orphan secrets")
	}
}

func TestSiteKeyStorageQuotaPreservesOldCiphertextAndAllowsDelete(t *testing.T) {
	s, first, _, owner, _, _ := keysTestFixture(t)
	keysTestSave(t, s, owner, first.ID, "EXISTING", "generic", "preserved-secret-value", "", http.StatusCreated)
	var before []byte
	if err := s.db.QueryRow("SELECT ciphertext FROM site_secrets WHERE site_id=? AND name='EXISTING'", first.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	s.cfg.TotalBytes = 1
	keysTestSave(t, s, owner, first.ID, "EXISTING", "generic", "rejected-secret-value", "", http.StatusInsufficientStorage)
	keysTestSave(t, s, owner, first.ID, "NEW_KEY", "generic", "rejected-secret-value", "", http.StatusInsufficientStorage)
	var after []byte
	var count int
	if err := s.db.QueryRow("SELECT ciphertext FROM site_secrets WHERE site_id=? AND name='EXISTING'", first.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM site_secrets WHERE site_id=?", first.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || count != 1 {
		t.Fatal("rejected storage quota write changed secrets")
	}
	s.cfg.TotalBytes = 4 * 1024 * MiB
	s.cfg.MinFreeBytes = 1 << 62
	keysTestSave(t, s, owner, first.ID, "EXISTING", "generic", "rejected-secret-value", "", http.StatusInsufficientStorage)
	authTestStatus(t, authTestRequest(s, "DELETE", "/api/sites/"+first.ID+"/keys/EXISTING", "", &owner, "http://app.localhost", owner.csrf, s.cfg.AdminHost), http.StatusOK)
}

func TestSiteSecretCiphertextBindsSiteNameKindAndChat(t *testing.T) {
	s, first, second, owner, _, _ := keysTestFixture(t)
	const token = "12345:authenticated_fixed_recipient_secret"
	keysTestSave(t, s, owner, first.ID, "TELEGRAM_BOT", "telegram", token, "-100123456789", http.StatusCreated)
	var encrypted []byte
	if err := s.db.QueryRow("SELECT ciphertext FROM site_secrets WHERE site_id=? AND name='TELEGRAM_BOT'", first.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ siteID, name, kind, chatID string }{
		{second.ID, "TELEGRAM_BOT", "telegram", "-100123456789"}, {first.ID, "OTHER_BOT", "telegram", "-100123456789"}, {first.ID, "TELEGRAM_BOT", "generic", "-100123456789"}, {first.ID, "TELEGRAM_BOT", "telegram", "-100999999999"},
	} {
		if value, err := s.decryptSiteSecret(test.siteID, test.name, test.kind, test.chatID, encrypted); err == nil || value != "" {
			t.Fatal("ciphertext decrypted after its authenticated context changed")
		}
	}
	tampered := append([]byte(nil), encrypted...)
	tampered[len(tampered)-1] ^= 1
	if _, err := s.db.Exec("UPDATE site_secrets SET ciphertext=? WHERE site_id=? AND name='TELEGRAM_BOT'", tampered, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, value, _, err := s.loadSiteSecret(first.ID, "TELEGRAM_BOT"); err == nil || value != "" || strings.Contains(err.Error(), token) {
		t.Fatal("tampered ciphertext was accepted or disclosed sensitive data")
	}
}

func TestSiteSecretKeyAndCiphertextSurviveRestart(t *testing.T) {
	cfg := DevConfig(t.TempDir())
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	u := User{ID: "vault-owner", Username: "vault-owner"}
	if _, err := s.db.Exec("INSERT INTO users(id,username,password_hash) VALUES(?,?,?)", u.ID, u.Username, "unused"); err != nil {
		t.Fatal(err)
	}
	authTestSeedSite(t, s, u, "vault-restart", 20)
	session := keysTestSession(t, s, u)
	keysTestSave(t, s, session, "vault-restart", "API_KEY", "generic", "persisted-secret-restart-value", "", http.StatusCreated)
	keyPath := filepath.Join(cfg.DataDir, ".secrets-key")
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("restart replaced the master key")
	}
	if _, value, _, err := s.loadSiteSecret("vault-restart", "API_KEY"); err != nil || value != "persisted-secret-restart-value" {
		t.Fatal("restart lost access to stored secrets")
	}
	authTestStatus(t, authTestRequest(s, "GET", "/api/sites/vault-restart/keys", "", &session, "", "", s.cfg.AdminHost), http.StatusOK)
}

func TestSiteSecretMasterKeyRejectsUnsafeOrPartialFiles(t *testing.T) {
	for _, mode := range []string{"empty", "partial", "oversized", "public-mode", "symlink", "directory", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cfg := DevConfig(filepath.Join(root, "data"))
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(cfg.DataDir, ".secrets-key")
			original, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(keyPath); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "empty":
				err = os.WriteFile(keyPath, nil, 0600)
			case "partial":
				err = os.WriteFile(keyPath, original[:31], 0600)
			case "oversized":
				err = os.WriteFile(keyPath, append(original, 0), 0600)
			case "public-mode":
				err = os.WriteFile(keyPath, original, 0644)
				if err == nil {
					err = os.Chmod(keyPath, 0644)
				}
			case "symlink":
				target := filepath.Join(root, "outside-key")
				if err = os.WriteFile(target, original, 0600); err == nil {
					err = os.Symlink(target, keyPath)
				}
			case "directory":
				err = os.Mkdir(keyPath, 0700)
			case "hardlink":
				target := filepath.Join(root, "outside-key")
				if err = os.WriteFile(target, original, 0600); err == nil {
					err = os.Link(target, keyPath)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if next, err := New(cfg); err == nil {
				next.Close()
				t.Fatal("unsafe or partial master key was accepted")
			}
		})
	}
}

func TestSiteSecretMissingOrReplacedMasterKeyFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			cfg := DevConfig(t.TempDir())
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			u := User{ID: "owner", Username: "owner"}
			if _, err = s.db.Exec("INSERT INTO users(id,username,password_hash) VALUES(?,?,?)", u.ID, u.Username, "unused"); err != nil {
				t.Fatal(err)
			}
			authTestSeedSite(t, s, u, "secret-site", 20)
			session := keysTestSession(t, s, u)
			keysTestSave(t, s, session, "secret-site", "API_KEY", "generic", "cannot-lose-existing-secret", "", http.StatusCreated)
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(cfg.DataDir, ".secrets-key")
			original, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing" {
				err = os.Remove(keyPath)
			} else {
				err = os.WriteFile(keyPath, bytes.Repeat([]byte{7}, 32), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if next, err := New(cfg); err == nil {
				next.Close()
				t.Fatal("startup accepted a missing or wrong master key with existing secrets")
			}
			if mode == "missing" {
				if _, err := os.Lstat(keyPath); !os.IsNotExist(err) {
					t.Fatal("startup silently replaced a missing master key")
				}
			}
			if err = os.WriteFile(keyPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			s, err = New(cfg)
			if err != nil {
				t.Fatal("restoring the original key did not recover the vault")
			}
			if _, value, _, err := s.loadSiteSecret("secret-site", "API_KEY"); err != nil || value != "cannot-lose-existing-secret" {
				t.Fatal("failed startup modified encrypted records")
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
