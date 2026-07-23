package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/dbctx"
)

const requestTxContextKey = "request_tx"

// RequestTxFromGin returns the pgx.Tx opened by WithRequestTx for this
// request. Handlers and downstream middleware (ResolveOrg, RequireRole,
// repository calls) must query through this, never through a raw pool —
// only this transaction has the RLS session variables applied.
func RequestTxFromGin(c *gin.Context) (pgx.Tx, bool) {
	v, ok := c.Get(requestTxContextKey)
	if !ok {
		return nil, false
	}
	rtx, ok := v.(*dbctx.RequestTx)
	if !ok {
		return nil, false
	}
	return rtx.Tx, true
}

// WithRequestTx opens a request-scoped transaction stamped with the
// caller's user ID (from AuthContext, if Authenticate ran first) via
// dbctx.Begin, and commits it if the handler chain completes without
// error, or rolls it back otherwise (including on panic, via defer — a
// panic must never leak a pooled connection).
//
// Organization context (app.current_org_id / app.current_role) is not
// set here: it is usually resolved by querying memberships against this
// same transaction (see ResolveOrg), so it must be applied afterwards via
// dbctx.SetOrgContext, before any org-scoped query runs.
func WithRequestTx(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var userID string
		if ac, ok := AuthContextFromGin(c); ok {
			userID = ac.UserID
		}

		rtx, err := dbctx.Begin(c.Request.Context(), pool, userID, "", "")
		if err != nil {
			// A failure to even open the transaction is a database-health
			// signal, not a per-handler error — emit a throttled DB alert.
			alertRequestTxFailure(err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Set(requestTxContextKey, rtx)

		committed := false
		defer func() {
			if !committed {
				_ = rtx.Rollback(c.Request.Context())
			}
		}()

		c.Next()

		if len(c.Errors) > 0 || c.Writer.Status() >= http.StatusInternalServerError {
			return
		}
		if err := rtx.Commit(c.Request.Context()); err == nil {
			committed = true
		}
	}
}
