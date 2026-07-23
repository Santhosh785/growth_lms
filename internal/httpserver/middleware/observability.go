package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/metrics"
	"growth-lms/internal/models"
)

// dbAlertSink emits a throttled database-category system_alert when the
// request transaction cannot be opened — the clearest single signal that
// Postgres is unreachable or the pool is exhausted. It is throttled in-process
// to at most one alert per throttleWindow so a sustained outage (which would
// otherwise fail every incoming request) produces a single alert row, not a
// flood. Configured once at startup via ConfigureDBAlerting; WithRequestTx
// reads the package-level sink so its own signature stays unchanged across its
// ~20 call sites.
type dbAlertSink struct {
	pool   *pgxpool.Pool
	alerts *models.AlertRepo

	mu       sync.Mutex
	lastEmit time.Time
}

const dbAlertThrottleWindow = 5 * time.Minute

var dbAlerting *dbAlertSink

// ConfigureDBAlerting wires the process-wide database-alert sink used by
// WithRequestTx. Safe to call once at startup; a nil sink (never configured)
// simply disables DB-connectivity alerting.
func ConfigureDBAlerting(pool *pgxpool.Pool, alerts *models.AlertRepo) {
	dbAlerting = &dbAlertSink{pool: pool, alerts: alerts}
}

func (s *dbAlertSink) emit(cause error) {
	s.mu.Lock()
	if !s.lastEmit.IsZero() && time.Since(s.lastEmit) < dbAlertThrottleWindow {
		s.mu.Unlock()
		return
	}
	s.lastEmit = time.Now()
	s.mu.Unlock()

	// Detached, bounded context: the triggering request's context may already
	// be cancelled (client gone / DB timeout), but the alert must still record.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.alerts.Record(ctx, s.pool, models.SystemAlert{
		Severity: models.AlertSeverityCritical,
		Category: models.AlertCategoryDatabase,
		Source:   "request_tx",
		Message:  "failed to open request transaction (database unreachable or pool exhausted): " + cause.Error(),
	})
}

// alertRequestTxFailure is called by WithRequestTx when dbctx.Begin fails.
func alertRequestTxFailure(cause error) {
	if dbAlerting != nil {
		dbAlerting.emit(cause)
	}
}

// Recover replaces gin.Recovery for Task 10's error-tracking requirement: it
// catches a panic in any downstream handler, logs it as a structured error
// correlated with the request ID (so an operator can join the panic to the
// RequestLogger line and any client-reported X-Request-ID), counts it in the
// metrics registry, and returns a generic 500 without leaking the panic value
// or stack to the client. It must be registered before RequestID so the
// deferred recover still sees the request-ID context — RequestID sets it on
// the same *gin.Context, which exists for the whole chain, so ordering after
// RequestID is fine too; we mount it right after RequestID in server.go.
func Recover(logger *slog.Logger, reg *metrics.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				reg.IncPanic()
				logger.Error("panic_recovered",
					"request_id", RequestIDFromContext(c),
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"panic", err,
					"stack", string(debug.Stack()),
				)
				// Abort with a generic 500. WithRequestTx's deferred rollback
				// still runs because the panic already unwound past it into
				// this recover — but to be safe against a panic that happens
				// before the tx defer, we only write a response here.
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				} else {
					c.Abort()
				}
			}
		}()
		c.Next()
	}
}

// Metrics records one histogram observation and request counter per completed
// request into the given registry. Path is intentionally not a label (see
// metrics.Registry) — cardinality is bounded to method × status.
func Metrics(reg *metrics.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		reg.ObserveRequest(c.Request.Method, statusClass(c.Writer.Status()), time.Since(start).Seconds())
	}
}

// statusClass collapses a status code to its class (2xx, 4xx, …) to keep the
// status label low-cardinality while still distinguishing success from client
// and server errors.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}
