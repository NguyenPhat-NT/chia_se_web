package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// 4,096 astral characters may occupy 49 KiB when JSON-escaped as pairs.
	telegramRequestLimit  = 64 * 1024
	telegramResponseLimit = 64 * 1024
	telegramRateEntries   = 1024
)

// This interface keeps tests entirely local; the production implementation only
// uses the fixed Telegram HTTPS destination below, with normal TLS verification.
type telegramHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type telegramCounter struct {
	started time.Time
	count   int
}

type telegramState struct {
	client   telegramHTTPClient
	now      func() time.Time
	mu       sync.Mutex
	counters map[string]telegramCounter
	global   telegramCounter
	active   chan struct{}
}

func newTelegramState() *telegramState {
	return &telegramState{
		client: &http.Client{
			// DefaultTransport keeps environment proxy and verified system CA trust.
			Transport:     http.DefaultTransport,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:      time.Now,
		counters: make(map[string]telegramCounter),
		active:   make(chan struct{}, 4),
	}
}

func (s *telegramState) allow(siteID, visitor string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !now.Before(s.global.started.Add(time.Minute)) {
		s.global = telegramCounter{started: now}
	}
	if s.global.count >= 60 {
		return false
	}
	keys := []string{"site:" + siteID, "ip:" + siteID + ":" + visitor}
	missing := func() int {
		n := 0
		for _, key := range keys {
			if _, ok := s.counters[key]; !ok {
				n++
			}
		}
		return n
	}
	if len(s.counters)+missing() > telegramRateEntries {
		for key, counter := range s.counters {
			if !now.Before(counter.started.Add(time.Minute)) {
				delete(s.counters, key)
			}
		}
		if len(s.counters)+missing() > telegramRateEntries {
			return false
		}
	}
	counts := make([]telegramCounter, len(keys))
	for index, key := range keys {
		counter := s.counters[key]
		if !now.Before(counter.started.Add(time.Minute)) {
			counter = telegramCounter{started: now}
		}
		limit := 20
		if index == 1 {
			limit = 5
		}
		if counter.count >= limit {
			return false
		}
		counts[index] = counter
	}
	for index, key := range keys {
		counter := counts[index]
		counter.count++
		s.counters[key] = counter
	}
	s.global.count++
	return true
}

func (s *Server) telegramVisitor(r *http.Request) string {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remote = r.RemoteAddr
	}
	peer := net.ParseIP(remote)
	if peer == nil {
		return "unknown"
	}
	if s.cfg.TrustCloudflare && peer.IsLoopback() {
		if visitor := net.ParseIP(r.Header.Get("CF-Connecting-IP")); visitor != nil && !visitor.IsLoopback() && !visitor.IsUnspecified() {
			return visitor.String()
		}
	}
	return peer.String()
}

func telegramUnavailable(w http.ResponseWriter) {
	fail(w, http.StatusBadGateway, "Chưa gửi được tin nhắn. Vui lòng thử lại sau.")
}

func telegramThrottled(w http.ResponseWriter, seconds string) {
	w.Header().Set("Retry-After", seconds)
	fail(w, http.StatusTooManyRequests, "Bạn gửi quá nhanh. Vui lòng thử lại sau.")
}

func (s *Server) relayTelegram(w http.ResponseWriter, r *http.Request) {
	// The administrative hostname and unknown/nested public hostnames never
	// expose this endpoint, regardless of cookies or supplied request fields.
	host := requestHost(r.Host)
	base := strings.ToLower(s.cfg.BaseDomain)
	if host == "" || host == s.cfg.AdminHost || !strings.HasSuffix(host, "."+base) {
		http.NotFound(w, r)
		return
	}
	slug := strings.TrimSuffix(host, "."+base)
	if !s.validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	var siteID string
	if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM sites WHERE slug=?`, slug).Scan(&siteID); err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, http.StatusMethodNotAllowed, "Phương thức không được hỗ trợ.")
		return
	}
	if r.Header.Get("Origin") != s.cfg.Scheme+"://"+r.Host {
		fail(w, http.StatusForbidden, "Nguồn yêu cầu không được phép.")
		return
	}
	name := r.PathValue("name")
	if !secretNamePattern.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, http.StatusBadRequest, "Yêu cầu gửi tin nhắn không hợp lệ.")
		return
	}
	input, err := io.ReadAll(http.MaxBytesReader(w, r.Body, telegramRequestLimit))
	if err != nil || !utf8.Valid(input) {
		fail(w, http.StatusBadRequest, "Yêu cầu gửi tin nhắn không hợp lệ.")
		return
	}
	var payload struct {
		Text *string `json:"text"`
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil {
		fail(w, http.StatusBadRequest, "Yêu cầu gửi tin nhắn không hợp lệ.")
		return
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF || payload.Text == nil || utf8.RuneCountInString(*payload.Text) > 4096 || strings.TrimSpace(*payload.Text) == "" {
		fail(w, http.StatusBadRequest, "Yêu cầu gửi tin nhắn không hợp lệ.")
		return
	}
	kind, token, chatID, err := s.loadSiteSecret(siteID, name)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "telegram") {
		http.NotFound(w, r)
		return
	}
	if err != nil || !validSiteSecret(name, kind, token, chatID) {
		telegramUnavailable(w)
		return
	}
	state := s.telegram
	if state == nil || state.client == nil {
		telegramUnavailable(w)
		return
	}
	if !state.allow(siteID, s.telegramVisitor(r)) {
		telegramThrottled(w, "60")
		return
	}
	select {
	case state.active <- struct{}{}:
		defer func() { <-state.active }()
	default:
		telegramThrottled(w, "1")
		return
	}
	body, err := json.Marshal(struct {
		ChatID         string `json:"chat_id"`
		Text           string `json:"text"`
		DisablePreview bool   `json:"disable_web_page_preview"`
	}{ChatID: chatID, Text: *payload.Text, DisablePreview: true})
	if err != nil {
		telegramUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// The token is validated as one path segment. Neither the browser nor a
	// generic key can select a host, method, chat, parse mode or upstream URL.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		telegramUnavailable(w)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := state.client.Do(request)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	// URL errors embed the bot token; do not wrap, log or return them.
	if err != nil || response == nil || response.Body == nil {
		telegramUnavailable(w)
		return
	}
	if response.StatusCode == http.StatusTooManyRequests {
		telegramThrottled(w, "60")
		return
	}
	if response.StatusCode != http.StatusOK {
		telegramUnavailable(w)
		return
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, telegramResponseLimit+1))
	if err != nil || len(result) > telegramResponseLimit {
		telegramUnavailable(w)
		return
	}
	var outcome struct {
		OK bool `json:"ok"`
	}
	if err = json.Unmarshal(result, &outcome); err != nil || !outcome.OK {
		telegramUnavailable(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
