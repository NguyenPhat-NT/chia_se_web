package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	cfg        Config
	db         *sql.DB
	uploadMu   sync.Mutex
	filesMu    sync.RWMutex
	passwordMu sync.Mutex
	rateMu     sync.Mutex
	attempts   map[string]attempt
	lock       *os.File
	secretKey  []byte
	telegram   *telegramState
}

type userContextKey struct{}

const schema = `
CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,admin INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),csrf TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS invites(id TEXT PRIMARY KEY,token_hash TEXT UNIQUE NOT NULL,created_by TEXT NOT NULL REFERENCES users(id),expires_at TEXT NOT NULL,used INTEGER NOT NULL DEFAULT 0,revoked INTEGER NOT NULL DEFAULT 0,max_uses INTEGER NOT NULL DEFAULT 1 CHECK(max_uses BETWEEN 1 AND 1000),use_count INTEGER NOT NULL DEFAULT 0 CHECK(use_count BETWEEN 0 AND max_uses));
CREATE TABLE IF NOT EXISTS sites(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id),name TEXT NOT NULL,slug TEXT UNIQUE NOT NULL,release_id TEXT NOT NULL,size_bytes INTEGER NOT NULL,spa INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS slug_reservations(slug TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS site_secrets(id TEXT PRIMARY KEY,site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,name TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('generic','telegram')),ciphertext BLOB NOT NULL,chat_id TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(site_id,name));
INSERT OR IGNORE INTO slug_reservations(slug,owner_id) SELECT slug,owner_id FROM sites;
DELETE FROM slug_reservations WHERE NOT EXISTS (SELECT 1 FROM sites WHERE sites.slug=slug_reservations.slug);
CREATE INDEX IF NOT EXISTS sites_owner ON sites(owner_id);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);
`

// Optional invite columns preserve the schema understood by older releases.
// Reusable invitations keep the legacy used flag set, so older binaries refuse
// them instead of consuming a registration without updating their counters.
func initializeSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(schema); err != nil {
		return err
	}
	rows, err := tx.Query("PRAGMA table_info(invites)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for _, column := range []struct{ name, definition string }{
		{"max_uses", "INTEGER NOT NULL DEFAULT 1 CHECK(max_uses BETWEEN 1 AND 1000)"},
		{"use_count", "INTEGER NOT NULL DEFAULT 0 CHECK(use_count BETWEEN 0 AND max_uses)"},
	} {
		if !columns[column.name] {
			if _, err = tx.Exec("ALTER TABLE invites ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	for _, query := range []string{
		"UPDATE invites SET use_count=1 WHERE max_uses=1 AND used=1 AND use_count=0",
		"UPDATE invites SET used=1 WHERE max_uses>1 AND used=0",
		"PRAGMA user_version=1",
	} {
		if _, err = tx.Exec(query); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func New(cfg Config) (*Server, error) {
	cfg, err := cfg.Validated()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, err
	}
	// Data is managed exclusively by this process, never a user-selected document root.
	info, err := os.Lstat(cfg.DataDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("data_dir phải là thư mục thực")
	}
	if err = prepareDataDir(cfg.DataDir); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(cfg.DataDir, ".lock")
	lock, err := acquireLock(lockPath)
	if err != nil {
		return nil, fmt.Errorf("data_dir đang được sử dụng: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "guiweb.sqlite"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			lock.Close()
			return nil, err
		}
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version > 1 {
		db.Close()
		lock.Close()
		return nil, errors.New("phiên bản cơ sở dữ liệu không được hỗ trợ")
	}
	if err = initializeSchema(db); err != nil {
		db.Close()
		lock.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(cfg.DataDir, "guiweb.sqlite"), 0600); err != nil {
		db.Close()
		lock.Close()
		return nil, err
	}
	for _, dir := range []string{"sites", "staging"} {
		if err = os.MkdirAll(filepath.Join(cfg.DataDir, dir), 0700); err != nil {
			db.Close()
			lock.Close()
			return nil, err
		}
	}
	s := &Server{cfg: cfg, db: db, attempts: map[string]attempt{}, lock: lock, telegram: newTelegramState()}
	if err = s.initializeSecrets(); err != nil {
		db.Close()
		lock.Close()
		return nil, err
	}
	if err = s.reconcile(); err != nil {
		db.Close()
		lock.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) Close() error {
	err := s.db.Close()
	if s.lock != nil {
		s.lock.Close()
	}
	return err
}

func prepareDataDir(dir string) error {
	marker := filepath.Join(dir, ".guiweb")
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		entries, e := os.ReadDir(dir)
		if e != nil {
			return e
		}
		if len(entries) > 0 {
			return errors.New("data_dir phải là thư mục riêng còn trống khi khởi tạo lần đầu")
		}
		f, e := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString("guiweb-v1\n")
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	} else if err != nil {
		return err
	} else {
		if !info.Mode().IsRegular() {
			return errors.New("marker dữ liệu không hợp lệ")
		}
		b, e := os.ReadFile(marker)
		if e != nil || string(b) != "guiweb-v1\n" {
			return errors.New("marker dữ liệu không hợp lệ")
		}
	}
	for _, name := range []string{".lock", ".secrets-key", "guiweb.sqlite", "guiweb.sqlite-wal", "guiweb.sqlite-shm", "sites", "staging"} {
		i, e := os.Lstat(filepath.Join(dir, name))
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if name == "sites" || name == "staging" {
			if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s phải là thư mục thực", name)
			}
		} else if !i.Mode().IsRegular() {
			return fmt.Errorf("%s phải là file thường", name)
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Yêu cầu dữ liệu JSON.")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "Dữ liệu không hợp lệ.")
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		fail(w, 400, "Dữ liệu không hợp lệ.")
		return false
	}
	return true
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", s.health)
	m.HandleFunc("POST /api/login", s.origin(s.login))
	m.HandleFunc("POST /api/register", s.origin(s.register))
	m.HandleFunc("GET /api/me", s.auth(s.me))
	m.HandleFunc("POST /api/logout", s.auth(s.logout))
	m.HandleFunc("GET /api/sites", s.auth(s.listSites))
	m.HandleFunc("POST /api/sites", s.auth(s.createSite))
	m.HandleFunc("PATCH /api/sites/{id}", s.auth(s.patchSite))
	m.HandleFunc("DELETE /api/sites/{id}", s.auth(s.deleteSite))
	m.HandleFunc("POST /api/sites/{id}/upload", s.auth(s.replaceSite))
	m.HandleFunc("GET /api/sites/{id}/keys", s.auth(s.listSiteKeys))
	m.HandleFunc("POST /api/sites/{id}/keys", s.auth(s.saveSiteKey))
	m.HandleFunc("DELETE /api/sites/{id}/keys/{name}", s.auth(s.deleteSiteKey))
	m.HandleFunc("GET /api/invites", s.auth(s.listInvites))
	m.HandleFunc("POST /api/invites", s.auth(s.createInvite))
	m.HandleFunc("DELETE /api/invites/{id}", s.auth(s.revokeInvite))
	assets, _ := fs.Sub(webFiles, "web")
	js, _ := fs.ReadFile(assets, "app.js")
	css, _ := fs.ReadFile(assets, "style.css")
	jsHash, cssHash := sha256.Sum256(js), sha256.Sum256(css)
	// Each HTML response names the assets embedded in this binary. Changing
	// their URLs prevents a browser's older script from using newer markup.
	assetURLs := strings.NewReplacer(
		`src="/app.js"`, fmt.Sprintf(`src="/app.js?v=%x"`, jsHash[:8]),
		`href="/style.css"`, fmt.Sprintf(`href="/style.css?v=%x"`, cssHash[:8]),
	)
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "style.css" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		types := map[string]string{"index.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8"}
		w.Header().Set("Content-Type", types[name])
		w.Header().Set("Cache-Control", "no-cache")
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
			data = []byte(assetURLs.Replace(string(data)))
		}
		_, _ = w.Write(data)
	})
	public := http.NewServeMux()
	public.HandleFunc("/__guiweb/telegram/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		s.relayTelegram(w, r)
	})
	// Keep integration paths out of uploaded files and SPA fallbacks.
	public.HandleFunc("/__guiweb/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
	})
	public.HandleFunc("/", s.serveSite)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		host := requestHost(r.Host)
		if host == "" {
			http.NotFound(w, r)
			return
		}
		if host != s.cfg.AdminHost {
			public.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if !s.cfg.Dev {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		m.ServeHTTP(w, r)
	})
}

func requestHost(raw string) string {
	h := raw
	if strings.Contains(h, ":") {
		var err error
		h, _, err = net.SplitHostPort(raw)
		if err != nil {
			return ""
		}
	}
	if h != strings.ToLower(h) {
		h = strings.ToLower(h)
	}
	if !validHost(h) {
		return ""
	}
	return h
}

func (s *Server) origin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected := s.cfg.Scheme + "://" + s.cfg.AdminHost
		if s.cfg.Dev {
			expected = "http://" + r.Host
		}
		if r.Header.Get("Origin") != expected {
			fail(w, 403, "Nguồn yêu cầu không được phép.")
			return
		}
		next(w, r)
	}
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := s.user(r)
		if err != nil {
			fail(w, 401, "Vui lòng đăng nhập.")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, u))
		if r.Method != "GET" && r.Method != "HEAD" {
			s.origin(func(w http.ResponseWriter, r *http.Request) {
				_, csrf, err := s.session(r)
				if err != nil || !equalToken(csrf, r.Header.Get("X-CSRF-Token")) {
					fail(w, 403, "Phiên xác nhận không hợp lệ. Vui lòng đăng nhập lại.")
					return
				}
				next(w, r)
			})(w, r)
		} else {
			next(w, r)
		}
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		fail(w, 503, "Cơ sở dữ liệu chưa sẵn sàng.")
		return
	}
	f, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "staging"), ".ready-")
	if err != nil {
		fail(w, 503, "Thư mục dữ liệu không ghi được.")
		return
	}
	n := f.Name()
	f.Close()
	os.Remove(n)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
