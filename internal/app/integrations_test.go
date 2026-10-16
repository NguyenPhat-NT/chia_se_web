package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationNamespaceBypassesUploadedFilesAndSPA(t *testing.T) {
	s, owner, _, _ := newSitesServer(t)
	value := createTestSite(t, s, owner, "namespace",
		testUploadFile{"index.html", []byte("public SPA home")},
		testUploadFile{"__guiweb/uploaded.txt", []byte("uploaded integration impersonation")},
	)
	if _, err := s.db.Exec("UPDATE sites SET spa=1 WHERE id=?", value.ID); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	request := func(method, host, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://"+host+path, nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request(http.MethodGet, "namespace.localhost", "/regular-spa-route"); w.Code != http.StatusOK || w.Body.String() != "public SPA home" {
		t.Fatalf("ordinary SPA route stopped working: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/__guiweb/missing", "/__guiweb/uploaded.txt"} {
		w := request(http.MethodGet, "namespace.localhost", path)
		if w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "public SPA home") || strings.Contains(w.Body.String(), "impersonation") {
			t.Fatalf("reserved integration path %q fell through to website: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut} {
		w := request(method, "namespace.localhost", "/__guiweb/telegram/CONTACT")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("unsupported integration method %q: %d %v", method, w.Code, w.Header())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if w := request(method, s.cfg.AdminHost, "/__guiweb/telegram/CONTACT"); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("public integration route exposed on management host: %d", w.Code)
		}
	}
}
