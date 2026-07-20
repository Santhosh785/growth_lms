// Package dbctx provides request-scoped Postgres transactions that carry
// the Row-Level Security session variables (app.current_user_id,
// app.current_org_id, app.current_role) every authenticated query relies
// on.
//
// pgxpool connections are shared and recycled across goroutines, and
// Postgres session-local settings (SET LOCAL / set_config(..., true))
// only live for the duration of a single transaction on a single
// connection. So RLS-scoped work must never run against the bare pool:
// every authenticated request acquires one connection, opens a
// transaction, stamps the session variables on it, and releases the
// connection back to the pool only once that transaction commits or
// rolls back.
package dbctx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequestTx is a single Postgres transaction scoped to one HTTP request,
// with the RLS session variables already applied to it.
type RequestTx struct {
	Tx   pgx.Tx
	conn *pgxpool.Conn
}

// Begin acquires a connection from pool, starts a transaction, and applies
// the RLS session variables to it via set_config with query parameters
// (never string-built SET LOCAL, which would risk SQL injection). orgID
// and role may be empty strings for requests that have not yet resolved an
// organization context (e.g. listing a user's own organizations); userID
// may be empty for fully anonymous requests, in which case RLS policies
// that require app_current_user_id() will simply deny access rather than
// erroring.
func Begin(ctx context.Context, pool *pgxpool.Pool, userID, orgID, role string) (*RequestTx, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("dbctx: acquire connection: %w", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()
		return nil, fmt.Errorf("dbctx: begin transaction: %w", err)
	}

	for _, setting := range []struct {
		name  string
		value string
	}{
		{"app.current_user_id", userID},
		{"app.current_org_id", orgID},
		{"app.current_role", role},
	} {
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", setting.name, setting.value); err != nil {
			_ = tx.Rollback(ctx)
			conn.Release()
			return nil, fmt.Errorf("dbctx: set %s: %w", setting.name, err)
		}
	}

	return &RequestTx{Tx: tx, conn: conn}, nil
}

// Commit commits the transaction and releases the underlying connection
// back to the pool.
func (r *RequestTx) Commit(ctx context.Context) error {
	defer r.conn.Release()
	return r.Tx.Commit(ctx)
}

// Rollback rolls back the transaction and releases the underlying
// connection back to the pool. Safe to call after a failed Commit or when
// the request handler errors/panics.
func (r *RequestTx) Rollback(ctx context.Context) error {
	defer r.conn.Release()
	return r.Tx.Rollback(ctx)
}

// SetOrgContext updates the app.current_org_id / app.current_role session
// variables on an already-open RequestTx. Needed because organization
// context (which org, which role) is usually only known after resolving
// :org_slug against the memberships table — a query that itself must run
// inside the transaction so app.current_user_id is already in effect —
// so Begin is called first with an empty org/role and this is called once
// the org is resolved, before any org-scoped query runs.
func SetOrgContext(ctx context.Context, tx pgx.Tx, orgID, role string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
		return fmt.Errorf("dbctx: set app.current_org_id: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_role', $1, true)", role); err != nil {
		return fmt.Errorf("dbctx: set app.current_role: %w", err)
	}
	return nil
}
