package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/ratelimit"
)

// KeyFunc extracts the rate-limit key (e.g. client IP, user ID) from a
// request. Callers choose the strategy appropriate to the route being
// protected.
type KeyFunc func(c *gin.Context) string

// ByClientIP is a KeyFunc that rate-limits per client IP address.
func ByClientIP(c *gin.Context) string {
	return c.ClientIP()
}

// RateLimit builds Gin middleware backed by the given Limiter. It is not
// attached to any route by this task; callers (e.g. Task 3's auth routes)
// choose which routes need it and with what key strategy.
func RateLimit(limiter *ratelimit.Limiter, keyFn KeyFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, err := limiter.Allow(c.Request.Context(), keyFn(c))
		if err != nil {
			// Fail open: a Redis outage should not take down the whole
			// application's traffic, only disable rate limiting.
			c.Next()
			return
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
