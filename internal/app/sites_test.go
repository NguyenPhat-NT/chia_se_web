package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testUploadFile struct {
	name string
	body []byte
}

func newSitesServer(t *testing.T) (*Server, User, User, User) {
	t.Helper()
	s, err := New(DevConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	owner := User{ID: "owner", Username: "owner"}
	other := User{ID: "other", Username: "other"}
	admin := User{ID: "admin", Username: "admin", Admin: true}
	for _, user := range []User{owner, other, admin} {
		isAdmin := 0
		if user.Admin {
			isAdmin = 1
		}
		if _, err = s.db.Exec(`INSERT INTO users(id,username,password_hash,admin) VALUES(?,?,?,?)`, user.ID, user.Username, "unused-in-unit-tests", isAdmin); err != nil {
			t.Fatal(err)
		}
	}
	return s, owner, other, admin
}

func uploadRequest(t *testing.T, user User, fields map[string]string, files ...testUploadFile) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "files", "filename": file.name}))
		header.Set("Content-Type", "application/octet-stream")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://app.localhost/api/sites", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r.WithContext(context.WithValue(r.Context(), userContextKey{}, user))
}

func createTestSite(t *testing.T, s *Server, user User, slug string, files ...testUploadFile) site {
	t.Helper()
	r := uploadRequest(t, user, map[string]string{"name": "Website " + slug, "slug": slug}, files...)
	w := httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", w.Code, w.Body.String())
	}
	var data struct {
		Site site `json:"site"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return data.Site
}

func publicRequest(s *Server, host, name, accept string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://"+host+name, nil)
	r.Header.Set("Accept", accept)
	w := httptest.NewRecorder()
	s.serveSite(w, r)
	return w
}

func zipBytes(t *testing.T, files ...testUploadFile) []byte {
	t.Helper()
	var body bytes.Buffer
	w := zip.NewWriter(&body)
	for _, file := range files {
		entry, err := w.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestFolderUploadAndPublicHostIsolation(t *testing.T) {
	s, owner, _, _ := newSitesServer(t)
	value := createTestSite(t, s, owner, "sample",
		testUploadFile{"build/index.html", []byte(`<link rel="stylesheet" href="/css/style.css">hello`)},
		testUploadFile{"build/css/style.css", []byte(`body { color: red; }`)},
		testUploadFile{"build/docs/index.html", []byte(`documentation`)},
	)
	if value.URL != "http://sample.localhost:8088" {
		t.Fatalf("URL %q", value.URL)
	}
	w := publicRequest(s, "sample.localhost:8088", "/", "text/html")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("root: %d %s", w.Code, w.Body.String())
	}
	w = publicRequest(s, "sample.localhost", "/css/style.css", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/css") || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("CSS response: %d %v", w.Code, w.Header())
	}
	w = publicRequest(s, "sample.localhost", "/docs", "")
	if w.Code != 301 || w.Header().Get("Location") != "/docs/" {
		t.Fatalf("directory redirect: %d %v", w.Code, w.Header())
	}
	for _, host := range []string{"unknown.localhost", "nested.sample.localhost", "sample.localhost.evil", "app.localhost", "sample.localhost."} {
		if w = publicRequest(s, host, "/", ""); w.Code != 404 {
			t.Errorf("host %q status=%d", host, w.Code)
		}
	}
	for _, name := range []string{"/css/", "/api/me", "/.env", "/../guiweb.sqlite", "/css%2f..%2findex.html"} {
		if w = publicRequest(s, "sample.localhost", name, ""); w.Code != 404 {
			t.Errorf("path %q status=%d", name, w.Code)
		}
	}
	active, err := s.siteByID(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(s.cfg.DataDir, "guiweb.sqlite"), filepath.Join(s.releasePath(active.ID, active.releaseID), "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if w = publicRequest(s, "sample.localhost", "/leak.txt", ""); w.Code != 404 {
		t.Fatalf("symlink served: %d", w.Code)
	}
}

func TestHTMLReplaceRollbackAndOwnership(t *testing.T) {
	s, owner, other, admin := newSitesServer(t)
	value := createTestSite(t, s, owner, "stable", testUploadFile{"anything.html", []byte("old website")})
	replace := func(user User, fields map[string]string, files ...testUploadFile) *httptest.ResponseRecorder {
		r := uploadRequest(t, user, fields, files...)
		r.SetPathValue("id", value.ID)
		w := httptest.NewRecorder()
		s.replaceSite(w, r)
		return w
	}
	w := replace(other, nil, testUploadFile{"index.html", []byte("intrusion")})
	if w.Code != 404 {
		t.Fatalf("ownership: %d", w.Code)
	}
	w = replace(owner, map[string]string{"mode": "folder"}, testUploadFile{"wrong.txt", []byte("invalid")})
	if w.Code != 400 {
		t.Fatalf("invalid replace: %d %s", w.Code, w.Body.String())
	}
	if w = publicRequest(s, "stable.localhost", "/", ""); w.Body.String() != "old website" {
		t.Fatalf("failed replacement lost active release: %s", w.Body.String())
	}
	w = replace(owner, map[string]string{"name": "ignored rename", "slug": "ignored-slug", "spa": "true"}, testUploadFile{"new.html", []byte("new website")})
	if w.Code != 200 {
		t.Fatalf("replace %d: %s", w.Code, w.Body.String())
	}
	active, err := s.siteByID(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Slug != "stable" || active.Name != value.Name || !active.SPA {
		t.Fatalf("replacement changed identity: %+v", active)
	}
	w = replace(owner, nil, testUploadFile{"newest.html", []byte("newest website")})
	if w.Code != 200 {
		t.Fatalf("replace %d: %s", w.Code, w.Body.String())
	}
	active, _ = s.siteByID(value.ID)
	if !active.SPA {
		t.Fatal("missing SPA field reset configured SPA")
	}
	versions, err := os.ReadDir(filepath.Dir(s.releasePath(value.ID, active.releaseID)))
	if err != nil || len(versions) != 1 {
		t.Fatalf("old releases not cleaned: %v %v", versions, err)
	}
	if w = publicRequest(s, "stable.localhost", "/account/profile", "text/html"); w.Code != 200 || w.Body.String() != "newest website" {
		t.Fatalf("SPA fallback: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"/missing.js", "/api/no-such-resource"} {
		if w = publicRequest(s, "stable.localhost", name, "application/json"); w.Code != 404 {
			t.Fatalf("SPA masked non-html path: %s %d", name, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPatch, "http://app.localhost/api/sites/"+value.ID, strings.NewReader(`{"name":"Tên mới"}`))
	r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, owner))
	r.SetPathValue("id", value.ID)
	w = httptest.NewRecorder()
	s.patchSite(w, r)
	if w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	active, _ = s.siteByID(value.ID)
	if active.Name != "Tên mới" || active.Slug != "stable" {
		t.Fatalf("rename changed link: %+v", active)
	}
	for _, user := range []User{other, admin} {
		r = httptest.NewRequest(http.MethodDelete, "http://app.localhost/api/sites/"+value.ID, nil)
		r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, user))
		r.SetPathValue("id", value.ID)
		w = httptest.NewRecorder()
		s.deleteSite(w, r)
		want := 404
		if user.Admin {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("delete by %s: %d %s", user.Username, w.Code, w.Body.String())
		}
	}
	if w = publicRequest(s, "stable.localhost", "/", ""); w.Code != 404 {
		t.Fatal("deleted hostname still serves content")
	}
	r = uploadRequest(t, other, map[string]string{"name": "replacement owner", "slug": "stable"}, testUploadFile{"index.html", []byte("new owner")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("deleted slug was not released for another member: %d %s", w.Code, w.Body.String())
	}
	if response := publicRequest(s, "stable.localhost", "/", ""); response.Code != http.StatusOK || response.Body.String() != "new owner" {
		t.Fatalf("reused hostname did not serve new content: %d %s", response.Code, response.Body.String())
	}
}

func deleteTestSite(t *testing.T, s *Server, user User, id string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "http://app.localhost/api/sites/"+id, nil)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, user))
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.deleteSite(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status %d: %s", w.Code, w.Body.String())
	}
}

func TestDeletedSlugReusableByAnyMember(t *testing.T) {
	for _, role := range []string{"owner", "member", "admin"} {
		t.Run(role, func(t *testing.T) {
			s, owner, member, admin := newSitesServer(t)
			previous := createTestSite(t, s, owner, "reusable", testUploadFile{"index.html", []byte("previous website")})
			deleteTestSite(t, s, admin, previous.ID)
			if response := publicRequest(s, "reusable.localhost", "/", ""); response.Code != http.StatusNotFound {
				t.Fatalf("deleted website still public: %d", response.Code)
			}
			user := owner
			switch role {
			case "member":
				user = member
			case "admin":
				user = admin
			}
			current := createTestSite(t, s, user, "reusable", testUploadFile{"index.html", []byte("new website by " + role)})
			if current.ID == previous.ID || current.URL != previous.URL || current.OwnerID != user.ID {
				t.Fatalf("recreation did not preserve URL with a new owner/id: old=%+v new=%+v", previous, current)
			}
			if response := publicRequest(s, "reusable.localhost", "/", ""); response.Code != http.StatusOK || response.Body.String() != "new website by "+role {
				t.Fatalf("reused URL content: %d %s", response.Code, response.Body.String())
			}
			for _, contender := range []User{owner, member, admin} {
				r := uploadRequest(t, contender, map[string]string{"name": "duplicate", "slug": "reusable"}, testUploadFile{"index.html", []byte("must not replace")})
				w := httptest.NewRecorder()
				s.createSite(w, r)
				if w.Code != http.StatusConflict {
					t.Fatalf("active name claimed by %s: %d %s", contender.Username, w.Code, w.Body.String())
				}
			}
			if response := publicRequest(s, "reusable.localhost", "/", ""); response.Body.String() != "new website by "+role {
				t.Fatalf("duplicate create changed active content: %s", response.Body.String())
			}
		})
	}
}

func TestLegacyDeletedSlugReleasedAfterRestart(t *testing.T) {
	s, owner, member, _ := newSitesServer(t)
	active := createTestSite(t, s, owner, "kept-live", testUploadFile{"index.html", []byte("preserved live website")})
	// v0.1.0 kept a reservation after deleting the site. Its existing two-column
	// table and schema version must remain readable without a manual DB edit.
	if _, err := s.db.Exec(`INSERT INTO slug_reservations(slug,owner_id) VALUES(?,?)`, "legacy-deleted", owner.ID); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("legacy schema fixture version: %d %v", version, err)
	}
	cfg := s.cfg
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	current := createTestSite(t, reopened, member, "legacy-deleted", testUploadFile{"index.html", []byte("reclaimed after restart")})
	if current.OwnerID != member.ID {
		t.Fatalf("legacy owner prevented transfer: %+v", current)
	}
	if response := publicRequest(reopened, "legacy-deleted.localhost", "/", ""); response.Code != http.StatusOK || response.Body.String() != "reclaimed after restart" {
		t.Fatalf("legacy reused URL: %d %s", response.Code, response.Body.String())
	}
	r := uploadRequest(t, member, map[string]string{"name": "duplicate", "slug": "kept-live"}, testUploadFile{"index.html", []byte("must not replace")})
	w := httptest.NewRecorder()
	reopened.createSite(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("restart released an active name: %d %s", w.Code, w.Body.String())
	}
	if response := publicRequest(reopened, "kept-live.localhost", "/", ""); response.Body.String() != "preserved live website" {
		t.Fatalf("restart damaged active content: %s", response.Body.String())
	}
	if value, err := reopened.siteByID(active.ID); err != nil || value.ID != active.ID {
		t.Fatalf("restart changed active site identity: %+v %v", value, err)
	}
}

func TestFailedSlugRecreationLeavesNameAvailable(t *testing.T) {
	s, owner, member, admin := newSitesServer(t)
	previous := createTestSite(t, s, owner, "retry-slug", testUploadFile{"index.html", []byte("deleted content")})
	deleteTestSite(t, s, owner, previous.ID)
	liveContent := []byte("unrelated active website")
	createTestSite(t, s, member, "unaffected", testUploadFile{"index.html", liveContent})
	// Fail after release installation to exercise the DB transaction rollback,
	// rather than merely rejecting invalid input before changing any state.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_slug_recreation BEFORE INSERT ON sites WHEN NEW.slug='retry-slug' BEGIN SELECT RAISE(ABORT,'forced insert failure'); END;`); err != nil {
		t.Fatal(err)
	}
	r := uploadRequest(t, member, map[string]string{"name": "failed", "slug": "retry-slug"}, testUploadFile{"index.html", []byte("must be rolled back")})
	w := httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("forced transaction failure: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sites WHERE slug=?`, "retry-slug").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed create left an active claim: %d %v", count, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM slug_reservations WHERE slug=?`, "retry-slug").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed create re-reserved released name: %d %v", count, err)
	}
	if response := publicRequest(s, "retry-slug.localhost", "/", ""); response.Code != http.StatusNotFound {
		t.Fatalf("failed create served partial content: %d", response.Code)
	}
	if response := publicRequest(s, "unaffected.localhost", "/", ""); response.Body.String() != string(liveContent) {
		t.Fatalf("failed create changed another website: %s", response.Body.String())
	}
	staged, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "staging"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("failed recreation left staging: %v %v", staged, err)
	}
	stored, err := directoryBytes(filepath.Join(s.cfg.DataDir, "sites"))
	if err != nil || stored != int64(len(liveContent)) {
		t.Fatalf("failed recreation left release content: %d %v", stored, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_slug_recreation`); err != nil {
		t.Fatal(err)
	}
	current := createTestSite(t, s, admin, "retry-slug", testUploadFile{"index.html", []byte("successful retry by another user")})
	if current.ID == previous.ID || current.URL != previous.URL || current.OwnerID != admin.ID {
		t.Fatalf("failed recreation blocked retry: %+v", current)
	}
	if response := publicRequest(s, "retry-slug.localhost", "/", ""); response.Body.String() != "successful retry by another user" {
		t.Fatalf("retry content: %s", response.Body.String())
	}
}

func TestFailedDeleteKeepsWebsiteAndSlugClaim(t *testing.T) {
	s, owner, member, _ := newSitesServer(t)
	previous := createTestSite(t, s, owner, "delete-retry", testUploadFile{"index.html", []byte("must survive failed deletion")})
	if _, err := s.db.Exec(`CREATE TRIGGER fail_slug_delete BEFORE DELETE ON slug_reservations WHEN OLD.slug='delete-retry' BEGIN SELECT RAISE(ABORT,'forced delete failure'); END;`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, "http://app.localhost/api/sites/"+previous.ID, nil)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, owner))
	r.SetPathValue("id", previous.ID)
	w := httptest.NewRecorder()
	s.deleteSite(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("forced delete failure: %d %s", w.Code, w.Body.String())
	}
	if current, err := s.siteByID(previous.ID); err != nil || current.ID != previous.ID {
		t.Fatalf("failed deletion removed site row: %+v %v", current, err)
	}
	if response := publicRequest(s, "delete-retry.localhost", "/", ""); response.Code != http.StatusOK || response.Body.String() != "must survive failed deletion" {
		t.Fatalf("failed deletion damaged active website: %d %s", response.Code, response.Body.String())
	}
	var claimedBy string
	if err := s.db.QueryRow(`SELECT owner_id FROM slug_reservations WHERE slug=?`, "delete-retry").Scan(&claimedBy); err != nil || claimedBy != owner.ID {
		t.Fatalf("failed deletion lost name claim: %q %v", claimedBy, err)
	}
	r = uploadRequest(t, member, map[string]string{"name": "duplicate", "slug": "delete-retry"}, testUploadFile{"index.html", []byte("must not replace")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("failed deletion freed active name: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_slug_delete`); err != nil {
		t.Fatal(err)
	}
	deleteTestSite(t, s, owner, previous.ID)
	current := createTestSite(t, s, member, "delete-retry", testUploadFile{"index.html", []byte("new member after successful deletion")})
	if current.ID == previous.ID || current.URL != previous.URL || current.OwnerID != member.ID {
		t.Fatalf("successful retry failed to release name: %+v", current)
	}
}

type pausedResponseWriter struct {
	header  http.Header
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
	body    bytes.Buffer
}

func (w *pausedResponseWriter) Header() http.Header    { return w.header }
func (w *pausedResponseWriter) WriteHeader(status int) {}
func (w *pausedResponseWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.resume })
	return w.body.Write(data)
}

func TestSlowViewerDoesNotBlockReplacement(t *testing.T) {
	s, owner, _, _ := newSitesServer(t)
	value := createTestSite(t, s, owner, "streaming", testUploadFile{"index.html", []byte("old version remains on the open descriptor")})
	w := &pausedResponseWriter{header: make(http.Header), entered: make(chan struct{}), resume: make(chan struct{})}
	served := make(chan struct{})
	go func() {
		s.serveSite(w, httptest.NewRequest(http.MethodGet, "http://streaming.localhost/", nil))
		close(served)
	}()
	resumed := false
	defer func() {
		if !resumed {
			close(w.resume)
		}
		<-served
	}()
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("response never began streaming")
	}
	replaced := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := uploadRequest(t, owner, nil, testUploadFile{"index.html", []byte("new active version")})
		r.SetPathValue("id", value.ID)
		response := httptest.NewRecorder()
		s.replaceSite(response, r)
		replaced <- response
	}()
	select {
	case response := <-replaced:
		if response.Code != 200 {
			t.Fatalf("replace status %d: %s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow public stream blocked replacement")
	}
	if response := publicRequest(s, "streaming.localhost", "/", ""); response.Body.String() != "new active version" {
		t.Fatalf("new requests did not see replacement: %s", response.Body.String())
	}
	close(w.resume)
	resumed = true
	<-served
	if w.body.String() != "old version remains on the open descriptor" {
		t.Fatalf("active old stream broke after unlink: %q", w.body.String())
	}
}

func TestReconcileRejectsSymlinkAncestorsBeforeCleanup(t *testing.T) {
	for _, target := range []string{"staging", "sites", "container", "releases", "active"} {
		t.Run(target, func(t *testing.T) {
			s, owner, _, _ := newSitesServer(t)
			value := createTestSite(t, s, owner, "preserved", testUploadFile{"index.html", []byte("active")})
			active, err := s.siteByID(value.ID)
			if err != nil {
				t.Fatal(err)
			}
			var replace string
			switch target {
			case "staging":
				replace = filepath.Join(s.cfg.DataDir, "staging")
			case "sites":
				replace = filepath.Join(s.cfg.DataDir, "sites")
			case "container":
				replace = filepath.Join(s.cfg.DataDir, "sites", value.ID)
			case "releases":
				replace = filepath.Join(s.cfg.DataDir, "sites", value.ID, "releases")
			case "active":
				replace = s.releasePath(value.ID, active.releaseID)
			}
			external := filepath.Join(t.TempDir(), "outside")
			if err = os.Rename(replace, external); err != nil {
				t.Fatal(err)
			}
			canary := filepath.Join(external, "unrelated.txt")
			if err = os.WriteFile(canary, []byte("must remain"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(external, replace); err != nil {
				t.Fatal(err)
			}
			if err = s.reconcile(); err == nil {
				t.Fatal("reconciliation accepted a symlink ancestor")
			}
			if data, err := os.ReadFile(canary); err != nil || string(data) != "must remain" {
				t.Fatalf("outside file changed: %q %v", data, err)
			}
		})
	}
}

func TestZIPValidationAndLimits(t *testing.T) {
	s, owner, _, _ := newSitesServer(t)
	createTestSite(t, s, owner, "zipped", testUploadFile{"site.zip", zipBytes(t, testUploadFile{"dist/index.html", []byte("ZIP website")}, testUploadFile{"dist/app.js", []byte("console.log(1)")})})
	if w := publicRequest(s, "zipped.localhost", "/", ""); w.Code != 200 || w.Body.String() != "ZIP website" {
		t.Fatalf("ZIP root: %d %s", w.Code, w.Body.String())
	}
	for _, attack := range []string{"../index.html", "/index.html", `a\index.html`, "C:/index.html", ".git/config", ".env", ".env.production", "a//index.html", "a/./index.html"} {
		t.Run(attack, func(t *testing.T) {
			r := uploadRequest(t, owner, map[string]string{"name": "attack", "slug": "attack"}, testUploadFile{"site.zip", zipBytes(t, testUploadFile{"index.html", []byte("normal")}, testUploadFile{attack, []byte("secret")})})
			w := httptest.NewRecorder()
			s.createSite(w, r)
			if w.Code != 400 {
				t.Fatalf("attack accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
	badZIPs := map[string][]byte{"duplicate": zipBytes(t, testUploadFile{"index.html", []byte("a")}, testUploadFile{"index.html", []byte("b")}), "broken": []byte("not a zip")}
	var symlink bytes.Buffer
	writer := zip.NewWriter(&symlink)
	header := &zip.FileHeader{Name: "index.html", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	entry.Write([]byte("/etc/passwd"))
	writer.Close()
	badZIPs["symlink"] = symlink.Bytes()
	for name, body := range badZIPs {
		r := uploadRequest(t, owner, map[string]string{"name": name, "slug": "invalid"}, testUploadFile{"site.zip", body})
		w := httptest.NewRecorder()
		s.createSite(w, r)
		if w.Code != 400 {
			t.Fatalf("%s status: %d %s", name, w.Code, w.Body.String())
		}
	}
	s.cfg.SiteBytes = 16
	r := uploadRequest(t, owner, map[string]string{"name": "large", "slug": "large"}, testUploadFile{"site.zip", zipBytes(t, testUploadFile{"index.html", bytes.Repeat([]byte("a"), 1024)})})
	w := httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != 413 {
		t.Fatalf("expanded size not limited: %d %s", w.Code, w.Body.String())
	}
	s.cfg.SiteBytes = 1024
	s.cfg.MaxFiles = 1
	r = uploadRequest(t, owner, map[string]string{"name": "count", "slug": "count"}, testUploadFile{"site.zip", zipBytes(t, testUploadFile{"dist/", nil}, testUploadFile{"dist/index.html", []byte("a")})})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != 413 {
		t.Fatalf("metadata count not limited: %d %s", w.Code, w.Body.String())
	}
	staging, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "staging"))
	if err != nil || len(staging) != 0 {
		t.Fatalf("failed uploads left staging: %v %v", staging, err)
	}
}

func TestUploadQuotasAndCrashReconciliation(t *testing.T) {
	s, owner, other, _ := newSitesServer(t)
	s.cfg.UploadBytes = 8
	r := uploadRequest(t, owner, map[string]string{"name": "large", "slug": "large"}, testUploadFile{"large.html", []byte("ninebytes")})
	w := httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != 413 {
		t.Fatalf("raw upload size not limited: %d %s", w.Code, w.Body.String())
	}
	s.cfg.UploadBytes = MiB
	s.cfg.UserBytes = 6
	value := createTestSite(t, s, owner, "first", testUploadFile{"one.html", []byte("1234")})
	r = uploadRequest(t, owner, map[string]string{"name": "second", "slug": "second"}, testUploadFile{"second.html", []byte("5678")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != 413 {
		t.Fatalf("owner quota not limited: %d %s", w.Code, w.Body.String())
	}
	createTestSite(t, s, other, "other", testUploadFile{"other.html", []byte("1234")})
	// A member cannot obtain another member's records through listSites.
	r = httptest.NewRequest(http.MethodGet, "http://app.localhost/api/sites", nil)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, owner))
	w = httptest.NewRecorder()
	s.listSites(w, r)
	var listed struct {
		Sites []site `json:"sites"`
	}
	json.Unmarshal(w.Body.Bytes(), &listed)
	if w.Code != 200 || len(listed.Sites) != 1 || listed.Sites[0].ID != value.ID {
		t.Fatalf("owner list leaked websites: %s", w.Body.String())
	}
	// All active files, ZIP staging and decompressed files count toward global storage.
	baseline, err := directoryBytes(s.cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.TotalBytes = baseline + 2
	r = uploadRequest(t, owner, nil, testUploadFile{"new.html", []byte("new")})
	r.SetPathValue("id", value.ID)
	w = httptest.NewRecorder()
	s.replaceSite(w, r)
	if w.Code != 507 {
		t.Fatalf("temporary storage quota: %d %s", w.Code, w.Body.String())
	}
	if w = publicRequest(s, "first.localhost", "/", ""); w.Body.String() != "1234" {
		t.Fatal("disk failure damaged active release")
	}
	s.cfg.TotalBytes = 4 * 1024 * MiB
	active, err := s.siteByID(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(s.cfg.DataDir, "sites", "orphan", "releases", "lost")
	partial := filepath.Join(s.cfg.DataDir, "sites", "partial")
	if err = os.Mkdir(partial, 0700); err != nil {
		t.Fatal(err)
	}
	old := s.releasePath(value.ID, "old")
	stage := filepath.Join(s.cfg.DataDir, "staging", "interrupted")
	for _, dir := range []string{orphan, old, stage} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "index.html"), []byte("leftover"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.reconcile(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{orphan, old, stage, partial} {
		if _, err = os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("orphan remains: %s %v", dir, err)
		}
	}
	if _, err = os.Stat(filepath.Join(s.releasePath(value.ID, active.releaseID), "index.html")); err != nil {
		t.Fatalf("active release removed: %v", err)
	}
}

func TestAdminUploadsUseGlobalStorageRatherThanMemberQuota(t *testing.T) {
	s, member, _, admin := newSitesServer(t)
	s.cfg.UserBytes = 6
	s.cfg.SiteBytes = 16
	s.cfg.UploadBytes = 32
	previous := createTestSite(t, s, admin, "admin-first", testUploadFile{"index.html", []byte("old!")})
	// The same file fits a member's quota while the admin's existing files
	// already make the next upload exceed the per-member limit.
	createTestSite(t, s, member, "member-first", testUploadFile{"index.html", []byte("html")})
	createTestSite(t, s, admin, "admin-second", testUploadFile{"index.html", []byte("html")})
	r := uploadRequest(t, admin, nil, testUploadFile{"index.html", []byte("ADMINNEW")})
	r.SetPathValue("id", previous.ID)
	w := httptest.NewRecorder()
	s.replaceSite(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin replacement hit member quota: %d %s", w.Code, w.Body.String())
	}
	active, err := s.siteByID(previous.ID)
	if err != nil || active.OwnerID != admin.ID || active.SizeBytes != 8 || active.URL != previous.URL {
		t.Fatalf("admin replacement identity/content: %+v %v", active, err)
	}
	if response := publicRequest(s, "admin-first.localhost", "/", ""); response.Body.String() != "ADMINNEW" {
		t.Fatalf("admin replacement not served: %s", response.Body.String())
	}
	r = uploadRequest(t, member, map[string]string{"name": "over quota", "slug": "member-second"}, testUploadFile{"index.html", []byte("html")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("member quota bypassed: %d %s", w.Code, w.Body.String())
	}
	// Admin quota exemption must not bypass limits on a single website/upload.
	r = uploadRequest(t, admin, map[string]string{"name": "large site", "slug": "admin-large"}, testUploadFile{"index.html", bytes.Repeat([]byte("a"), 17)})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("admin bypassed per-site limit: %d %s", w.Code, w.Body.String())
	}
	s.cfg.UploadBytes = 5
	r = uploadRequest(t, admin, map[string]string{"name": "large upload", "slug": "admin-upload"}, testUploadFile{"index.html", []byte("123456")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("admin bypassed upload limit: %d %s", w.Code, w.Body.String())
	}
	s.cfg.UploadBytes = 32
	baseline, err := directoryBytes(s.cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.TotalBytes = baseline + 2
	r = uploadRequest(t, admin, map[string]string{"name": "disk full", "slug": "admin-total"}, testUploadFile{"index.html", []byte("123")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("admin bypassed physical total quota: %d %s", w.Code, w.Body.String())
	}
	s.cfg.TotalBytes = 512 * MiB
	s.cfg.MinFreeBytes = 1 << 62
	r = uploadRequest(t, admin, map[string]string{"name": "reserve disk", "slug": "admin-reserve"}, testUploadFile{"index.html", []byte("123")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("admin bypassed minimum free storage: %d %s", w.Code, w.Body.String())
	}
	if response := publicRequest(s, "admin-first.localhost", "/", ""); response.Body.String() != "ADMINNEW" {
		t.Fatalf("failed admin uploads damaged active content: %s", response.Body.String())
	}
	staged, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "staging"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("quota failures left staging: %v %v", staged, err)
	}
}

func TestAdminReplacingMemberSiteKeepsOwnersQuota(t *testing.T) {
	s, member, _, admin := newSitesServer(t)
	s.cfg.UserBytes = 6
	s.cfg.SiteBytes = 16
	s.cfg.UploadBytes = 32
	previous := createTestSite(t, s, member, "member-owned", testUploadFile{"index.html", []byte("OLD!")})
	createTestSite(t, s, member, "member-other", testUploadFile{"index.html", []byte("S")})
	r := uploadRequest(t, admin, nil, testUploadFile{"index.html", []byte("sixsix")})
	r.SetPathValue("id", previous.ID)
	w := httptest.NewRecorder()
	s.replaceSite(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("admin bypassed actual member owner's quota: %d %s", w.Code, w.Body.String())
	}
	if response := publicRequest(s, "member-owned.localhost", "/", ""); response.Body.String() != "OLD!" {
		t.Fatalf("failed admin replacement lost member's content: %s", response.Body.String())
	}
	r = uploadRequest(t, admin, nil, testUploadFile{"index.html", []byte("valid")})
	r.SetPathValue("id", previous.ID)
	w = httptest.NewRecorder()
	s.replaceSite(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin replacement within owner's quota failed: %d %s", w.Code, w.Body.String())
	}
	current, err := s.siteByID(previous.ID)
	if err != nil || current.OwnerID != member.ID || current.URL != previous.URL || current.SizeBytes != 5 {
		t.Fatalf("admin replacement transferred ownership or link: %+v %v", current, err)
	}
	if response := publicRequest(s, "member-owned.localhost", "/", ""); response.Body.String() != "valid" {
		t.Fatalf("valid member replacement not served: %s", response.Body.String())
	}
}

func TestSlugReservationAndMultipartPaths(t *testing.T) {
	s, owner, _, _ := newSitesServer(t)
	s.cfg.Reserved = append(s.cfg.Reserved, "in4")
	for _, slug := range []string{"app", "www", "api", "admin", "in4", "a", "-abc", "abc-", "nested.name"} {
		r := uploadRequest(t, owner, map[string]string{"name": "name", "slug": slug}, testUploadFile{"index.html", []byte("hello")})
		w := httptest.NewRecorder()
		s.createSite(w, r)
		if w.Code != 400 {
			t.Errorf("slug %q accepted: %d %s", slug, w.Code, w.Body.String())
		}
	}
	for _, names := range [][]string{{"../index.html"}, {"/index.html"}, {`dist\index.html`}, {"index.html", "index.html"}, {".env"}} {
		var files []testUploadFile
		for _, name := range names {
			files = append(files, testUploadFile{name, []byte("hello")})
		}
		r := uploadRequest(t, owner, map[string]string{"name": "name", "slug": "valid", "mode": "folder"}, files...)
		w := httptest.NewRecorder()
		s.createSite(w, r)
		if w.Code != 400 {
			t.Errorf("unsafe multipart %v accepted: %d %s", names, w.Code, w.Body.String())
		}
	}
	s.uploadMu.Lock()
	r := uploadRequest(t, owner, map[string]string{"name": "busy", "slug": "busy"}, testUploadFile{"index.html", []byte("hello")})
	w := httptest.NewRecorder()
	s.createSite(w, r)
	s.uploadMu.Unlock()
	if w.Code != 409 {
		t.Fatalf("parallel processing not rejected: %d", w.Code)
	}
	createTestSite(t, s, owner, "unique", testUploadFile{"index.html", []byte("hello")})
	r = uploadRequest(t, owner, map[string]string{"name": "duplicate", "slug": "unique"}, testUploadFile{"index.html", []byte("hello")})
	w = httptest.NewRecorder()
	s.createSite(w, r)
	if w.Code != 409 {
		t.Fatalf("duplicate slug accepted: %d %s", w.Code, w.Body.String())
	}
}
