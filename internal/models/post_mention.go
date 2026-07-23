package models

import (
	"context"
	"fmt"
	"regexp"
)

// PostMention records that a post @-mentions an org member. Populated by the
// discussions handler after parsing @[uuid] tokens out of the post body.
type PostMention struct {
	PostID          string
	OrgID           string
	MentionedUserID string
}

type PostMentionRepo struct{}

func NewPostMentionRepo() *PostMentionRepo { return &PostMentionRepo{} }

// mentionTokenRe matches the hidden member tokens the compose UI inserts:
// @[<uuid>]. Plain "@name" text is intentionally NOT a mention — resolution
// is unambiguous only through the picker-inserted uuid token.
var mentionTokenRe = regexp.MustCompile(`@\[([0-9a-fA-F-]{36})\]`)

// ParseMentionTokens extracts the unique user IDs referenced by @[uuid]
// tokens in a post body, preserving first-seen order.
func ParseMentionTokens(body string) []string {
	matches := mentionTokenRe.FindAllStringSubmatch(body, -1)
	seen := make(map[string]struct{}, len(matches))
	var out []string
	for _, m := range matches {
		id := m[1]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// StripMentionTokens removes @[uuid] tokens from a body, for rendering a
// plain-text preview (email/notification snippet) that doesn't leak raw uuids.
func StripMentionTokens(body string) string {
	return mentionTokenRe.ReplaceAllString(body, "")
}

// AddMany inserts mention rows for a post, ignoring duplicates. Non-members
// are silently skipped by the WITH CHECK (is_org_member) policy only if the
// caller filters first; callers should pass user IDs already validated as
// org members.
func (r *PostMentionRepo) AddMany(ctx context.Context, q Querier, postID, orgID string, userIDs []string) error {
	for _, uid := range userIDs {
		_, err := q.Exec(ctx, `
			INSERT INTO post_mentions (post_id, org_id, mentioned_user_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (post_id, mentioned_user_id) DO NOTHING`, postID, orgID, uid)
		if err != nil {
			return fmt.Errorf("models: add mention: %w", err)
		}
	}
	return nil
}

// ListByPost returns the user IDs mentioned in a post.
func (r *PostMentionRepo) ListByPost(ctx context.Context, q Querier, postID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT mentioned_user_id FROM post_mentions WHERE post_id = $1`, postID)
	if err != nil {
		return nil, fmt.Errorf("models: list mentions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("models: scan mention: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
