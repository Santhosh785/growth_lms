package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PodcastPlaylist is a curated, ordered collection of episodes within an
// org (plan.md Task 9's "playlists"). Membership + ordering live in
// podcast_playlist_items.
type PodcastPlaylist struct {
	ID          string
	OrgID       string
	Title       string
	Description string
	IsPublished bool
	CreatedBy   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PodcastPlaylistItem is one episode's membership in a playlist with an
// explicit sort order.
type PodcastPlaylistItem struct {
	ID         string
	PlaylistID string
	EpisodeID  string
	OrgID      string
	SortOrder  int
}

type PodcastPlaylistRepo struct{}

func NewPodcastPlaylistRepo() *PodcastPlaylistRepo { return &PodcastPlaylistRepo{} }

const podcastPlaylistColumns = `id, org_id, title, description, is_published, created_by, created_at, updated_at`

func scanPodcastPlaylist(row pgx.Row) (*PodcastPlaylist, error) {
	var p PodcastPlaylist
	if err := row.Scan(&p.ID, &p.OrgID, &p.Title, &p.Description, &p.IsPublished,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *PodcastPlaylistRepo) Create(ctx context.Context, q Querier, orgID, title, description string, isPublished bool, createdBy string) (*PodcastPlaylist, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO podcast_playlists (org_id, title, description, is_published, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid)
		RETURNING `+podcastPlaylistColumns, orgID, title, description, isPublished, createdBy)
	p, err := scanPodcastPlaylist(row)
	if err != nil {
		return nil, fmt.Errorf("models: create podcast playlist: %w", err)
	}
	return p, nil
}

func (r *PodcastPlaylistRepo) Get(ctx context.Context, q Querier, id string) (*PodcastPlaylist, error) {
	row := q.QueryRow(ctx, `SELECT `+podcastPlaylistColumns+` FROM podcast_playlists WHERE id = $1`, id)
	p, err := scanPodcastPlaylist(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get podcast playlist: %w", err)
	}
	return p, nil
}

func (r *PodcastPlaylistRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*PodcastPlaylist, error) {
	rows, err := q.Query(ctx, `SELECT `+podcastPlaylistColumns+`
		FROM podcast_playlists WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list podcast playlists: %w", err)
	}
	defer rows.Close()

	var out []*PodcastPlaylist
	for rows.Next() {
		var p PodcastPlaylist
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Title, &p.Description, &p.IsPublished,
			&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("models: scan podcast playlist: %w", err)
		}
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list podcast playlists: %w", err)
	}
	return out, nil
}

func (r *PodcastPlaylistRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM podcast_playlists WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete podcast playlist: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddItem appends an episode to a playlist. sortOrder positions it; a
// re-add of the same episode updates its position (idempotent on the
// (playlist, episode) unique key) rather than erroring.
func (r *PodcastPlaylistRepo) AddItem(ctx context.Context, q Querier, playlistID, episodeID, orgID string, sortOrder int) (*PodcastPlaylistItem, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO podcast_playlist_items (playlist_id, episode_id, org_id, sort_order)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (playlist_id, episode_id) DO UPDATE SET sort_order = EXCLUDED.sort_order
		RETURNING id, playlist_id, episode_id, org_id, sort_order`,
		playlistID, episodeID, orgID, sortOrder)
	var it PodcastPlaylistItem
	if err := row.Scan(&it.ID, &it.PlaylistID, &it.EpisodeID, &it.OrgID, &it.SortOrder); err != nil {
		return nil, fmt.Errorf("models: add podcast playlist item: %w", err)
	}
	return &it, nil
}

func (r *PodcastPlaylistRepo) RemoveItem(ctx context.Context, q Querier, playlistID, episodeID string) error {
	tag, err := q.Exec(ctx, `DELETE FROM podcast_playlist_items WHERE playlist_id = $1 AND episode_id = $2`, playlistID, episodeID)
	if err != nil {
		return fmt.Errorf("models: remove podcast playlist item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListItems returns a playlist's episodes joined to their episode rows,
// ordered by sort_order. Used by the in-app playlist view.
func (r *PodcastPlaylistRepo) ListItems(ctx context.Context, q Querier, playlistID string) ([]*PodcastEpisode, error) {
	rows, err := q.Query(ctx, `
		SELECT e.id, e.show_id, e.org_id, e.title, e.description, e.audio_url, e.audio_bytes,
		       e.audio_mime_type, e.duration_seconds, e.transcript, e.episode_number, e.season_number,
		       e.is_published, e.published_at, e.created_by, e.created_at, e.updated_at
		FROM podcast_playlist_items i
		JOIN podcast_episodes e ON e.id = i.episode_id
		WHERE i.playlist_id = $1
		ORDER BY i.sort_order ASC`, playlistID)
	if err != nil {
		return nil, fmt.Errorf("models: list podcast playlist items: %w", err)
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
		return nil, fmt.Errorf("models: list podcast playlist items: %w", err)
	}
	return out, nil
}
