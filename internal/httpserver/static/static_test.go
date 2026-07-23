package static

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegister_ServesHtmx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r)

	req := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/javascript; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc == "" {
		t.Error("expected a Cache-Control header on an immutable asset")
	}
	if w.Body.Len() == 0 {
		t.Error("expected htmx body to be served")
	}
}

func TestRegister_ServesPWAFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r)

	cases := []struct {
		path       string
		wantCTPart string
	}{
		{"/manifest.webmanifest", "manifest+json"},
		{"/sw.js", "javascript"},
		{"/static/app.js", "javascript"},
		{"/static/icon.svg", "svg"},
		{"/offline.html", "html"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", tc.path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !contains(ct, tc.wantCTPart) {
			t.Errorf("%s content-type = %q, want to contain %q", tc.path, ct, tc.wantCTPart)
		}
		if w.Body.Len() == 0 {
			t.Errorf("%s served empty body", tc.path)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
