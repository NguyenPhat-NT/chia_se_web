package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const maxSiteKeys = 20
const maxSecretBytes = 4096

var secretNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramChannelPattern = regexp.MustCompile(`^@[A-Za-z0-9_]{1,32}$`)
var telegramChatPattern = regexp.MustCompile(`^-?[0-9]+$`)
var errSiteSecret = errors.New("Không thể sử dụng kho khóa bảo mật.")

type siteKeyMetadata struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	ChatID    string `json:"chat_id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Endpoint  string `json:"endpoint"`
}

func (key *siteKeyMetadata) setEndpoint() {
	if key.Kind == "telegram" {
		key.Endpoint = "/__guiweb/telegram/" + key.Name
	}
}

// initializeSecrets runs while New holds the data-directory lock. The master
// key is never reconstructed or silently replaced when stored secrets exist.
func (s *Server) initializeSecrets() error {
	path := filepath.Join(s.cfg.DataDir, ".secrets-key")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		var count int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM site_secrets").Scan(&count); err != nil || count != 0 {
			return errSiteSecret
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return errSiteSecret
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return errSiteSecret
		}
		complete := false
		defer func() {
			if !complete {
				_ = os.Remove(path)
			}
		}()
		if err = file.Chmod(0600); err != nil {
			_ = file.Close()
			return errSiteSecret
		}
		n, writeErr := file.Write(key)
		if writeErr == nil && n != len(key) {
			writeErr = io.ErrShortWrite
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errSiteSecret
		}
		directory, err := os.Open(s.cfg.DataDir)
		if err != nil {
			return errSiteSecret
		}
		syncErr := directory.Sync()
		closeErr = directory.Close()
		if syncErr != nil || closeErr != nil {
			return errSiteSecret
		}
		complete = true
		s.secretKey = key
	} else {
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != 32 {
			return errSiteSecret
		}
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errSiteSecret
		}
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 || opened.Size() != 32 {
			_ = file.Close()
			return errSiteSecret
		}
		if stat, ok := opened.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
			_ = file.Close()
			return errSiteSecret
		}
		key, readErr := io.ReadAll(io.LimitReader(file, 33))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(key) != 32 {
			return errSiteSecret
		}
		s.secretKey = key
	}
	// Detect a replaced key or damaged ciphertext before accepting requests.
	rows, err := s.db.Query("SELECT site_id,name,kind,ciphertext,chat_id FROM site_secrets")
	if err != nil {
		return errSiteSecret
	}
	defer rows.Close()
	for rows.Next() {
		var siteID, name, kind, chatID string
		var encrypted []byte
		if err = rows.Scan(&siteID, &name, &kind, &encrypted, &chatID); err != nil {
			return errSiteSecret
		}
		value, err := s.decryptSiteSecret(siteID, name, kind, chatID, encrypted)
		if err != nil || !validSiteSecret(name, kind, value, chatID) {
			return errSiteSecret
		}
	}
	if err = rows.Err(); err != nil {
		return errSiteSecret
	}
	return nil
}

func secretAssociatedData(siteID, name, kind, chatID string) []byte {
	return []byte("guiweb-secret-v1\x00" + siteID + "\x00" + name + "\x00" + kind + "\x00" + chatID)
}

func (s *Server) secretCipher() (cipher.AEAD, error) {
	if len(s.secretKey) != 32 {
		return nil, errSiteSecret
	}
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return nil, errSiteSecret
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errSiteSecret
	}
	return aead, nil
}

func (s *Server) encryptSiteSecret(siteID, name, kind, value, chatID string) ([]byte, error) {
	aead, err := s.secretCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, errSiteSecret
	}
	// Format: version byte, random nonce, authenticated encrypted value.
	encrypted := append([]byte{1}, nonce...)
	return aead.Seal(encrypted, nonce, []byte(value), secretAssociatedData(siteID, name, kind, chatID)), nil
}

func (s *Server) decryptSiteSecret(siteID, name, kind, chatID string, encrypted []byte) (string, error) {
	aead, err := s.secretCipher()
	if err != nil {
		return "", err
	}
	if len(encrypted) <= 1+aead.NonceSize()+aead.Overhead() || encrypted[0] != 1 {
		return "", errSiteSecret
	}
	nonce := encrypted[1 : 1+aead.NonceSize()]
	value, err := aead.Open(nil, nonce, encrypted[1+aead.NonceSize():], secretAssociatedData(siteID, name, kind, chatID))
	if err != nil {
		return "", errSiteSecret
	}
	return string(value), nil
}

// loadSiteSecret is for server-side integrations only. No HTTP response may
// include its value. Missing names remain distinguishable without exposing data.
func (s *Server) loadSiteSecret(siteID, name string) (kind, value, chatID string, err error) {
	var encrypted []byte
	if err = s.db.QueryRow("SELECT kind,ciphertext,chat_id FROM site_secrets WHERE site_id=? AND name=?", siteID, name).Scan(&kind, &encrypted, &chatID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", sql.ErrNoRows
		}
		return "", "", "", errSiteSecret
	}
	value, err = s.decryptSiteSecret(siteID, name, kind, chatID, encrypted)
	if err != nil || !validSiteSecret(name, kind, value, chatID) {
		return "", "", "", errSiteSecret
	}
	return kind, value, chatID, nil
}

func validSiteSecret(name, kind, value, chatID string) bool {
	if !secretNamePattern.MatchString(name) || len(value) < 1 || len(value) > maxSecretBytes {
		return false
	}
	switch kind {
	case "generic":
		return chatID == ""
	case "telegram":
		if !telegramTokenPattern.MatchString(value) || len(chatID) == 0 || len(chatID) > 64 {
			return false
		}
		if telegramChannelPattern.MatchString(chatID) {
			return true
		}
		if !telegramChatPattern.MatchString(chatID) {
			return false
		}
		_, err := strconv.ParseInt(chatID, 10, 64)
		return err == nil
	default:
		return false
	}
}

func (s *Server) secretOwnedSite(w http.ResponseWriter, r *http.Request) (site, bool) {
	u, err := s.user(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "Vui lòng đăng nhập.")
		return site{}, false
	}
	value, err := s.siteByID(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && value.OwnerID != u.ID) {
		fail(w, http.StatusNotFound, "Không tìm thấy website.")
		return site{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không đọc được website.")
		return site{}, false
	}
	return value, true
}

func (s *Server) listSiteKeys(w http.ResponseWriter, r *http.Request) {
	value, ok := s.secretOwnedSite(w, r)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT name,kind,chat_id,created_at,updated_at FROM site_secrets WHERE site_id=? ORDER BY name", value.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không đọc được danh sách khóa.")
		return
	}
	defer rows.Close()
	keys := []siteKeyMetadata{}
	for rows.Next() {
		var key siteKeyMetadata
		if err = rows.Scan(&key.Name, &key.Kind, &key.ChatID, &key.CreatedAt, &key.UpdatedAt); err != nil {
			fail(w, http.StatusInternalServerError, "Không đọc được danh sách khóa.")
			return
		}
		key.setEndpoint()
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		fail(w, http.StatusInternalServerError, "Không đọc được danh sách khóa.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) saveSiteKey(w http.ResponseWriter, r *http.Request) {
	site, ok := s.secretOwnedSite(w, r)
	if !ok {
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "Yêu cầu dữ liệu JSON.")
		return
	}
	var input struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Value  string `json:"value"`
		ChatID string `json:"chat_id"`
	}
	// A 4096-byte value may use six JSON bytes per character after escaping.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fail(w, http.StatusBadRequest, "Thông tin khóa không hợp lệ.")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || !validSiteSecret(input.Name, input.Kind, input.Value, input.ChatID) {
		fail(w, http.StatusBadRequest, "Tên, loại, giá trị hoặc đích nhận của khóa không hợp lệ.")
		return
	}
	// Serialize with uploads so vault writes respect the same physical-storage
	// limits, including a conservative allowance for SQLite/WAL pages.
	if !s.uploadMu.TryLock() {
		fail(w, http.StatusConflict, "Hệ thống đang xử lý dữ liệu. Vui lòng thử lại.")
		return
	}
	defer s.uploadMu.Unlock()
	baseline, err := directoryBytes(s.cfg.DataDir)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
		return
	}
	budget := uploadBudget{cfg: s.cfg, baseline: baseline}
	if err = budget.check(32 * 1024); err != nil {
		var failure *uploadFailure
		if errors.As(err, &failure) {
			fail(w, failure.status, failure.message)
		} else {
			fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
		}
		return
	}
	encrypted, err := s.encryptSiteSecret(site.ID, input.Name, input.Kind, input.Value, input.ChatID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
		return
	}
	defer tx.Rollback()
	var id, createdAt string
	status := http.StatusOK
	err = tx.QueryRowContext(r.Context(), "SELECT id,created_at FROM site_secrets WHERE site_id=? AND name=?", site.ID, input.Name).Scan(&id, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM site_secrets WHERE site_id=?", site.ID).Scan(&count); err != nil {
			fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
			return
		}
		if count >= maxSiteKeys {
			fail(w, http.StatusConflict, "Mỗi website có tối đa 20 khóa.")
			return
		}
		id, err = randomToken(18)
		if err != nil {
			fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
			return
		}
		createdAt = now()
		status = http.StatusCreated
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
		return
	}
	key := siteKeyMetadata{Name: input.Name, Kind: input.Kind, ChatID: input.ChatID, CreatedAt: createdAt, UpdatedAt: now()}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO site_secrets(id,site_id,name,kind,ciphertext,chat_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(site_id,name) DO UPDATE SET kind=excluded.kind,ciphertext=excluded.ciphertext,chat_id=excluded.chat_id,updated_at=excluded.updated_at`, id, site.ID, key.Name, key.Kind, encrypted, key.ChatID, key.CreatedAt, key.UpdatedAt)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, http.StatusInternalServerError, "Không lưu được khóa.")
		return
	}
	key.setEndpoint()
	writeJSON(w, status, map[string]any{"key": key})
}

func (s *Server) deleteSiteKey(w http.ResponseWriter, r *http.Request) {
	site, ok := s.secretOwnedSite(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !secretNamePattern.MatchString(name) {
		fail(w, http.StatusBadRequest, "Tên khóa không hợp lệ.")
		return
	}
	result, err := s.db.ExecContext(r.Context(), "DELETE FROM site_secrets WHERE site_id=? AND name=?", site.ID, name)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không xóa được khóa.")
		return
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		fail(w, http.StatusInternalServerError, "Không xóa được khóa.")
		return
	}
	if deleted == 0 {
		fail(w, http.StatusNotFound, "Không tìm thấy khóa.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}
