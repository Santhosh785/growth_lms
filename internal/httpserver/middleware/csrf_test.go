package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCSRFTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/mutate", RequireCSRF(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestRequireCSRF_MissingToken(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireCSRF_MismatchedToken(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "token-a"})
	req.Header.Set(CSRFHeaderName, "token-b")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireCSRF_MatchingToken(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "matching-token"})
	req.Header.Set(CSRFHeaderName, "matching-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireCSRF_GetPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/read", RequireCSRF(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/read", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
