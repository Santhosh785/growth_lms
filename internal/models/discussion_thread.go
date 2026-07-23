package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DiscussionThread is a Task 7 community thread. course_id nil = org-wide
// forum thread; course_id set = course discussion. Pin/lock/hide are
// moderator actions (enforced by the is_org_moderator branch of the
// discussion_threads UPDATE policy in migration 000008).
type DiscussionThread struct {
	ID        string
	OrgID     string
	CourseID  *string
	Title     string
	CreatedBy string
	IsPinned  bool
	IsLocked  bool
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type DiscussionThreadRepo struct{}

func NewDiscussionThreadRepo() *DiscussionThreadRepo { return &DiscussionThreadRepo{} }

const discussionThreadColumns = `id, org_id, course_id, title, created_by, is_pinned, is_locked, status, created_at, updated_at`

func (r *DiscussionThreadRepo) Create(ctx context.Context, q Querier, orgID string, courseID *string, title, createdBy string) (*DiscussionThread, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO discussion_threads (org_id, course_id, title, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING `+discussionThreadColumns, orgID, courseID, title, createdBy)
	return scanDiscussionThread(row)
}

func (r *DiscussionThreadRepo) Get(ctx context.Context, q Querier, id string) (*DiscussionThread, error) {
	row := q.QueryRow(ctx, `SELECT `+discussionThreadColumns+` FROM discussion_threads WHERE id = $1`, id)
	return scanDiscussionThread(row)
}

// ListByOrg returns the org-wide (course_id IS NULL) threads, pinned first
// then newest activity. Hidden/deleted threads are excluded.
func (r *DiscussionThreadRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*DiscussionThread, error) {
	rows, err := q.Query(ctx, `
		SELECT `+discussionThreadColumns+` FROM discussion_threads
		WHERE org_id = $1 AND course_id IS NULL AND status = 'open'
		ORDER BY is_pinned DESC, updated_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list org threads: %w", err)
	}
	return collectDiscussionThreads(rows)
}

// ListByCourse returns a course's discussion threads, pinned first.
func (r *DiscussionThreadRepo) ListByCourse(ctx context.Context, q Querier, courseID string) ([]*DiscussionThread, error) {
	rows, err := q.Query(ctx, `
		SELECT `+discussionThreadColumns+` FROM discussion_threads
		WHERE course_id = $1 AND status = 'open'
		ORDER BY is_pinned DESC, updated_at DESC`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list course threads: %w", err)
	}
	return collectDiscussionThreads(rows)
}

func (r *DiscussionThreadRepo) SetPinned(ctx context.Context, q Querier, id string, pinned bool) (*DiscussionThread, error) {
	row := q.QueryRow(ctx, `
		UPDATE discussion_threads SET is_pinned = $2, updated_at = now()
		WHERE id = $1 RETURNING `+discussionThreadColumns, id, pinned)
	return scanDiscussionThread(row)
}

func (r *DiscussionThreadRepo) SetLocked(ctx context.Context, q Querier, id string, locked bool) (*DiscussionThread, error) {
	row := q.QueryRow(ctx, `
		UPDATE discussion_threads SET is_locked = $2, updated_at = now()
		WHERE id = $1 RETURNING `+discussionThreadColumns, id, locked)
	return scanDiscussionThread(row)
}

// UpdateStatus sets a thread's moderation status ('open'|'hidden'|'deleted').
func (r *DiscussionThreadRepo) UpdateStatus(ctx context.Context, q Querier, id, status string) (*DiscussionThread, error) {
	row := q.QueryRow(ctx, `
		UPDATE discussion_threads SET status = $2, updated_at = now()
		WHERE id = $1 RETURNING `+discussionThreadColumns, id, status)
	return scanDiscussionThread(row)
}

// Touch bumps updated_at so a thread with a fresh reply floats to the top of
// the activity-ordered list.
func (r *DiscussionThreadRepo) Touch(ctx context.Context, q Querier, id string) error {
	_, err := q.Exec(ctx, `UPDATE discussion_threads SET updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: touch thread: %w", err)
	}
	return nil
}

func (r *DiscussionThreadRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM discussion_threads WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete thread: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func collectDiscussionThreads(rows pgx.Rows) ([]*DiscussionThread, error) {
	defer rows.Close()
	var out []*DiscussionThread
	for rows.Next() {
		t, err := scanDiscussionThreadRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanDiscussionThread(row pgx.Row) (*DiscussionThread, error) {
	var t DiscussionThread
	if err := row.Scan(&t.ID, &t.OrgID, &t.CourseID, &t.Title, &t.CreatedBy, &t.IsPinned, &t.IsLocked, &t.Status, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan thread: %w", err)
	}
	return &t, nil
}

func scanDiscussionThreadRows(rows pgx.Rows) (*DiscussionThread, error) {
	var t DiscussionThread
	if err := rows.Scan(&t.ID, &t.OrgID, &t.CourseID, &t.Title, &t.CreatedBy, &t.IsPinned, &t.IsLocked, &t.Status, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan thread: %w", err)
	}
	return &t, nil
}
