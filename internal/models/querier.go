// Package models holds typed repositories over the auth/tenancy tables
// (profiles, organizations, memberships, invitations, audit_events,
// api_tokens). Every repository method takes a Querier rather than a
// concrete pool or transaction type so the same code runs against a
// request-scoped dbctx.RequestTx (the normal case, needed for RLS session
// variables to be in effect), a worker's pool connection, or a test
// harness's transaction.
package models

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// IsUniqueViolation reports whether err (or any error it wraps) is a Postgres
// unique-constraint violation (SQLSTATE 23505). Handlers use it to turn a
// duplicate-key insert into a 409 rather than a generic 500.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
