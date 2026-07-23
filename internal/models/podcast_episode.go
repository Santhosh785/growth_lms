package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PodcastEpisode is one episode within a show. org_id is denormalized from
// the parent show for flat RLS. audio_url is the enclosure a podcast app
// downloads; duration_seconds/audio_bytes feed the RSS enclosure length and
// <itunes:duration>. published_at is stamped when the episode is published
// (see SetPublished) and drives the RSS <pubDate> and feed ordering.
type PodcastEpisode struct {
	ID            string
	ShowID        string
	OrgID         string
	Title         string
	Description   string
	AudioURL      string
	AudioBytes    int64
	AudioMimeType string
	Duration      int
	Transcript    string
	EpisodeNumber *int
	SeasonNumber  *int
	IsPublished   bool
	PublishedAt   *time.Time
	CreatedBy     *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PublishedEpisode is the trimmed projection the anonymous RSS feed reads
// via list_published_podcast_episodes — never transcript/authoring columns.
type PublishedEpisode struct {
	ID            string
	Title         string
	Description   string
	AudioURL      string
	AudioBytes    int64
	AudioMimeType string
	Duration      int
	EpisodeNumber *int
	SeasonNumber  *int
	PublishedAt   time.Time
}

type PodcastEpisodeRepo struct{}

func NewPodcastEpisodeRepo() *PodcastEpisodeRepo { return &PodcastEpisodeRepo{} }

const podcastEpisodeColumns = `id, show_id, org_id, title, description, audio_url, audio_bytes,
	audio_mime_type, duration_seconds, transcript, episode_number, season_number,
	is_published, published_at, created_by, created_at, updated_at`

func scanPodcastEpisode(row pgx.Row) (*PodcastEpisode, error) {
	var e PodcastEpisode
	if err := row.Scan(&e.ID, &e.ShowID, &e.OrgID, &e.Title, &e.Description, &e.AudioURL,
		&e.AudioBytes, &e.AudioMimeType, &e.Duration, &e.Transcript, &e.EpisodeNumber,
		&e.SeasonNumber, &e.IsPublished, &e.PublishedAt, &e.CreatedBy, &e.CreatedAt,
		&e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func scanPodcastEpisodeRows(rows pgx.Rows) (*PodcastEpisode, error) {
	var e PodcastEpisode
	if err := rows.Scan(&e.ID, &e.ShowID, &e.OrgID, &e.Title, &e.Description, &e.AudioURL,
		&e.AudioBytes, &e.AudioMimeType, &e.Duration, &e.Transcript, &e.EpisodeNumber,
		&e.SeasonNumber, &e.IsPublished, &e.PublishedAt, &e.CreatedBy, &e.CreatedAt,
		&e.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan podcast episode: %w", err)
	}
	return &e, nil
}

// Create inserts a new episode (unpublished by default; publish via
// SetPublished so published_at is stamped consistently).
func (r *PodcastEpisodeRepo) Create(ctx context.Context, q Querier, e PodcastEpisode) (*PodcastEpisode, error) {
	if e.AudioMimeType == "" {
		e.AudioMimeType = "audio/mpeg"
	}
	row := q.QueryRow(ctx, `
		INSERT INTO podcast_episodes
			(show_id, org_id, title, description, audio_url, audio_bytes, audio_mime_type,
			 duration_seconds, transcript, episode_number, season_number, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, '')::uuid)
		RETURNING `+podcastEpisodeColumns,
		e.ShowID, e.OrgID, e.Title, e.Description, e.AudioURL, e.AudioBytes, e.AudioMimeType,
		e.Duration, e.Transcript, e.EpisodeNumber, e.SeasonNumber, strOrEmpty(e.CreatedBy))
	out, err := scanPodcastEpisode(row)
	if err != nil {
		return nil, fmt.Errorf("models: create podcast episode: %w", err)
	}
	return out, nil
}

func (r *PodcastEpisodeRepo) Get(ctx context.Context, q Querier, id string) (*PodcastEpisode, error) {
	row := q.QueryRow(ctx, `SELECT `+podcastEpisodeColumns+` FROM podcast_episodes WHERE id = $1`, id)
	e, err := scanPodcastEpisode(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get podcast episode: %w", err)
	}
	return e, nil
}

// ListByShow returns every episode of a show (published or not) for the
// in-app authoring/catalog view, newest-created first.
func (r *PodcastEpisodeRepo) ListByShow(ctx context.Context, q Querier, showID string) ([]*PodcastEpisode, error) {
	rows, err := q.Query(ctx, `SELECT `+podcastEpisodeColumns+`
		FROM podcast_episodes WHERE show_id = $1 ORDER BY created_at DESC`, showID)
	if err != nil {
		return nil, fmt.Errorf("models: list podcast episodes: %w", err)
	}
	defer rows.Close()

	var out []*PodcastEpisode
	for rows.Next() {
		e, err := scanPodcastEpisodeRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list podcast episodes: %w", err)
	}
	return out, nil
}

// Update overwrites the editable content fields of an episode. Publish
// state is managed separately via SetPublished.
func (r *PodcastEpisodeRepo) Update(ctx context.Context, q Querier, e PodcastEpisode) (*PodcastEpisode, error) {
	if e.AudioMimeType == "" {
		e.AudioMimeType = "audio/mpeg"
	}
	row := q.QueryRow(ctx, `
		UPDATE podcast_episodes
		SET title = $2, description = $3, audio_url = $4, audio_bytes = $5, audio_mime_type = $6,
		    duration_seconds = $7, transcript = $8, episode_number = $9, season_number = $10, updated_at = now()
		WHERE id = $1
		RETURNING `+podcastEpisodeColumns,
		e.ID, e.Title, e.Description, e.AudioURL, e.AudioBytes, e.AudioMimeType,
		e.Duration, e.Transcript, e.EpisodeNumber, e.SeasonNumber)
	out, err := scanPodcastEpisode(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update podcast episode: %w", err)
	}
	return out, nil
}

// SetPublished toggles an episode's publish state. Publishing stamps
// published_at (only if not already set, so re-publishing keeps the
// original air date); unpublishing leaves published_at intact so a
// re-publish keeps the original date rather than resetting it.
func (r *PodcastEpisodeRepo) SetPublished(ctx context.Context, q Querier, id string, published bool) (*PodcastEpisode, error) {
	row := q.QueryRow(ctx, `
		UPDATE podcast_episodes
		SET is_published = $2,
		    published_at = CASE WHEN $2 AND published_at IS NULL THEN now() ELSE published_at END,
		    updated_at = now()
		WHERE id = $1
		RETURNING `+podcastEpisodeColumns, id, published)
	out, err := scanPodcastEpisode(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: set podcast episode published: %w", err)
	}
	return out, nil
}

func (r *PodcastEpisodeRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM podcast_episodes WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete podcast episode: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPublishedByShow reads the published episodes of a published show for
// the anonymous RSS feed via the SECURITY DEFINER function — no RLS session
// needed, published rows only.
func (r *PodcastEpisodeRepo) ListPublishedByShow(ctx context.Context, q Querier, showID string) ([]*PublishedEpisode, error) {
	rows, err := q.Query(ctx, `
		SELECT id, title, description, audio_url, audio_bytes, audio_mime_type,
		       duration_seconds, episode_number, season_number, published_at
		FROM list_published_podcast_episodes($1)`, showID)
	if err != nil {
		return nil, fmt.Errorf("models: list published podcast episodes: %w", err)
	}
	defer rows.Close()

	var out []*PublishedEpisode
	for rows.Next() {
		var e PublishedEpisode
		if err := rows.Scan(&e.ID, &e.Title, &e.Description, &e.AudioURL, &e.AudioBytes,
			&e.AudioMimeType, &e.Duration, &e.EpisodeNumber, &e.SeasonNumber, &e.PublishedAt); err != nil {
			return nil, fmt.Errorf("models: scan published podcast episode: %w", err)
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list published podcast episodes: %w", err)
	}
	return out, nil
}
