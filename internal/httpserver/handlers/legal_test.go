package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLegalPages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/privacy", PrivacyPage)
	r.GET("/terms", TermsPage)

	cases := []struct {
		path string
		want string
	}{
		{"/privacy", "Privacy Policy"},
		{"/terms", "Terms of Service"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", tc.path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s content-type = %q, want text/html", tc.path, ct)
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s body missing %q", tc.path, tc.want)
		}
		if !strings.Contains(w.Body.String(), "manifest.webmanifest") {
			t.Errorf("%s should link the PWA manifest", tc.path)
		}
	}
}
