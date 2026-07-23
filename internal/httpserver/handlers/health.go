// Package handlers holds HTTP handlers shared by the server (health checks
// today; route handlers land here as later tasks add business logic).
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/metrics"
)

// MetricsHandler serves the process-wide metrics registry in Prometheus text
// exposition format (Task 10 observability). Content-Type matches the
// Prometheus 0.0.4 text format so a scraper negotiates it correctly.
func MetricsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.Default.WriteProm(c.Writer)
	}
}

// Healthz reports liveness only: the process is running and able to serve
// HTTP. It never checks dependencies, so it stays fast and cannot be taken
// down by an unrelated outage.
func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readyz reports readiness: the application can serve real traffic because
// its dependencies (Postgres, Redis) are reachable. Used by the reverse
// proxy to route traffic only to healthy instances.
func Readyz(db *pgxpool.Pool, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		checks := gin.H{}
		healthy := true

		if err := db.Ping(ctx); err != nil {
			checks["database"] = "unreachable"
			healthy = false
		} else {
			checks["database"] = "ok"
		}

		if err := redisClient.Ping(ctx).Err(); err != nil {
			checks["redis"] = "unreachable"
			healthy = false
		} else {
			checks["redis"] = "ok"
		}

		status := http.StatusOK
		if !healthy {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"status": readyStatus(healthy), "checks": checks})
	}
}

func readyStatus(healthy bool) string {
	if healthy {
		return "ready"
	}
	return "not_ready"
}
