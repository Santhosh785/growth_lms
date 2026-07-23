package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DiscussionPost is a message in a thread. A root post has ParentPostID nil;
// a reply points at a root post (one level of nesting, enforced in the
// handler). Status supports soft-delete/hide moderation.
type DiscussionPost struct {
	ID           string
	OrgID        string
	ThreadID     string
	ParentPostID *string
	AuthorID     string
	Body         string
	Status       string
	EditedAt     *time.Time
	CreatedAt    time.Time
}

type DiscussionPostRepo struct{}

func NewDiscussionPostRepo() *DiscussionPostRepo { return &DiscussionPostRepo{} }

const discussionPostColumns = `id, org_id, thread_id, parent_post_id, author_id, body, status, edited_at, created_at`

func (r *DiscussionPostRepo) Create(ctx context.Context, q Querier, orgID, threadID string, parentPostID *string, authorID, body string) (*DiscussionPost, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO discussion_posts (org_id, thread_id, parent_post_id, author_id, body)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+discussionPostColumns, orgID, threadID, parentPostID, authorID, body)
	return scanDiscussionPost(row)
}

func (r *DiscussionPostRepo) Get(ctx context.Context, q Querier, id string) (*DiscussionPost, error) {
	row := q.QueryRow(ctx, `SELECT `+discussionPostColumns+` FROM discussion_posts WHERE id = $1`, id)
	return scanDiscussionPost(row)
}

// ListByThread returns a thread's visible posts oldest-first (reading order).
func (r *DiscussionPostRepo) ListByThread(ctx context.Context, q Querier, threadID string) ([]*DiscussionPost, error) {
	rows, err := q.Query(ctx, `
		SELECT `+discussionPostColumns+` FROM discussion_posts
		WHERE thread_id = $1 AND status = 'visible'
		ORDER BY created_at ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("models: list thread posts: %w", err)
	}
	defer rows.Close()
	var out []*DiscussionPost
	for rows.Next() {
		p, err := scanDiscussionPostRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateBody edits a post's text and stamps edited_at.
func (r *DiscussionPostRepo) UpdateBody(ctx context.Context, q Querier, id, body string) (*DiscussionPost, error) {
	row := q.QueryRow(ctx, `
		UPDATE discussion_posts SET body = $2, edited_at = now()
		WHERE id = $1 RETURNING `+discussionPostColumns, id, body)
	return scanDiscussionPost(row)
}

// UpdateStatus soft-deletes/hides a post ('visible'|'hidden'|'deleted').
func (r *DiscussionPostRepo) UpdateStatus(ctx context.Context, q Querier, id, status string) (*DiscussionPost, error) {
	row := q.QueryRow(ctx, `
		UPDATE discussion_posts SET status = $2
		WHERE id = $1 RETURNING `+discussionPostColumns, id, status)
	return scanDiscussionPost(row)
}

func scanDiscussionPost(row pgx.Row) (*DiscussionPost, error) {
	var p DiscussionPost
	if err := row.Scan(&p.ID, &p.OrgID, &p.ThreadID, &p.ParentPostID, &p.AuthorID, &p.Body, &p.Status, &p.EditedAt, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan post: %w", err)
	}
	return &p, nil
}

func scanDiscussionPostRows(rows pgx.Rows) (*DiscussionPost, error) {
	var p DiscussionPost
	if err := rows.Scan(&p.ID, &p.OrgID, &p.ThreadID, &p.ParentPostID, &p.AuthorID, &p.Body, &p.Status, &p.EditedAt, &p.CreatedAt); err != nil {
		return nil, fmt.Errorf("models: scan post: %w", err)
	}
	return &p, nil
}
