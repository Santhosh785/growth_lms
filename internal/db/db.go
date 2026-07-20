// Package db builds the shared Postgres connection pool used by the API
// and worker processes.
package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds a connection pool against the given Postgres URL. It does
// not eagerly connect: a Postgres outage at startup must not prevent the
// HTTP server (and its /healthz, /readyz endpoints) from coming up. Actual
// reachability is checked continuously by the /readyz handler.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, databaseURL)
}
