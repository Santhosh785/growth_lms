package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/config"
)

func newSecHeadersRouter(env config.Env) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders(&config.Config{Env: env}))
	h := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.GET("/app", h)
	r.GET("/embed/o/acme/catalog", h)
	return r
}

func doGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSecurityHeaders_BaselineOnEveryResponse(t *testing.T) {
	w := doGet(newSecHeadersRouter(config.EnvProduction), "/app")

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := w.Header().Get(k); got != v {
			t.Errorf("header %q = %q, want %q", k, got, v)
		}
	}
	if got := w.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("expected a Content-Security-Policy header on app routes")
	}
	if got := w.Header().Get("Permissions-Policy"); got == "" {
		t.Error("expected a Permissions-Policy header")
	}
}

func TestSecurityHeaders_HSTSOnlyOutsideDevelopment(t *testing.T) {
	dev := doGet(newSecHeadersRouter(config.EnvDevelopment), "/app")
	if got := dev.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("did not expect HSTS in development, got %q", got)
	}

	prod := doGet(newSecHeadersRouter(config.EnvProduction), "/app")
	if got := prod.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("expected HSTS in production")
	}
}

func TestSecurityHeaders_EmbedRoutesRemainFrameable(t *testing.T) {
	w := doGet(newSecHeadersRouter(config.EnvProduction), "/embed/o/acme/catalog")

	if got := w.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("embed route must not set X-Frame-Options, got %q", got)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("embed route should still carry a CSP")
	}
	// The whole point of /embed is third-party framing, so frame-ancestors
	// must not be locked down there.
	if want := "frame-ancestors"; contains(csp, want) {
		t.Errorf("embed CSP must not restrict %q, got %q", want, csp)
	}
	// Baseline non-framing protections still apply.
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("embed route missing nosniff, got %q", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
