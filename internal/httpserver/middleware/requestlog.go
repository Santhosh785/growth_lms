// Package middleware holds reusable Gin middleware: request ID tagging,
// structured request logging, and rate limiting.
package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDHeader is the header clients may set (and that responses echo)
// to correlate a request across systems.
const RequestIDHeader = "X-Request-ID"

const requestIDContextKey = "request_id"

// RequestID assigns a unique ID to every request, reusing an incoming
// X-Request-ID header if present, and stores it in the Gin context.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(requestIDContextKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// RequestIDFromContext returns the request ID set by RequestID, or "" if
// none is present (e.g. outside of an HTTP request).
func RequestIDFromContext(c *gin.Context) string {
	id, _ := c.Get(requestIDContextKey)
	s, _ := id.(string)
	return s
}

// RequestLogger emits one structured JSON log line per completed request,
// including the request ID and duration. It never logs request/response
// bodies or headers, so it cannot leak secrets such as auth tokens.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		logger.Info("http_request",
			"request_id", RequestIDFromContext(c),
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}
