package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Keep this fixture independent of schema: deployed v0.1.0 databases have
// exactly these six invitation columns, without either capacity column.
const schemaTestLegacySQL = `
CREATE TABLE users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,admin INTEGER NOT NULL DEFAULT 0);
CREATE TABLE sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),csrf TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE TABLE invites(id TEXT PRIMARY KEY,token_hash TEXT UNIQUE NOT NULL,created_by TEXT NOT NULL REFERENCES users(id),expires_at TEXT NOT NULL,used INTEGER NOT NULL DEFAULT 0,revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE sites(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id),name TEXT NOT NULL,slug TEXT UNIQUE NOT NULL,release_id TEXT NOT NULL,size_bytes INTEGER NOT NULL,spa INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL);
CREATE TABLE slug_reservations(slug TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id));
CREATE INDEX sites_owner ON sites(owner_id);
CREATE INDEX sessions_user ON sessions(user_id);
PRAGMA user_version=1;
`

const schemaTestHTML = "<!doctype html><title>Legacy website</title><p>Keep this release.</p>"

type schemaTestLegacyInvite struct {
	id, token, expiry string
	used, revoked     int
}

func schemaTestDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "guiweb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	return db
}

func schemaTestExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func schemaTestLegacyFixture(t *testing.T) (Config, []schemaTestLegacyInvite) {
	t.Helper()
	cfg := DevConfig(t.TempDir())
	if err := os.WriteFile(filepath.Join(cfg.DataDir, ".guiweb"), []byte("guiweb-v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db := schemaTestDB(t, cfg.DataDir)
	schemaTestExec(t, db, schemaTestLegacySQL)
	schemaTestExec(t, db, "INSERT INTO users VALUES('legacy-owner','legacy_admin','original-password-hash',1)")
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	schemaTestExec(t, db, "INSERT INTO sessions VALUES(?,?,?,?)", tokenHash(strings.Repeat("s", 43)), "legacy-owner", "original-csrf", future)
	invites := []schemaTestLegacyInvite{
		{"legacy-unused", strings.Repeat("a", 43), future, 0, 0},
		{"legacy-used", strings.Repeat("b", 43), future, 1, 0},
		{"legacy-revoked", strings.Repeat("c", 43), future, 0, 1},
		{"legacy-expired", strings.Repeat("d", 43), past, 0, 0},
		{"legacy-used-revoked", strings.Repeat("e", 43), future, 1, 1},
		{"legacy-used-expired", strings.Repeat("f", 43), past, 1, 0},
	}
	for _, invite := range invites {
		schemaTestExec(t, db, "INSERT INTO invites VALUES(?,?,?,?,?,?)", invite.id, tokenHash(invite.token), "legacy-owner", invite.expiry, invite.used, invite.revoked)
	}
	schemaTestExec(t, db, "INSERT INTO sites VALUES(?,?,?,?,?,?,?,?)", "legacy-site", "legacy-owner", "Original website", "legacy", "legacy-release", len(schemaTestHTML), 1, "2026-01-01T00:00:00Z")
	schemaTestExec(t, db, "INSERT INTO slug_reservations VALUES('legacy','legacy-owner')")
	releaseDir := filepath.Join(cfg.DataDir, "sites", "legacy-site", "releases", "legacy-release")
	if err := os.MkdirAll(releaseDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "index.html"), []byte(schemaTestHTML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return cfg, invites
}

func schemaTestColumns(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(invites)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func schemaTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func schemaTestVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != want {
		t.Fatalf("user_version = %d; want %d", version, want)
	}
}

func schemaTestPreserved(t *testing.T, s *Server) {
	t.Helper()
	var username, hash string
	var admin int
	if err := s.db.QueryRow("SELECT username,password_hash,admin FROM users WHERE id='legacy-owner'").Scan(&username, &hash, &admin); err != nil {
		t.Fatal(err)
	}
	if username != "legacy_admin" || hash != "original-password-hash" || admin != 1 {
		t.Fatalf("migration changed existing user: %q %q %d", username, hash, admin)
	}
	var owner, csrf string
	if err := s.db.QueryRow("SELECT user_id,csrf FROM sessions WHERE token_hash=?", tokenHash(strings.Repeat("s", 43))).Scan(&owner, &csrf); err != nil {
		t.Fatal(err)
	}
	if owner != "legacy-owner" || csrf != "original-csrf" {
		t.Fatal("migration changed existing session")
	}
	var name, slug, release, updated string
	var size, spa int
	if err := s.db.QueryRow("SELECT owner_id,name,slug,release_id,size_bytes,spa,updated_at FROM sites WHERE id='legacy-site'").Scan(&owner, &name, &slug, &release, &size, &spa, &updated); err != nil {
		t.Fatal(err)
	}
	if owner != "legacy-owner" || name != "Original website" || slug != "legacy" || release != "legacy-release" || size != len(schemaTestHTML) || spa != 1 || updated != "2026-01-01T00:00:00Z" {
		t.Fatal("migration changed the live website row")
	}
	if err := s.db.QueryRow("SELECT owner_id FROM slug_reservations WHERE slug='legacy'").Scan(&owner); err != nil || owner != "legacy-owner" {
		t.Fatalf("migration changed the active slug reservation: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://legacy.localhost/", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != schemaTestHTML {
		t.Fatalf("live website after migration = %d %q", w.Code, w.Body.String())
	}
	session := &authTestSession{cookie: &http.Cookie{Name: s.cookieName(), Value: strings.Repeat("s", 43)}}
	w = authTestRequest(s, http.MethodGet, "/api/me", "", session, "", "", s.cfg.AdminHost)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "legacy_admin") {
		t.Fatalf("existing session stopped working after migration: %d %s", w.Code, w.Body.String())
	}
}

func schemaTestRegister(t *testing.T, s *Server, token, username string, wantStatus int) {
	t.Helper()
	body := authTestJSON(t, map[string]string{"token": token, "username": username, "password": authTestPassword})
	w := authTestRequest(s, http.MethodPost, "/api/register", body, nil, "http://"+s.cfg.AdminHost, "", s.cfg.AdminHost)
	authTestStatus(t, w, wantStatus)
}

func TestSchemaMigrationPreservesLegacyDataAndInviteValidity(t *testing.T) {
	cfg, invites := schemaTestLegacyFixture(t)
	s := schemaTestServer(t, cfg)
	schemaTestVersion(t, s.db, 1)
	schemaTestPreserved(t, s)
	for _, invite := range invites {
		var hash, owner, expiry string
		var used, revoked, maxUses, useCount int
		if err := s.db.QueryRow("SELECT token_hash,created_by,expires_at,used,revoked,max_uses,use_count FROM invites WHERE id=?", invite.id).Scan(&hash, &owner, &expiry, &used, &revoked, &maxUses, &useCount); err != nil {
			t.Fatal(err)
		}
		if hash != tokenHash(invite.token) || owner != "legacy-owner" || expiry != invite.expiry || used != invite.used || revoked != invite.revoked || maxUses != 1 || useCount != invite.used {
			t.Fatalf("migration changed invite %s or did not backfill it safely: max=%d count=%d used=%d revoked=%d", invite.id, maxUses, useCount, used, revoked)
		}
	}
	// Re-running startup must neither reactivate nor increment any invitation.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = schemaTestServer(t, cfg)
	schemaTestPreserved(t, s)
	for _, invite := range invites {
		var used, revoked, maxUses, useCount int
		if err := s.db.QueryRow("SELECT used,revoked,max_uses,use_count FROM invites WHERE id=?", invite.id).Scan(&used, &revoked, &maxUses, &useCount); err != nil {
			t.Fatal(err)
		}
		if used != invite.used || revoked != invite.revoked || maxUses != 1 || useCount != invite.used {
			t.Fatalf("repeat migration changed legacy invitation %s: max=%d count=%d used=%d revoked=%d", invite.id, maxUses, useCount, used, revoked)
		}
	}
	for _, invite := range invites[1:] {
		schemaTestRegister(t, s, invite.token, "member_"+strings.ReplaceAll(invite.id, "-", "_"), http.StatusBadRequest)
	}
	schemaTestRegister(t, s, invites[0].token, "legacy_member", http.StatusOK)
	schemaTestRegister(t, s, invites[0].token, "second_member", http.StatusBadRequest)
	var count, maxUses, used int
	if err := s.db.QueryRow("SELECT use_count,max_uses,used FROM invites WHERE id='legacy-unused'").Scan(&count, &maxUses, &used); err != nil {
		t.Fatal(err)
	}
	if count != 1 || maxUses != 1 || used != 1 {
		t.Fatalf("legacy single-use invitation changed capacity: %d/%d used=%d", count, maxUses, used)
	}
	schemaTestPreserved(t, s)
}

func TestSchemaMigrationKeepsReusableCapacityAcrossRestarts(t *testing.T) {
	cfg, _ := schemaTestLegacyFixture(t)
	s := schemaTestServer(t, cfg)
	token := strings.Repeat("r", 43)
	expiry := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	// A pre-existing reusable row may need the legacy gate repaired, but its
	// consumption counter must survive initialization without a backfill.
	schemaTestExec(t, s.db, "INSERT INTO invites(id,token_hash,created_by,expires_at,used,revoked,max_uses,use_count) VALUES(?,?,?,?,0,0,5,2)", "reusable", tokenHash(token), "legacy-owner", expiry)
	for cycle := 0; cycle < 3; cycle++ {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s = schemaTestServer(t, cfg)
		schemaTestVersion(t, s.db, 1)
		var count, maxUses, used int
		if err := s.db.QueryRow("SELECT use_count,max_uses,used FROM invites WHERE id='reusable'").Scan(&count, &maxUses, &used); err != nil {
			t.Fatal(err)
		}
		if count != 2 || maxUses != 5 || used != 1 {
			t.Fatalf("restart %d changed reusable invitation: %d/%d used=%d", cycle, count, maxUses, used)
		}
		var legacyEligible int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM invites WHERE token_hash=? AND used=0 AND revoked=0 AND expires_at>?", tokenHash(token), now()).Scan(&legacyEligible); err != nil {
			t.Fatal(err)
		}
		if legacyEligible != 0 {
			t.Fatal("older binary could admit a member without counting reusable capacity")
		}
		schemaTestPreserved(t, s)
	}
	schemaTestRegister(t, s, token, "reusable_member", http.StatusOK)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = schemaTestServer(t, cfg)
	var count, used int
	if err := s.db.QueryRow("SELECT use_count,used FROM invites WHERE id='reusable'").Scan(&count, &used); err != nil {
		t.Fatal(err)
	}
	if count != 3 || used != 1 {
		t.Fatalf("restart changed consumed reusable capacity: count=%d used=%d", count, used)
	}
	schemaTestRegister(t, s, token, "reusable_after_restart", http.StatusOK)
	if err := s.db.QueryRow("SELECT use_count,used FROM invites WHERE id='reusable'").Scan(&count, &used); err != nil {
		t.Fatal(err)
	}
	if count != 4 || used != 1 {
		t.Fatalf("reusable capacity unavailable after restart: count=%d used=%d", count, used)
	}
}

func TestSchemaMigrationFailureRollsBackOptionalColumns(t *testing.T) {
	cfg, invites := schemaTestLegacyFixture(t)
	db := schemaTestDB(t, cfg.DataDir)
	schemaTestExec(t, db, "CREATE TRIGGER reject_backfill BEFORE UPDATE ON invites WHEN NEW.id='legacy-used' BEGIN SELECT RAISE(ABORT,'deliberate migration failure'); END")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := New(cfg); err == nil {
		s.Close()
		t.Fatal("startup succeeded despite an aborted invitation backfill")
	} else if !strings.Contains(err.Error(), "deliberate migration failure") {
		t.Fatalf("startup failed before the intended migration error: %v", err)
	}
	db = schemaTestDB(t, cfg.DataDir)
	wantColumns := []string{"id", "token_hash", "created_by", "expires_at", "used", "revoked"}
	if columns := schemaTestColumns(t, db); !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("failed migration left a partial ALTER: %v", columns)
	}
	schemaTestVersion(t, db, 1)
	for _, invite := range invites {
		var used, revoked int
		if err := db.QueryRow("SELECT used,revoked FROM invites WHERE id=?", invite.id).Scan(&used, &revoked); err != nil {
			t.Fatal(err)
		}
		if used != invite.used || revoked != invite.revoked {
			t.Fatalf("failed migration changed legacy invite %s", invite.id)
		}
	}
	// Once the diagnosed failure is removed, startup can migrate the same DB
	// and serve the original website instead of requiring a fresh data directory.
	schemaTestExec(t, db, "DROP TRIGGER reject_backfill")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := schemaTestServer(t, cfg)
	schemaTestPreserved(t, s)
	schemaTestVersion(t, s.db, 1)
}

func TestSchemaMigrationRejectsFutureVersionWithoutAlteringLegacyTables(t *testing.T) {
	cfg, _ := schemaTestLegacyFixture(t)
	db := schemaTestDB(t, cfg.DataDir)
	schemaTestExec(t, db, "PRAGMA user_version=2")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := New(cfg); err == nil {
		s.Close()
		t.Fatal("startup accepted an unsupported future schema")
	} else if !strings.Contains(err.Error(), "phiên bản") {
		t.Fatalf("startup failed for a different reason: %v", err)
	}
	db = schemaTestDB(t, cfg.DataDir)
	schemaTestVersion(t, db, 2)
	wantColumns := []string{"id", "token_hash", "created_by", "expires_at", "used", "revoked"}
	if columns := schemaTestColumns(t, db); !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("unsupported database was migrated: %v", columns)
	}
}
