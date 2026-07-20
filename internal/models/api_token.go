package models

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type APIToken struct {
	ID              string
	OrgID           string
	Name            string
	TokenPrefix     string
	CreatedByUserID string
	CreatedAt       time.Time
	RevokedAt       *time.Time
}

type APITokenRepo struct{}

func NewAPITokenRepo() *APITokenRepo { return &APITokenRepo{} }

// generateSecret returns a random 32-byte URL-safe secret and an 8-char
// prefix of it. The prefix is stored in the clear for display/lookup; the
// full secret is bcrypt-hashed and never stored or logged in plaintext.
func generateSecret() (secret, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("models: generate token secret: %w", err)
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	prefix = secret[:8]
	return secret, prefix, nil
}

// Create generates a new API token, persists its bcrypt hash, and returns
// the plaintext secret exactly once — callers must show it to the user
// immediately and never be able to retrieve it again.
func (r *APITokenRepo) Create(ctx context.Context, q Querier, orgID, name, createdByUserID string) (*APIToken, string, error) {
	secret, prefix, err := generateSecret()
	if err != nil {
		return nil, "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("models: hash token secret: %w", err)
	}

	row := q.QueryRow(ctx, `
		INSERT INTO api_tokens (org_id, name, token_hash, token_prefix, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, org_id, name, token_prefix, created_by_user_id, created_at, revoked_at
	`, orgID, name, string(hash), prefix, createdByUserID)

	var t APIToken
	if err := row.Scan(&t.ID, &t.OrgID, &t.Name, &t.TokenPrefix, &t.CreatedByUserID, &t.CreatedAt, &t.RevokedAt); err != nil {
		return nil, "", fmt.Errorf("models: create api token: %w", err)
	}
	return &t, secret, nil
}

func (r *APITokenRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]APIToken, error) {
	rows, err := q.Query(ctx, `
		SELECT id, org_id, name, token_prefix, created_by_user_id, created_at, revoked_at
		FROM api_tokens
		WHERE org_id = $1
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list api tokens: %w", err)
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name, &t.TokenPrefix, &t.CreatedByUserID, &t.CreatedAt, &t.RevokedAt); err != nil {
			return nil, fmt.Errorf("models: scan api token: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list api tokens: %w", err)
	}
	return out, nil
}

func (r *APITokenRepo) Revoke(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("models: revoke api token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// VerifySecret looks up a token by its prefix (via the SECURITY DEFINER
// find_api_token_by_prefix() function, since token auth resolves identity
// FROM the token — no session variables are set yet for ordinary RLS to
// apply) and checks the given secret against its bcrypt hash. Returns
// ErrNotFound if the prefix is unknown, the token was revoked, or the
// secret doesn't match.
func (r *APITokenRepo) VerifySecret(ctx context.Context, q Querier, prefix, secret string) (*APIToken, error) {
	row := q.QueryRow(ctx, `
		SELECT (t).id, (t).org_id, (t).name, (t).token_prefix, (t).created_by_user_id, (t).created_at, (t).revoked_at, (t).token_hash
		FROM (SELECT find_api_token_by_prefix($1) AS t) s
		WHERE (t).id IS NOT NULL
	`, prefix)

	var t APIToken
	var hash string
	if err := row.Scan(&t.ID, &t.OrgID, &t.Name, &t.TokenPrefix, &t.CreatedByUserID, &t.CreatedAt, &t.RevokedAt, &hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: lookup api token: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)); err != nil {
		return nil, ErrNotFound
	}
	return &t, nil
}
