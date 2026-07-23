package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UnsubscribeToken is an opaque token embedded in email footers. Resolution
// happens on a public unauthenticated endpoint via the resolve_unsubscribe()
// SECURITY DEFINER function, so no session context is required.
type UnsubscribeToken struct {
	Token    string
	UserID   string
	OrgID    *string
	Category *string
}

type UnsubscribeTokenRepo struct{}

func NewUnsubscribeTokenRepo() *UnsubscribeTokenRepo { return &UnsubscribeTokenRepo{} }

// NewToken returns a random 32-byte hex token suitable for an email link.
func (r *UnsubscribeTokenRepo) NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("models: generate unsubscribe token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Create persists an unsubscribe token. A nil orgID = global unsubscribe
// (master opt-out); a nil category with an orgID = all categories in that org.
// Called by the worker at pool privilege when composing an email.
func (r *UnsubscribeTokenRepo) Create(ctx context.Context, q Querier, token, userID string, orgID, category *string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO unsubscribe_tokens (token, user_id, org_id, category)
		VALUES ($1, $2, $3, $4)`, token, userID, orgID, category)
	if err != nil {
		return fmt.Errorf("models: create unsubscribe token: %w", err)
	}
	return nil
}

// Resolve applies an unsubscribe token via the resolve_unsubscribe() function
// and returns the affected user ID. A used or unknown token returns
// ("", ErrNotFound). Idempotent by construction.
func (r *UnsubscribeTokenRepo) Resolve(ctx context.Context, q Querier, token string) (string, error) {
	var userID *string
	if err := q.QueryRow(ctx, `SELECT resolve_unsubscribe($1)`, token).Scan(&userID); err != nil {
		if err == pgx.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("models: resolve unsubscribe: %w", err)
	}
	if userID == nil {
		return "", ErrNotFound
	}
	return *userID, nil
}
