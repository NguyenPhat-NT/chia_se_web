package app

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementHTMLVersionsItsAssets(t *testing.T) {
	s := &Server{cfg: DevConfig(t.TempDir())}
	handler := s.Handler()
	assets, err := fs.Sub(webFiles, "web")
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+s.cfg.AdminHost+path, nil))
		return w
	}
	for _, path := range []string{"/", "/index.html"} {
		t.Run(path, func(t *testing.T) {
			page := request(path)
			if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("management HTML: status %d, cache policy %q", page.Code, page.Header().Get("Cache-Control"))
			}
			for _, asset := range []struct{ name, attribute, contentType string }{
				{"app.js", "src", "text/javascript; charset=utf-8"},
				{"style.css", "href", "text/css; charset=utf-8"},
			} {
				data, err := fs.ReadFile(assets, asset.name)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256(data)
				url := fmt.Sprintf("/%s?v=%x", asset.name, hash[:8])
				if !strings.Contains(page.Body.String(), asset.attribute+`="`+url+`"`) {
					t.Fatalf("HTML does not reference the current %s at %s", asset.name, url)
				}
				if strings.Contains(page.Body.String(), asset.attribute+`="/`+asset.name+`"`) {
					t.Fatalf("HTML still references the unversioned %s", asset.name)
				}
				response := request(url)
				if response.Code != http.StatusOK || response.Body.String() != string(data) {
					t.Fatalf("versioned %s does not serve the matching embedded bytes", asset.name)
				}
				if response.Header().Get("Cache-Control") != "no-cache" || response.Header().Get("Content-Type") != asset.contentType {
					t.Fatalf("incorrect response headers for %s: %v", asset.name, response.Header())
				}
				legacy := request("/" + asset.name)
				if legacy.Code != http.StatusOK || legacy.Body.String() != string(data) {
					t.Fatalf("unversioned asset compatibility broken for %s", asset.name)
				}
			}
		})
	}
	if response := request("/missing.js?v=123"); response.Code != http.StatusNotFound {
		t.Fatalf("unknown asset returned %d", response.Code)
	}
}
