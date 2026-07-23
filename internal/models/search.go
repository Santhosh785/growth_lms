package models

import (
	"context"
	"fmt"
)

// SearchResult is one hit from a cross-entity search (plan.md Task 8:
// "search across courses, lessons, users, and discussions"). Kind
// distinguishes which of the four entity types this row came from so the
// handler can render/link each result appropriately.
type SearchResult struct {
	Kind    string // "course", "lesson", "discussion", "user"
	ID      string
	Title   string
	Snippet string
}

type SearchRepo struct{}

func NewSearchRepo() *SearchRepo { return &SearchRepo{} }

// Courses runs a websearch_to_tsquery match against courses.search_vector,
// scoped to the org by RLS (courses_select already restricts to
// is_org_member).
func (r *SearchRepo) Courses(ctx context.Context, q Querier, orgID, query string) ([]SearchResult, error) {
	rows, err := q.Query(ctx, `
		SELECT id, title, description
		FROM courses
		WHERE org_id = $1 AND search_vector @@ websearch_to_tsquery('english', $2)
		ORDER BY ts_rank(search_vector, websearch_to_tsquery('english', $2)) DESC
		LIMIT 20
	`, orgID, query)
	if err != nil {
		return nil, fmt.Errorf("models: search courses: %w", err)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var id, title, desc string
		if err := rows.Scan(&id, &title, &desc); err != nil {
			return nil, fmt.Errorf("models: scan course search result: %w", err)
		}
		out = append(out, SearchResult{Kind: "course", ID: id, Title: title, Snippet: desc})
	}
	return out, rows.Err()
}

func (r *SearchRepo) Lessons(ctx context.Context, q Querier, orgID, query string) ([]SearchResult, error) {
	rows, err := q.Query(ctx, `
		SELECT l.id, l.title, c.id
		FROM lessons l
		JOIN chapters ch ON ch.id = l.chapter_id
		JOIN courses c ON c.id = ch.course_id
		WHERE c.org_id = $1 AND l.search_vector @@ websearch_to_tsquery('english', $2)
		ORDER BY ts_rank(l.search_vector, websearch_to_tsquery('english', $2)) DESC
		LIMIT 20
	`, orgID, query)
	if err != nil {
		return nil, fmt.Errorf("models: search lessons: %w", err)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var id, title, courseID string
		if err := rows.Scan(&id, &title, &courseID); err != nil {
			return nil, fmt.Errorf("models: scan lesson search result: %w", err)
		}
		out = append(out, SearchResult{Kind: "lesson", ID: id, Title: title, Snippet: courseID})
	}
	return out, rows.Err()
}

func (r *SearchRepo) Discussions(ctx context.Context, q Querier, orgID, query string) ([]SearchResult, error) {
	rows, err := q.Query(ctx, `
		SELECT id, title
		FROM discussion_threads
		WHERE org_id = $1 AND search_vector @@ websearch_to_tsquery('english', $2)
		ORDER BY ts_rank(search_vector, websearch_to_tsquery('english', $2)) DESC
		LIMIT 20
	`, orgID, query)
	if err != nil {
		return nil, fmt.Errorf("models: search discussions: %w", err)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, fmt.Errorf("models: scan discussion search result: %w", err)
		}
		out = append(out, SearchResult{Kind: "discussion", ID: id, Title: title})
	}
	return out, rows.Err()
}

// Members calls the search_org_members() SECURITY DEFINER SQL function
// (migration 000009) — see that migration for why a plain SELECT against
// profiles can't do this under RLS.
func (r *SearchRepo) Members(ctx context.Context, q Querier, orgID, query string) ([]SearchResult, error) {
	rows, err := q.Query(ctx, `SELECT user_id, full_name, email FROM search_org_members($1, $2)`, orgID, query)
	if err != nil {
		return nil, fmt.Errorf("models: search members: %w", err)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var id string
		var fullName, email *string
		if err := rows.Scan(&id, &fullName, &email); err != nil {
			return nil, fmt.Errorf("models: scan member search result: %w", err)
		}
		title := ""
		if fullName != nil {
			title = *fullName
		}
		snippet := ""
		if email != nil {
			snippet = *email
		}
		out = append(out, SearchResult{Kind: "user", ID: id, Title: title, Snippet: snippet})
	}
	return out, rows.Err()
}
