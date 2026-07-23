package models

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PostReaction is a single (post, user, emoji) reaction. The primary key
// makes a repeated identical reaction a no-op via ON CONFLICT.
type PostReaction struct {
	PostID    string
	OrgID     string
	UserID    string
	Emoji     string
	CreatedAt time.Time
}

type PostReactionRepo struct{}

func NewPostReactionRepo() *PostReactionRepo { return &PostReactionRepo{} }

// Add records a reaction, ignoring a duplicate from the same user.
func (r *PostReactionRepo) Add(ctx context.Context, q Querier, postID, orgID, userID, emoji string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO post_reactions (post_id, org_id, user_id, emoji)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (post_id, user_id, emoji) DO NOTHING`, postID, orgID, userID, emoji)
	if err != nil {
		return fmt.Errorf("models: add reaction: %w", err)
	}
	return nil
}

// Remove deletes the caller's own reaction (RLS restricts the row to
// user_id = current user).
func (r *PostReactionRepo) Remove(ctx context.Context, q Querier, postID, userID, emoji string) error {
	_, err := q.Exec(ctx, `
		DELETE FROM post_reactions WHERE post_id = $1 AND user_id = $2 AND emoji = $3`, postID, userID, emoji)
	if err != nil {
		return fmt.Errorf("models: remove reaction: %w", err)
	}
	return nil
}

// ListByThread returns every reaction on the thread's posts, so the handler
// can aggregate emoji counts per post in one query.
func (r *PostReactionRepo) ListByThread(ctx context.Context, q Querier, threadID string) ([]*PostReaction, error) {
	rows, err := q.Query(ctx, `
		SELECT pr.post_id, pr.org_id, pr.user_id, pr.emoji, pr.created_at
		FROM post_reactions pr
		JOIN discussion_posts dp ON dp.id = pr.post_id
		WHERE dp.thread_id = $1`, threadID)
	if err != nil {
		return nil, fmt.Errorf("models: list thread reactions: %w", err)
	}
	defer rows.Close()
	var out []*PostReaction
	for rows.Next() {
		pr, err := scanPostReaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func scanPostReaction(rows pgx.Rows) (*PostReaction, error) {
	var pr PostReaction
	if err := rows.Scan(&pr.PostID, &pr.OrgID, &pr.UserID, &pr.Emoji, &pr.CreatedAt); err != nil {
		return nil, fmt.Errorf("models: scan reaction: %w", err)
	}
	return &pr, nil
}
