package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const telegramFixtureToken = "123456789:FixtureBotToken_NotARealCredential_123"
const telegramFixtureChat = "-1001234567890"

type telegramTransport func(*http.Request) (*http.Response, error)

func (f telegramTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func telegramResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func storeRelaySecret(t *testing.T, s *Server, siteID, name, kind, value, chatID string) {
	t.Helper()
	ciphertext, err := s.encryptSiteSecret(siteID, name, kind, value, chatID)
	if err != nil {
		t.Fatal("cannot encrypt relay fixture")
	}
	id, err := randomToken(18)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO site_secrets(id,site_id,name,kind,ciphertext,chat_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(site_id,name) DO UPDATE SET kind=excluded.kind,ciphertext=excluded.ciphertext,chat_id=excluded.chat_id,updated_at=excluded.updated_at`, id, siteID, name, kind, ciphertext, chatID, now(), now())
	if err != nil {
		t.Fatal("cannot store relay fixture")
	}
}

func newTelegramFixture(t *testing.T) (*Server, site, site) {
	t.Helper()
	s, owner, other, _ := newSitesServer(t)
	alpha := createTestSite(t, s, owner, "relay-alpha", testUploadFile{"index.html", []byte("alpha")})
	beta := createTestSite(t, s, other, "relay-beta", testUploadFile{"index.html", []byte("beta")})
	storeRelaySecret(t, s, alpha.ID, "BOT", "telegram", telegramFixtureToken, telegramFixtureChat)
	s.telegram.client.(*http.Client).Transport = telegramTransport(func(*http.Request) (*http.Response, error) { return telegramResponse(200, `{"ok":true}`), nil })
	return s, alpha, beta
}

func relayRequest(s *Server, host, name, body, origin, remote string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://"+host+"/__guiweb/telegram/"+name, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if remote != "" {
		r.RemoteAddr = remote
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func alphaRelay(s *Server, body string) *httptest.ResponseRecorder {
	return relayRequest(s, "relay-alpha.localhost:8088", "BOT", body, "http://relay-alpha.localhost:8088", "198.51.100.1:1234", nil)
}

func TestTelegramRelayUsesOnlyStoredFixedTarget(t *testing.T) {
	s, _, _ := newTelegramFixture(t)
	var calls int
	s.telegram.client.(*http.Client).Transport = telegramTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "api.telegram.org" || r.URL.Path != "/bot"+telegramFixtureToken+"/sendMessage" || r.URL.RawQuery != "" || r.URL.User != nil {
			t.Error("relay changed fixed Telegram destination")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Origin") != "" {
			t.Error("relay forwarded browser credentials or origin")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("upstream body is not JSON")
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Error("relay did not bound upstream deadline")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error("invalid upstream JSON")
		}
		if len(body) != 3 || body["chat_id"] != telegramFixtureChat || body["text"] != "Xin chào <b>plain text</b>" || body["disable_web_page_preview"] != true {
			t.Error("upstream parameters differ from fixed chat and plain message")
		}
		return telegramResponse(200, `{"ok":true,"result":{"token":"`+telegramFixtureToken+`","chat_id":"`+telegramFixtureChat+`"}}`), nil
	})
	w := relayRequest(s, "relay-alpha.localhost:8088", "BOT", `{"text":"Xin chào <b>plain text</b>"}`, "http://relay-alpha.localhost:8088", "198.51.100.1:1234", map[string]string{"Cookie": "guiweb_dev_session=not-a-session", "Authorization": "Bearer not-a-secret"})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true}` || calls != 1 {
		t.Fatalf("relay result status=%d calls=%d", w.Code, calls)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("relay added CORS, cookies or cacheability")
	}
}

func TestTelegramRelayHostAndSiteIsolation(t *testing.T) {
	s, alpha, beta := newTelegramFixture(t)
	storeRelaySecret(t, s, beta.ID, "ONLY_BETA", "telegram", "987654321:OtherFixtureBot_123", "@fixture_channel")
	storeRelaySecret(t, s, alpha.ID, "GENERIC", "generic", "generic-fixture-value", "")
	for _, test := range []struct {
		host, name, origin string
		status             int
	}{
		{"app.localhost:8088", "BOT", "http://app.localhost:8088", 405},
		{"unknown.localhost:8088", "BOT", "http://unknown.localhost:8088", 404},
		{"nested.relay-alpha.localhost:8088", "BOT", "http://nested.relay-alpha.localhost:8088", 404},
		{"relay-alpha.localhost.evil:8088", "BOT", "http://relay-alpha.localhost.evil:8088", 404},
		{"relay-beta.localhost:8088", "BOT", "http://relay-beta.localhost:8088", 404},
		{"relay-alpha.localhost:8088", "ONLY_BETA", "http://relay-alpha.localhost:8088", 404},
		{"relay-alpha.localhost:8088", "GENERIC", "http://relay-alpha.localhost:8088", 404},
		{"relay-alpha.localhost:8088", "missing", "http://relay-alpha.localhost:8088", 404},
		{"relay-alpha.localhost:8088", "BOT", "", 403},
		{"relay-alpha.localhost:8088", "BOT", "null", 403},
		{"relay-alpha.localhost:8088", "BOT", "http://relay-beta.localhost:8088", 403},
		{"relay-alpha.localhost:8088", "BOT", "https://relay-alpha.localhost:8088", 403},
		{"relay-alpha.localhost:8088", "BOT", "http://relay-alpha.localhost", 403},
	} {
		w := relayRequest(s, test.host, test.name, `{"text":"hello"}`, test.origin, "198.51.100.1:1234", nil)
		if w.Code != test.status {
			t.Errorf("host/name/origin isolation host=%q status=%d want=%d", test.host, w.Code, test.status)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://app.localhost:8088/__guiweb/telegram/BOT", strings.NewReader(`{"text":"hello"}`))
	r.SetPathValue("name", "BOT")
	r.Header.Set("Origin", "http://app.localhost:8088")
	r.Header.Set("Content-Type", "application/json")
	adminResponse := httptest.NewRecorder()
	s.relayTelegram(adminResponse, r)
	if adminResponse.Code != 404 {
		t.Fatalf("relay handler accepted administrative host: %d", adminResponse.Code)
	}
	var calls atomic.Int32
	s.telegram.client.(*http.Client).Transport = telegramTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Path != "/bot987654321:OtherFixtureBot_123/sendMessage" {
			t.Error("public host loaded another owner's token")
		}
		var body struct {
			ChatID string `json:"chat_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.ChatID != "@fixture_channel" {
			t.Error("public host loaded another destination")
		}
		return telegramResponse(200, `{"ok":true}`), nil
	})
	w := relayRequest(s, "relay-beta.localhost:8088", "ONLY_BETA", `{"text":"hello"}`, "http://relay-beta.localhost:8088", "198.51.100.1:1234", nil)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("second site's own key not usable: %d", w.Code)
	}
	// Deleted and subsequently reused hostnames must not inherit a previous
	// owner's integration, even though public site slugs are reusable.
	deleteTestSite(t, s, User{ID: alpha.OwnerID, Username: alpha.Owner}, alpha.ID)
	if w = alphaRelay(s, `{"text":"hello"}`); w.Code != 404 {
		t.Fatalf("deleted site still relays: %d", w.Code)
	}
	createTestSite(t, s, User{ID: beta.OwnerID, Username: beta.Owner}, alpha.Slug, testUploadFile{"index.html", []byte("new owner")})
	if w = alphaRelay(s, `{"text":"hello"}`); w.Code != 404 || calls.Load() != 1 {
		t.Fatalf("reused hostname inherited key: status=%d", w.Code)
	}
}

func TestTelegramRelayStrictInputAndUnicodeBoundary(t *testing.T) {
	s, _, _ := newTelegramFixture(t)
	var calls int
	s.telegram.client.(*http.Client).Transport = telegramTransport(func(*http.Request) (*http.Response, error) { calls++; return telegramResponse(200, `{"ok":true}`), nil })
	for _, body := range []string{
		`{}`, `null`, `{"text":null}`, `{"text":123}`, `{"text":""}`, `{"text":"  \n"}`,
		`{"text":"hello","chat_id":"elsewhere"}`, `{"text":"hello","token":"supplied"}`,
		`{"text":"hello","url":"http://127.0.0.1"}`, `{"text":"hello","method":"getUpdates"}`,
		`{"text":"hello","parse_mode":"HTML"}`, `{"text":"hello"} {"text":"second"}`,
		`{"text":"` + strings.Repeat("a", 4097) + `"}`, strings.Repeat("x", telegramRequestLimit+1),
		string([]byte{'{', '"', 't', 'e', 'x', 't', '"', ':', '"', 0xff, '"', '}'}),
	} {
		if w := alphaRelay(s, body); w.Code != 400 {
			t.Errorf("invalid input accepted with status=%d", w.Code)
		}
	}
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/json; invalid"} {
		w := relayRequest(s, "relay-alpha.localhost:8088", "BOT", `{"text":"hello"}`, "http://relay-alpha.localhost:8088", "198.51.100.1:1234", map[string]string{"Content-Type": contentType})
		if w.Code != 400 {
			t.Errorf("non-JSON input status=%d", w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("rejected input reached Telegram")
	}
	// JSON encoding expands astral characters to twelve bytes each when escaped.
	// Both literal UTF-8 and escaped 4,096-character messages must fit the body cap.
	text := strings.Repeat("😀", 4096)
	literal, _ := json.Marshal(map[string]string{"text": text})
	escaped := `{"text":"` + strings.Repeat(`\ud83d\ude00`, 4096) + `"}`
	for _, body := range []string{string(literal), escaped} {
		if w := alphaRelay(s, body); w.Code != 200 {
			t.Errorf("4096 Unicode characters rejected: status=%d", w.Code)
		}
	}
	if calls != 2 {
		t.Fatalf("Unicode boundary requests=%d", calls)
	}
}

func TestTelegramRelayDoesNotFollowRedirectOrExposeErrors(t *testing.T) {
	s, _, _ := newTelegramFixture(t)
	var logs bytes.Buffer
	originalLog := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(originalLog)
	tests := []struct {
		name      string
		status    int
		transport telegramTransport
	}{
		{"transport-error", 502, func(*http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Post", URL: "https://api.telegram.org/bot" + telegramFixtureToken + "/sendMessage", Err: errors.New("fixture failure")}
		}},
		{"redirect", 502, func(*http.Request) (*http.Response, error) {
			response := telegramResponse(302, telegramFixtureToken)
			response.Header.Set("Location", "http://127.0.0.1:1/steal")
			return response, nil
		}},
		{"provider-error", 502, func(*http.Request) (*http.Response, error) {
			return telegramResponse(500, telegramFixtureToken+telegramFixtureChat), nil
		}},
		{"provider-not-ok", 502, func(*http.Request) (*http.Response, error) {
			return telegramResponse(200, `{"ok":false,"description":"`+telegramFixtureToken+`"}`), nil
		}},
		{"malformed-provider-json", 502, func(*http.Request) (*http.Response, error) { return telegramResponse(200, `{"ok":true} trailing`), nil }},
		{"missing-provider-ok", 502, func(*http.Request) (*http.Response, error) { return telegramResponse(200, `{"result":{}}`), nil }},
		{"provider-throttle", 429, func(*http.Request) (*http.Response, error) { return telegramResponse(429, telegramFixtureToken), nil }},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			// Retain the actual production client's redirect and timeout policy.
			s.telegram = newTelegramState()
			s.telegram.client.(*http.Client).Transport = telegramTransport(func(r *http.Request) (*http.Response, error) { calls++; return test.transport(r) })
			w := relayRequest(s, "relay-alpha.localhost:8088", "BOT", `{"text":"hello"}`, "http://relay-alpha.localhost:8088", fmt.Sprintf("198.51.100.%d:1234", index+1), nil)
			if w.Code != test.status || calls != 1 {
				t.Fatalf("upstream failure status=%d calls=%d", w.Code, calls)
			}
			if strings.Contains(w.Body.String(), telegramFixtureToken) || strings.Contains(w.Body.String(), telegramFixtureChat) || strings.Contains(w.Body.String(), "api.telegram.org") || strings.Contains(logs.String(), telegramFixtureToken) {
				t.Fatal("relay exposed upstream credentials or URL")
			}
		})
	}
	client := newTelegramState().client.(*http.Client)
	if client.Timeout != 10*time.Second || client.Transport != http.DefaultTransport || client.CheckRedirect == nil {
		t.Fatal("production client lost timeout, proxy/TLS transport or redirect policy")
	}
}

type countedTelegramBody struct {
	remaining, read int
	closed          bool
}

func (b *countedTelegramBody) Read(buffer []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(buffer), b.remaining)
	for i := range buffer[:n] {
		buffer[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return n, nil
}
func (b *countedTelegramBody) Close() error { b.closed = true; return nil }

func TestTelegramRelayBoundsResponseAndRejectsCorruptSecret(t *testing.T) {
	s, alpha, _ := newTelegramFixture(t)
	body := &countedTelegramBody{remaining: 1024 * 1024}
	var calls int
	s.telegram.client.(*http.Client).Transport = telegramTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	})
	if w := alphaRelay(s, `{"text":"hello"}`); w.Code != 502 {
		t.Fatalf("oversized response accepted: %d", w.Code)
	}
	if body.read > telegramResponseLimit+1 || !body.closed {
		t.Fatalf("response not bounded/closed: read=%d closed=%v", body.read, body.closed)
	}
	if _, err := s.db.Exec(`UPDATE site_secrets SET chat_id=? WHERE site_id=? AND name=?`, "-1009999999999", alpha.ID, "BOT"); err != nil {
		t.Fatal(err)
	}
	if w := alphaRelay(s, `{"text":"hello"}`); w.Code != 502 || calls != 1 {
		t.Fatalf("changed fixed recipient passed authenticated storage: status=%d", w.Code)
	}
	storeRelaySecret(t, s, alpha.ID, "BOT", "telegram", "123:bad/../../getUpdates", telegramFixtureChat)
	if w := alphaRelay(s, `{"text":"hello"}`); w.Code != 502 || calls != 1 {
		t.Fatalf("invalid upstream path token reached client: status=%d", w.Code)
	}
}

func TestTelegramRelayRateCapsAndExpiry(t *testing.T) {
	t.Run("IP and site", func(t *testing.T) {
		state := newTelegramState()
		instant := time.Now()
		state.now = func() time.Time { return instant }
		for index := 0; index < 5; index++ {
			if !state.allow("one", "198.51.100.1") {
				t.Fatal("IP allowance ended early")
			}
		}
		if state.allow("one", "198.51.100.1") {
			t.Fatal("IP cap bypassed")
		}
		if !state.allow("two", "198.51.100.1") {
			t.Fatal("IP cap leaked across sites")
		}
		for index := 0; index < 15; index++ {
			if !state.allow("one", fmt.Sprintf("203.0.113.%d", index+1)) {
				t.Fatal("site allowance ended early")
			}
		}
		if state.allow("one", "203.0.113.200") {
			t.Fatal("site cap bypassed with a different IP")
		}
		instant = instant.Add(time.Minute + time.Second)
		if !state.allow("one", "198.51.100.1") {
			t.Fatal("rate limits did not expire")
		}
	})
	t.Run("global", func(t *testing.T) {
		state := newTelegramState()
		for index := 0; index < 60; index++ {
			if !state.allow(fmt.Sprintf("site-%d", index/20), fmt.Sprintf("203.0.113.%d", index+1)) {
				t.Fatal("global allowance ended early")
			}
		}
		if state.allow("fresh", "203.0.113.200") {
			t.Fatal("global cap bypassed with a fresh site/IP")
		}
	})
	t.Run("bounded map", func(t *testing.T) {
		state := newTelegramState()
		instant := time.Now()
		state.now = func() time.Time { return instant }
		for index := 0; index < telegramRateEntries; index++ {
			state.counters[fmt.Sprint(index)] = telegramCounter{started: instant, count: 1}
		}
		if state.allow("new", "203.0.113.1") || len(state.counters) != telegramRateEntries {
			t.Fatal("live rate entries were evicted or map grew beyond cap")
		}
		instant = instant.Add(time.Minute + time.Second)
		if !state.allow("new", "203.0.113.1") || len(state.counters) > telegramRateEntries {
			t.Fatal("expired rate entries did not release capacity")
		}
	})
	s, _, _ := newTelegramFixture(t)
	for index := 0; index < 5; index++ {
		if w := alphaRelay(s, `{"text":"hello"}`); w.Code != 200 {
			t.Fatalf("rate allowance ended early: %d", w.Code)
		}
	}
	if w := alphaRelay(s, `{"text":"hello"}`); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("HTTP IP rate response=%d", w.Code)
	}
}

func TestTelegramVisitorOnlyTrustsConfiguredLoopbackTunnel(t *testing.T) {
	s, _, _ := newTelegramFixture(t)
	for _, test := range []struct {
		trust                   bool
		remote, forwarded, want string
	}{
		{false, "127.0.0.1:1234", "203.0.113.4", "127.0.0.1"},
		{true, "198.51.100.1:1234", "203.0.113.4", "198.51.100.1"},
		{true, "127.0.0.1:1234", "203.0.113.4", "203.0.113.4"},
		{true, "[::1]:1234", "2001:db8::4", "2001:db8::4"},
		{true, "127.0.0.1:1234", "invalid, forged", "127.0.0.1"},
		{true, "127.0.0.1:1234", "127.0.0.2", "127.0.0.1"},
		{true, "127.0.0.1:1234", "0.0.0.0", "127.0.0.1"},
		{true, "invalid", "203.0.113.4", "unknown"},
	} {
		s.cfg.TrustCloudflare = test.trust
		r := httptest.NewRequest("POST", "http://relay-alpha.localhost/", nil)
		r.RemoteAddr = test.remote
		r.Header.Set("CF-Connecting-IP", test.forwarded)
		if got := s.telegramVisitor(r); got != test.want {
			t.Errorf("visitor trust mismatch for trust=%v remote=%q", test.trust, test.remote)
		}
	}
}

func TestTelegramRelayBoundsConcurrentUpstreamRequests(t *testing.T) {
	s, _, _ := newTelegramFixture(t)
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	defer releaseAll()
	s.telegram.client.(*http.Client).Transport = telegramTransport(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return telegramResponse(200, `{"ok":true}`), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	results := make(chan int, 4)
	for index := 0; index < 4; index++ {
		go func(index int) {
			results <- relayRequest(s, "relay-alpha.localhost:8088", "BOT", `{"text":"hello"}`, "http://relay-alpha.localhost:8088", fmt.Sprintf("198.51.100.%d:1234", index+1), nil).Code
		}(index)
	}
	for index := 0; index < 4; index++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent request did not reach mock provider")
		}
	}
	w := relayRequest(s, "relay-alpha.localhost:8088", "BOT", `{"text":"hello"}`, "http://relay-alpha.localhost:8088", "198.51.100.100:1234", nil)
	if w.Code != 429 {
		t.Fatalf("fifth simultaneous upstream request accepted: %d", w.Code)
	}
	releaseAll()
	for index := 0; index < 4; index++ {
		if status := <-results; status != 200 {
			t.Fatalf("active request failed: %d", status)
		}
	}
}
