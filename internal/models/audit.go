package models

import (
	"context"
	"encoding/json"
	"fmt"
)

// AuditEvent is a single security/administrative action to record.
// OrgID/UserID/ResourceID are nullable because some events (e.g. a failed
// login for an unrecognized email) have no resolvable org or user.
type AuditEvent struct {
	OrgID        *string
	UserID       *string
	Action       string
	ResourceType string
	ResourceID   *string
	Details      map[string]any
	IPAddress    string
	UserAgent    string
}

type AuditRepo struct{}

func NewAuditRepo() *AuditRepo { return &AuditRepo{} }

// Record inserts an audit_events row. Callers pass the same Querier
// (typically the request's dbctx.RequestTx) used for the mutation being
// audited, so a failure to log rolls back the mutation too — for security
// logging, "the action happened but wasn't recorded" is worse than "the
// action didn't happen."
func (r *AuditRepo) Record(ctx context.Context, q Querier, e AuditEvent) error {
	var details []byte
	if e.Details != nil {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("models: marshal audit details: %w", err)
		}
		details = b
	}

	var ip any
	if e.IPAddress != "" {
		ip = e.IPAddress
	}

	_, err := q.Exec(ctx, `
		INSERT INTO audit_events (org_id, user_id, action, resource_type, resource_id, details, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, e.OrgID, e.UserID, e.Action, nullIfEmpty(e.ResourceType), e.ResourceID, details, ip, nullIfEmpty(e.UserAgent))
	if err != nil {
		return fmt.Errorf("models: record audit event: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
