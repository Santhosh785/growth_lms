package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ContentReport is a member's report of a post. Only moderators see the full
// open queue and resolve/dismiss (enforced by the content_reports policies).
type ContentReport struct {
	ID         string
	OrgID      string
	PostID     string
	ReporterID string
	Reason     string
	Status     string
	ResolvedBy *string
	ResolvedAt *time.Time
	CreatedAt  time.Time
}

type ContentReportRepo struct{}

func NewContentReportRepo() *ContentReportRepo { return &ContentReportRepo{} }

const contentReportColumns = `id, org_id, post_id, reporter_id, reason, status, resolved_by, resolved_at, created_at`

func (r *ContentReportRepo) Create(ctx context.Context, q Querier, orgID, postID, reporterID, reason string) (*ContentReport, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO content_reports (org_id, post_id, reporter_id, reason)
		VALUES ($1, $2, $3, $4)
		RETURNING `+contentReportColumns, orgID, postID, reporterID, reason)
	return scanContentReport(row)
}

// ListOpenByOrg returns the org's open report queue, newest first. Only
// moderators/owners can see more than their own rows (RLS).
func (r *ContentReportRepo) ListOpenByOrg(ctx context.Context, q Querier, orgID string) ([]*ContentReport, error) {
	rows, err := q.Query(ctx, `
		SELECT `+contentReportColumns+` FROM content_reports
		WHERE org_id = $1 AND status = 'open'
		ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list open reports: %w", err)
	}
	defer rows.Close()
	var out []*ContentReport
	for rows.Next() {
		rep, err := scanContentReportRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

// Resolve marks a report resolved (the reported content was actioned).
func (r *ContentReportRepo) Resolve(ctx context.Context, q Querier, id, resolvedBy string) (*ContentReport, error) {
	return r.setStatus(ctx, q, id, "resolved", resolvedBy)
}

// Dismiss marks a report dismissed (no action needed).
func (r *ContentReportRepo) Dismiss(ctx context.Context, q Querier, id, resolvedBy string) (*ContentReport, error) {
	return r.setStatus(ctx, q, id, "dismissed", resolvedBy)
}

func (r *ContentReportRepo) setStatus(ctx context.Context, q Querier, id, status, resolvedBy string) (*ContentReport, error) {
	row := q.QueryRow(ctx, `
		UPDATE content_reports SET status = $2, resolved_by = $3, resolved_at = now()
		WHERE id = $1 RETURNING `+contentReportColumns, id, status, resolvedBy)
	return scanContentReport(row)
}

func scanContentReport(row pgx.Row) (*ContentReport, error) {
	var rep ContentReport
	if err := row.Scan(&rep.ID, &rep.OrgID, &rep.PostID, &rep.ReporterID, &rep.Reason, &rep.Status, &rep.ResolvedBy, &rep.ResolvedAt, &rep.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan report: %w", err)
	}
	return &rep, nil
}

func scanContentReportRows(rows pgx.Rows) (*ContentReport, error) {
	var rep ContentReport
	if err := rows.Scan(&rep.ID, &rep.OrgID, &rep.PostID, &rep.ReporterID, &rep.Reason, &rep.Status, &rep.ResolvedBy, &rep.ResolvedAt, &rep.CreatedAt); err != nil {
		return nil, fmt.Errorf("models: scan report: %w", err)
	}
	return &rep, nil
}
