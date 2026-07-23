package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PodcastShow is one podcast series belonging to an org (plan.md Task 9's
// "podcast episodes, playlists, RSS, transcripts, and progress"). slug is
// the stable public identifier the RSS feed URL is built from, unique per
// org. course_id optionally ties the show to a course.
type PodcastShow struct {
	ID          string
	OrgID       string
	CourseID    *string
	Slug        string
	Title       string
	Description string
	Author      string
	ImageURL    *string
	Language    string
	Category    string
	IsPublished bool
	CreatedBy   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PublishedShow is the trimmed projection the anonymous RSS feed reads via
// the get_published_podcast_show SECURITY DEFINER function — never the
// authoring/audit columns.
type PublishedShow struct {
	ID          string
	Slug        string
	Title       string
	Description string
	Author      string
	ImageURL    *string
	Language    string
	Category    string
	UpdatedAt   time.Time
}

type PodcastShowRepo struct{}

func NewPodcastShowRepo() *PodcastShowRepo { return &PodcastShowRepo{} }

const podcastShowColumns = `id, org_id, course_id, slug, title, description, author,
	image_url, language, category, is_published, created_by, created_at, updated_at`

func scanPodcastShow(row pgx.Row) (*PodcastShow, error) {
	var s PodcastShow
	if err := row.Scan(&s.ID, &s.OrgID, &s.CourseID, &s.Slug, &s.Title, &s.Description,
		&s.Author, &s.ImageURL, &s.Language, &s.Category, &s.IsPublished, &s.CreatedBy,
		&s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func scanPodcastShowRows(rows pgx.Rows) (*PodcastShow, error) {
	var s PodcastShow
	if err := rows.Scan(&s.ID, &s.OrgID, &s.CourseID, &s.Slug, &s.Title, &s.Description,
		&s.Author, &s.ImageURL, &s.Language, &s.Category, &s.IsPublished, &s.CreatedBy,
		&s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan podcast show: %w", err)
	}
	return &s, nil
}

// Create inserts a new show. courseID/imageURL may be "" to store NULL.
func (r *PodcastShowRepo) Create(ctx context.Context, q Querier, s PodcastShow) (*PodcastShow, error) {
	if s.Language == "" {
		s.Language = "en"
	}
	row := q.QueryRow(ctx, `
		INSERT INTO podcast_shows
			(org_id, course_id, slug, title, description, author, image_url, language, category, is_published, created_by)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, NULLIF($11, '')::uuid)
		RETURNING `+podcastShowColumns,
		s.OrgID, strOrEmpty(s.CourseID), s.Slug, s.Title, s.Description, s.Author,
		strOrEmpty(s.ImageURL), s.Language, s.Category, s.IsPublished, strOrEmpty(s.CreatedBy))
	out, err := scanPodcastShow(row)
	if err != nil {
		return nil, fmt.Errorf("models: create podcast show: %w", err)
	}
	return out, nil
}

func (r *PodcastShowRepo) Get(ctx context.Context, q Querier, id string) (*PodcastShow, error) {
	row := q.QueryRow(ctx, `SELECT `+podcastShowColumns+` FROM podcast_shows WHERE id = $1`, id)
	s, err := scanPodcastShow(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get podcast show: %w", err)
	}
	return s, nil
}

func (r *PodcastShowRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*PodcastShow, error) {
	rows, err := q.Query(ctx, `SELECT `+podcastShowColumns+`
		FROM podcast_shows WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list podcast shows: %w", err)
	}
	defer rows.Close()

	var out []*PodcastShow
	for rows.Next() {
		s, err := scanPodcastShowRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list podcast shows: %w", err)
	}
	return out, nil
}

// Update overwrites the editable fields of a show. is_published is set here
// too — publishing a show is a plain field flip (unlike an episode, whose
// publish stamps published_at, see PodcastEpisodeRepo.SetPublished).
func (r *PodcastShowRepo) Update(ctx context.Context, q Querier, s PodcastShow) (*PodcastShow, error) {
	if s.Language == "" {
		s.Language = "en"
	}
	row := q.QueryRow(ctx, `
		UPDATE podcast_shows
		SET course_id = NULLIF($2, '')::uuid, title = $3, description = $4, author = $5,
		    image_url = NULLIF($6, ''), language = $7, category = $8, is_published = $9, updated_at = now()
		WHERE id = $1
		RETURNING `+podcastShowColumns,
		s.ID, strOrEmpty(s.CourseID), s.Title, s.Description, s.Author,
		strOrEmpty(s.ImageURL), s.Language, s.Category, s.IsPublished)
	out, err := scanPodcastShow(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update podcast show: %w", err)
	}
	return out, nil
}

func (r *PodcastShowRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM podcast_shows WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete podcast show: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetPublishedBySlug reads a published show for the anonymous RSS feed via
// the SECURITY DEFINER function, so it works with no RLS session context
// (the same pattern as CourseRepo.ListPublished / resolve_org_by_domain).
// Returns ErrNotFound when the show doesn't exist or isn't published.
func (r *PodcastShowRepo) GetPublishedBySlug(ctx context.Context, q Querier, orgID, slug string) (*PublishedShow, error) {
	row := q.QueryRow(ctx, `
		SELECT id, slug, title, description, author, image_url, language, category, updated_at
		FROM get_published_podcast_show($1, $2)`, orgID, slug)
	var s PublishedShow
	if err := row.Scan(&s.ID, &s.Slug, &s.Title, &s.Description, &s.Author,
		&s.ImageURL, &s.Language, &s.Category, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get published podcast show: %w", err)
	}
	return &s, nil
}
