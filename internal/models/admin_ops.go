package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AdminOpsRepo is the data-access layer behind Task 10's platform-owner
// administrative actions: suspending users, deactivating organizations,
// forcing a course's publish status across org boundaries, and reading the
// platform-wide user directory and audit log. Every mutating method routes
// through a SECURITY DEFINER SQL function (migration 000017) that re-checks
// app_is_platform_owner() itself, so these are defence-in-depth over the
// RequirePlatformOwner route gate, never a substitute for it.
type AdminOpsRepo struct{}

func NewAdminOpsRepo() *AdminOpsRepo { return &AdminOpsRepo{} }

// SetUserSuspended suspends (suspend=true) or reactivates a user. Reason is
// stored only when suspending. Returns ErrNotFound if the target user does
// not exist or is itself a platform owner (which the function refuses to
// suspend). The SQL function raises insufficient_privilege if the caller is
// not a platform owner; that surfaces here as a wrapped error, not ErrNotFound.
func (r *AdminOpsRepo) SetUserSuspended(ctx context.Context, q Querier, userID string, suspend bool, reason string) error {
	var affected bool
	if err := q.QueryRow(ctx, `SELECT admin_set_user_suspended($1, $2, $3)`,
		userID, suspend, nullIfEmpty(reason)).Scan(&affected); err != nil {
		return fmt.Errorf("models: set user suspended: %w", err)
	}
	if !affected {
		return ErrNotFound
	}
	return nil
}

// SetOrgActive deactivates (active=false) or reactivates an organization.
func (r *AdminOpsRepo) SetOrgActive(ctx context.Context, q Querier, orgID string, active bool, reason string) error {
	var affected bool
	if err := q.QueryRow(ctx, `SELECT admin_set_org_active($1, $2, $3)`,
		orgID, active, nullIfEmpty(reason)).Scan(&affected); err != nil {
		return fmt.Errorf("models: set org active: %w", err)
	}
	if !affected {
		return ErrNotFound
	}
	return nil
}

// SetCourseStatus forces a course's status regardless of org membership,
// used for platform takedown ("archived") and restore.
func (r *AdminOpsRepo) SetCourseStatus(ctx context.Context, q Querier, courseID, status string) error {
	var affected bool
	if err := q.QueryRow(ctx, `SELECT admin_set_course_status($1, $2)`,
		courseID, status).Scan(&affected); err != nil {
		return fmt.Errorf("models: set course status: %w", err)
	}
	if !affected {
		return ErrNotFound
	}
	return nil
}

// AdminUser is one row of the platform-wide user directory.
type AdminUser struct {
	ID              string
	Email           string
	FullName        *string
	IsPlatformOwner bool
	SuspendedAt     *time.Time
	SuspendedReason *string
	CreatedAt       time.Time
	OrgCount        int
}

// UserFilter narrows and paginates the user directory.
type UserFilter struct {
	Search        string // case-insensitive email substring
	SuspendedOnly bool
	Limit         int
	Offset        int
}

// ListUsers returns the platform-wide user directory (platform owner only).
func (r *AdminOpsRepo) ListUsers(ctx context.Context, q Querier, f UserFilter) ([]AdminUser, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.Query(ctx, `SELECT id, email, full_name, is_platform_owner, suspended_at, suspended_reason, created_at, org_count
		FROM admin_list_users($1, $2, $3, $4)`,
		nullIfEmpty(f.Search), f.SuspendedOnly, limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("models: list users: %w", err)
	}
	defer rows.Close()

	out := make([]AdminUser, 0, limit)
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.IsPlatformOwner,
			&u.SuspendedAt, &u.SuspendedReason, &u.CreatedAt, &u.OrgCount); err != nil {
			return nil, fmt.Errorf("models: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AuditEventRow is one row returned by the audit-log viewer.
type AuditEventRow struct {
	ID           string
	OrgID        *string
	UserID       *string
	Action       string
	ResourceType *string
	ResourceID   *string
	Details      map[string]any
	IPAddress    *string
	UserAgent    *string
	CreatedAt    time.Time
}

// AuditFilter narrows and paginates the audit-log viewer.
type AuditFilter struct {
	OrgID  string // empty = all orgs (platform-wide)
	UserID string
	Action string
	Limit  int
	Offset int
}

// ListAuditEvents returns audit events newest-first. RLS on audit_events
// scopes the rows: a platform owner sees every event, an org member sees only
// their org's. The optional filters narrow within what RLS already permits.
func (r *AdminOpsRepo) ListAuditEvents(ctx context.Context, q Querier, f AuditFilter) ([]AuditEventRow, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.Query(ctx, `
		SELECT id, org_id, user_id, action, resource_type, resource_id, details,
		       host(ip_address), user_agent, created_at
		FROM audit_events
		WHERE ($1 = '' OR org_id = $1::uuid)
		  AND ($2 = '' OR user_id = $2::uuid)
		  AND ($3 = '' OR action = $3)
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5
	`, f.OrgID, f.UserID, f.Action, limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("models: list audit events: %w", err)
	}
	defer rows.Close()

	out := make([]AuditEventRow, 0, limit)
	for rows.Next() {
		var (
			e       AuditEventRow
			details []byte
		)
		if err := rows.Scan(&e.ID, &e.OrgID, &e.UserID, &e.Action, &e.ResourceType,
			&e.ResourceID, &details, &e.IPAddress, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("models: scan audit event: %w", err)
		}
		if len(details) > 0 {
			_ = json.Unmarshal(details, &e.Details)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
