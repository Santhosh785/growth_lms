package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/auth"
)

// SessionCookieName is the HttpOnly cookie the Go backend sets after a
// successful login, holding the Supabase access token directly (the
// token is itself a signed, self-verifying JWT with its own expiry, so no
// separate server-side session store is needed to trust it).
const SessionCookieName = "lms_session"

const authContextKey = "auth_context"

// AuthContext is what Authenticate resolves from the caller's JWT. It
// intentionally carries only what the token itself asserts (sub, email);
// anything that requires a database lookup (full profile, org
// memberships) is resolved later, once a request-scoped transaction with
// RLS session variables is available.
type AuthContext struct {
	UserID string
	Email  string
}

// AuthContextFromGin returns the AuthContext stored by Authenticate (or
// OptionalAuthenticate), and whether one was present.
func AuthContextFromGin(c *gin.Context) (AuthContext, bool) {
	v, ok := c.Get(authContextKey)
	if !ok {
		return AuthContext{}, false
	}
	ac, ok := v.(AuthContext)
	return ac, ok
}

func bearerOrCookieToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); h != "" {
		if token, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(token)
		}
	}
	if cookie, err := c.Cookie(SessionCookieName); err == nil {
		return cookie
	}
	return ""
}

// Authenticate requires a valid Supabase-issued JWT (bearer header or
// session cookie), storing the resolved AuthContext in the Gin context.
// Responds 401 without leaking why (expired vs malformed vs missing all
// look identical to the client).
func Authenticate(verifier *auth.Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerOrCookieToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		claims, err := verifier.Verify(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		c.Set(authContextKey, AuthContext{UserID: claims.Subject, Email: claims.Email})
		c.Next()
	}
}

// OptionalAuthenticate behaves like Authenticate but never aborts: routes
// that behave differently for anonymous vs logged-in callers (e.g.
// viewing an invitation before deciding whether to sign up or accept
// immediately) use this instead.
func OptionalAuthenticate(verifier *auth.Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerOrCookieToken(c)
		if token == "" {
			c.Next()
			return
		}

		claims, err := verifier.Verify(token)
		if err != nil {
			c.Next()
			return
		}

		c.Set(authContextKey, AuthContext{UserID: claims.Subject, Email: claims.Email})
		c.Next()
	}
}
