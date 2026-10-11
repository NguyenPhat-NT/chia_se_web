package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type site struct {
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	OwnerID   string `json:"owner_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	URL       string `json:"url"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
	SPA       bool   `json:"spa"`
	releaseID string
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)
var identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

const siteSelect = `SELECT s.id,u.username,s.owner_id,s.name,s.slug,s.release_id,s.size_bytes,s.updated_at,s.spa FROM sites s JOIN users u ON u.id=s.owner_id`

type rowScanner interface{ Scan(...any) error }

func (s *Server) scanSite(row rowScanner) (site, error) {
	var value site
	var spa int
	err := row.Scan(&value.ID, &value.Owner, &value.OwnerID, &value.Name, &value.Slug, &value.releaseID, &value.SizeBytes, &value.UpdatedAt, &spa)
	value.SPA = spa != 0
	value.URL = s.publicURL(value.Slug)
	return value, err
}

func (s *Server) publicURL(slug string) string {
	url := s.cfg.Scheme + "://" + slug + "." + s.cfg.BaseDomain
	if s.cfg.Dev {
		_, port, err := net.SplitHostPort(s.cfg.Listen)
		if err == nil && port != "80" {
			url += ":" + port
		}
	}
	return url
}

func (s *Server) siteByID(id string) (site, error) {
	return s.scanSite(s.db.QueryRow(siteSelect+` WHERE s.id=?`, id))
}

func (s *Server) ownedSite(r *http.Request) (site, User, error) {
	user, err := s.user(r)
	if err != nil {
		return site{}, User{}, uploadError(http.StatusUnauthorized, "Bạn cần đăng nhập.")
	}
	value, err := s.siteByID(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		return value, user, uploadError(http.StatusNotFound, "Không tìm thấy website.")
	}
	if err != nil {
		return value, user, err
	}
	if !user.Admin && value.OwnerID != user.ID {
		return value, user, uploadError(http.StatusNotFound, "Không tìm thấy website.")
	}
	return value, user, nil
}

func validSiteName(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 80 {
		return false
	}
	for _, c := range value {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func (s *Server) validSlug(slug string) bool {
	if !slugPattern.MatchString(slug) {
		return false
	}
	adminLabel := strings.TrimSuffix(strings.ToLower(s.cfg.AdminHost), "."+strings.ToLower(s.cfg.BaseDomain))
	if slug == adminLabel {
		return false
	}
	for _, reserved := range s.cfg.Reserved {
		if strings.EqualFold(slug, reserved) {
			return false
		}
	}
	return true
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	user, err := s.user(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "Bạn cần đăng nhập.")
		return
	}
	query := siteSelect
	var args []any
	if !user.Admin {
		query += ` WHERE s.owner_id=?`
		args = append(args, user.ID)
	}
	query += ` ORDER BY s.updated_at DESC,s.id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không thể đọc danh sách website.")
		return
	}
	defer rows.Close()
	values := []site{}
	for rows.Next() {
		value, err := s.scanSite(rows)
		if err != nil {
			fail(w, http.StatusInternalServerError, "Không thể đọc danh sách website.")
			return
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		fail(w, http.StatusInternalServerError, "Không thể đọc danh sách website.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": values})
}

func (s *Server) ownerAllowance(owner string, replacingBytes int64) (int64, error) {
	var used int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(size_bytes),0) FROM sites WHERE owner_id=?`, owner).Scan(&used); err != nil {
		return 0, err
	}
	if s.cfg.UserBytes <= 0 {
		return s.cfg.SiteBytes, nil
	}
	return max(0, s.cfg.UserBytes-used+replacingBytes), nil
}

func (s *Server) takeUploadLock(w http.ResponseWriter) bool {
	if !s.uploadMu.TryLock() {
		fail(w, http.StatusConflict, "Hệ thống đang xử lý một website khác. Hãy thử lại sau.")
		return false
	}
	return true
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	user, err := s.user(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "Bạn cần đăng nhập.")
		return
	}
	if !s.takeUploadLock(w) {
		return
	}
	defer s.uploadMu.Unlock()
	allowance, err := s.ownerAllowance(user.ID, 0)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	stageID, err := randomToken(18)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	stage := filepath.Join(s.cfg.DataDir, "staging", stageID)
	defer os.RemoveAll(stage)
	upload, err := s.prepareUpload(w, r, stage, allowance)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	if !validSiteName(upload.name) {
		fail(w, http.StatusBadRequest, "Tên website cần có từ 1 đến 80 ký tự.")
		return
	}
	if !s.validSlug(upload.slug) {
		fail(w, http.StatusBadRequest, "Địa chỉ cần 3–40 chữ thường, số hoặc dấu gạch nối; không dùng tên đã dành cho dịch vụ khác.")
		return
	}
	var found int
	err = s.db.QueryRow(`SELECT 1 FROM sites WHERE slug=?`, upload.slug).Scan(&found)
	if err == nil {
		fail(w, http.StatusConflict, "Địa chỉ website này đang được sử dụng.")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		respondUploadError(w, err)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	value := site{ID: id, Owner: user.Username, OwnerID: user.ID, Name: upload.name, Slug: upload.slug, SizeBytes: upload.size, UpdatedAt: now(), SPA: upload.spa, releaseID: stageID}
	if err = s.installRelease(upload, value, false); err != nil {
		respondUploadError(w, err)
		return
	}
	value.URL = s.publicURL(value.Slug)
	writeJSON(w, http.StatusCreated, map[string]any{"site": value})
}

func (s *Server) replaceSite(w http.ResponseWriter, r *http.Request) {
	if !s.takeUploadLock(w) {
		return
	}
	defer s.uploadMu.Unlock()
	value, _, err := s.ownedSite(r)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	allowance, err := s.ownerAllowance(value.OwnerID, value.SizeBytes)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	stageID, err := randomToken(18)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	stage := filepath.Join(s.cfg.DataDir, "staging", stageID)
	defer os.RemoveAll(stage)
	upload, err := s.prepareUpload(w, r, stage, allowance)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	oldRelease := value.releaseID
	value.releaseID, value.SizeBytes, value.UpdatedAt = stageID, upload.size, now()
	if upload.spaSet {
		value.SPA = upload.spa
	}
	// The visible name and hostname are stable across content replacement.
	if err = s.installRelease(upload, value, true); err != nil {
		respondUploadError(w, err)
		return
	}
	s.filesMu.Lock()
	if err = os.RemoveAll(s.releasePath(value.ID, oldRelease)); err != nil {
		log.Printf("website release cleanup deferred: %v", err)
	}
	s.filesMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"site": value})
}

func (s *Server) releasePath(id, release string) string {
	return filepath.Join(s.cfg.DataDir, "sites", id, "releases", release)
}

func (s *Server) installRelease(upload preparedUpload, value site, replacing bool) error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	release := s.releasePath(value.ID, value.releaseID)
	if err := os.MkdirAll(filepath.Dir(release), 0700); err != nil {
		return err
	}
	if err := syncDirectories(upload.directory); err != nil {
		return err
	}
	if err := os.Rename(upload.directory, release); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(release)
		}
	}()
	if err := syncDirectory(filepath.Dir(release)); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(filepath.Dir(release))); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(s.cfg.DataDir, "sites")); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	spa := 0
	if value.SPA {
		spa = 1
	}
	if replacing {
		_, err = tx.Exec(`UPDATE sites SET release_id=?,size_bytes=?,spa=?,updated_at=? WHERE id=?`, value.releaseID, value.SizeBytes, spa, value.UpdatedAt, value.ID)
	} else {
		if _, err = tx.Exec(`INSERT INTO slug_reservations(slug,owner_id) VALUES(?,?) ON CONFLICT(slug) DO UPDATE SET owner_id=excluded.owner_id`, value.Slug, value.OwnerID); err == nil {
			_, err = tx.Exec(`INSERT INTO sites(id,owner_id,name,slug,release_id,size_bytes,spa,updated_at) VALUES(?,?,?,?,?,?,?,?)`, value.ID, value.OwnerID, value.Name, value.Slug, value.releaseID, value.SizeBytes, spa, value.UpdatedAt)
		}
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncDirectories(root string) error {
	return filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDirectory(name)
		}
		return nil
	})
}

func (s *Server) patchSite(w http.ResponseWriter, r *http.Request) {
	if !s.takeUploadLock(w) {
		return
	}
	defer s.uploadMu.Unlock()
	value, _, err := s.ownedSite(r)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	var patch struct {
		Name *string `json:"name"`
		SPA  *bool   `json:"spa"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&patch); err != nil {
		fail(w, http.StatusBadRequest, "Thông tin website không hợp lệ.")
		return
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		fail(w, http.StatusBadRequest, "Thông tin website không hợp lệ.")
		return
	}
	if patch.Name == nil && patch.SPA == nil {
		fail(w, http.StatusBadRequest, "Chưa có thay đổi nào.")
		return
	}
	if patch.Name != nil {
		value.Name = strings.TrimSpace(*patch.Name)
		if !validSiteName(value.Name) {
			fail(w, http.StatusBadRequest, "Tên website cần có từ 1 đến 80 ký tự.")
			return
		}
	}
	if patch.SPA != nil {
		value.SPA = *patch.SPA
	}
	value.UpdatedAt = now()
	spa := 0
	if value.SPA {
		spa = 1
	}
	if _, err = s.db.Exec(`UPDATE sites SET name=?,spa=?,updated_at=? WHERE id=?`, value.Name, spa, value.UpdatedAt, value.ID); err != nil {
		respondUploadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": value})
}

func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request) {
	if !s.takeUploadLock(w) {
		return
	}
	defer s.uploadMu.Unlock()
	value, _, err := s.ownedSite(r)
	if err != nil {
		respondUploadError(w, err)
		return
	}
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		respondUploadError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM sites WHERE id=?`, value.ID); err != nil {
		respondUploadError(w, err)
		return
	}
	if _, err = tx.Exec(`DELETE FROM slug_reservations WHERE slug=?`, value.Slug); err != nil {
		respondUploadError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		respondUploadError(w, err)
		return
	}
	if err = os.RemoveAll(filepath.Join(s.cfg.DataDir, "sites", value.ID)); err != nil {
		log.Printf("website cleanup deferred: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) serveSite(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if strings.Contains(host, ":") {
		name, _, err := net.SplitHostPort(host)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		host = name
	}
	base := strings.ToLower(s.cfg.BaseDomain)
	if !strings.HasSuffix(host, "."+base) {
		http.NotFound(w, r)
		return
	}
	slug := strings.TrimSuffix(host, "."+base)
	if !s.validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	// Resolve and open under the lock. Linux keeps an open file readable after
	// unlink, so slow viewers never block uploads while streaming the response.
	s.filesMu.RLock()
	locked := true
	defer func() {
		if locked {
			s.filesMu.RUnlock()
		}
	}()
	value, err := s.scanSite(s.db.QueryRow(siteSelect+` WHERE s.slug=?`, slug))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	requestPath := r.URL.Path
	if !strings.HasPrefix(requestPath, "/") || strings.ContainsAny(requestPath, "\\\x00") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(requestPath, "/")
	if name != "" {
		if _, err = safeUploadPath(strings.TrimSuffix(name, "/"), false); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	root := s.releasePath(value.ID, value.releaseID)
	for _, directory := range []string{
		filepath.Join(s.cfg.DataDir, "sites"),
		filepath.Join(s.cfg.DataDir, "sites", value.ID),
		filepath.Join(s.cfg.DataDir, "sites", value.ID, "releases"),
		root,
	} {
		if err = requireRealDirectory(directory); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	fileName := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(fileName)
	if err == nil && info.IsDir() {
		if !strings.HasSuffix(requestPath, "/") {
			location := r.URL.EscapedPath() + "/"
			if r.URL.RawQuery != "" {
				location += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, location, http.StatusMovedPermanently)
			return
		}
		fileName = filepath.Join(fileName, "index.html")
		info, err = os.Lstat(fileName)
	}
	if err != nil && value.SPA && path.Ext(name) == "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
		fileName = filepath.Join(root, "index.html")
		info, err = os.Lstat(fileName)
	}
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	// No uploaded symlink can exist, but reject any introduced outside the app.
	relative, err := filepath.Rel(root, fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	check := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		check = filepath.Join(check, component)
		entry, e := os.Lstat(check)
		if e != nil || entry.Mode()&os.ModeSymlink != 0 {
			http.NotFound(w, r)
			return
		}
	}
	file, err := os.Open(fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	s.filesMu.RUnlock()
	locked = false
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Uploaded origins cannot frame the administrative UI; the admin handler
	// supplies its own CSP. Uploaded scripts remain able to run on their origin.
	http.ServeContent(w, r, filepath.Base(fileName), info.ModTime(), file)
}

// reconcile is only called before serving. It removes crash leftovers without
// changing the release referenced by a committed database row.
func (s *Server) reconcile() error {
	rows, err := s.db.Query(`SELECT id,release_id FROM sites`)
	if err != nil {
		return err
	}
	active := map[string]string{}
	for rows.Next() {
		var id, release string
		if err = rows.Scan(&id, &release); err != nil {
			rows.Close()
			return err
		}
		if !identifierPattern.MatchString(id) || !identifierPattern.MatchString(release) {
			rows.Close()
			return fmt.Errorf("invalid site release identifiers")
		}
		active[id] = release
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	staging := filepath.Join(s.cfg.DataDir, "staging")
	siteRoot := filepath.Join(s.cfg.DataDir, "sites")
	for _, directory := range []string{staging, siteRoot} {
		if err := requireRealDirectory(directory); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(siteRoot)
	if err != nil {
		return err
	}
	// Validate every container before deleting anything: following a symlink
	// ancestor during ReadDir + child removal could erase an unrelated directory.
	for _, entry := range entries {
		container := filepath.Join(siteRoot, entry.Name())
		if err := requireRealDirectory(container); err != nil {
			return err
		}
		// A crash may leave an uncommitted container without a releases folder.
		// RemoveAll later removes that orphan without following nested symlinks.
		if _, exists := active[entry.Name()]; exists {
			if err := requireRealDirectory(filepath.Join(container, "releases")); err != nil {
				return err
			}
		}
	}
	for id, release := range active {
		for _, directory := range []string{filepath.Join(siteRoot, id), filepath.Join(siteRoot, id, "releases"), s.releasePath(id, release)} {
			if err := requireRealDirectory(directory); err != nil {
				return err
			}
		}
		info, err := os.Lstat(filepath.Join(s.releasePath(id, release), "index.html"))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("active release for site %s is missing or invalid", id)
		}
	}
	staged, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, entry := range staged {
		if err = os.RemoveAll(filepath.Join(staging, entry.Name())); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		release, exists := active[entry.Name()]
		if !exists {
			if err = os.RemoveAll(filepath.Join(siteRoot, entry.Name())); err != nil {
				return err
			}
			continue
		}
		if !entry.IsDir() {
			return fmt.Errorf("site data path is not a directory")
		}
		releases := filepath.Join(siteRoot, entry.Name(), "releases")
		versions, err := os.ReadDir(releases)
		if err != nil {
			return err
		}
		for _, version := range versions {
			if version.Name() != release {
				if err = os.RemoveAll(filepath.Join(releases, version.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func requireRealDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("data container must be a real directory: %s", directory)
	}
	return nil
}
