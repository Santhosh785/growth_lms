package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PodcastProgress is one learner's listen position for one episode
// (plan.md Task 9's "progress"). Learner-owned via flat RLS on learner_id,
// the same shape as AITutorSession. org_id is denormalized for flat RLS.
type PodcastProgress struct {
	ID              string
	OrgID           string
	EpisodeID       string
	LearnerID       string
	PositionSeconds int
	DurationSeconds int
	Completed       bool
	UpdatedAt       time.Time
}

type PodcastProgressRepo struct{}

func NewPodcastProgressRepo() *PodcastProgressRepo { return &PodcastProgressRepo{} }

const podcastProgressColumns = `id, org_id, episode_id, learner_id, position_seconds, duration_seconds, completed, updated_at`

func scanPodcastProgress(row pgx.Row) (*PodcastProgress, error) {
	var p PodcastProgress
	if err := row.Scan(&p.ID, &p.OrgID, &p.EpisodeID, &p.LearnerID, &p.PositionSeconds,
		&p.DurationSeconds, &p.Completed, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// Upsert records a learner's listen position for an episode. completed is
// sticky: once true it stays true even if a later ping reports an earlier
// position (a learner scrubbing back through an already-finished episode
// hasn't "un-finished" it), mirroring MarkComplete's COALESCE idempotence
// on learner_lesson_progress.
func (r *PodcastProgressRepo) Upsert(ctx context.Context, q Querier, orgID, episodeID, learnerID string, positionSeconds, durationSeconds int, completed bool) (*PodcastProgress, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO podcast_progress (org_id, episode_id, learner_id, position_seconds, duration_seconds, completed)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (episode_id, learner_id) DO UPDATE
		SET position_seconds = EXCLUDED.position_seconds,
		    duration_seconds = EXCLUDED.duration_seconds,
		    completed = podcast_progress.completed OR EXCLUDED.completed,
		    updated_at = now()
		RETURNING `+podcastProgressColumns,
		orgID, episodeID, learnerID, positionSeconds, durationSeconds, completed)
	p, err := scanPodcastProgress(row)
	if err != nil {
		return nil, fmt.Errorf("models: upsert podcast progress: %w", err)
	}
	return p, nil
}

func (r *PodcastProgressRepo) Get(ctx context.Context, q Querier, learnerID, episodeID string) (*PodcastProgress, error) {
	row := q.QueryRow(ctx, `SELECT `+podcastProgressColumns+`
		FROM podcast_progress WHERE learner_id = $1 AND episode_id = $2`, learnerID, episodeID)
	p, err := scanPodcastProgress(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get podcast progress: %w", err)
	}
	return p, nil
}

// ListForLearnerByShow returns a learner's progress across every episode of
// a show, for rendering per-episode listen state in the show view.
func (r *PodcastProgressRepo) ListForLearnerByShow(ctx context.Context, q Querier, learnerID, showID string) ([]*PodcastProgress, error) {
	rows, err := q.Query(ctx, `
		SELECT `+prefixPodcastProgressColumns+`
		FROM podcast_progress p
		JOIN podcast_episodes e ON e.id = p.episode_id
		WHERE p.learner_id = $1 AND e.show_id = $2
		ORDER BY p.updated_at DESC`, learnerID, showID)
	if err != nil {
		return nil, fmt.Errorf("models: list podcast progress: %w", err)
	}
	defer rows.Close()

	var out []*PodcastProgress
	for rows.Next() {
		var p PodcastProgress
		if err := rows.Scan(&p.ID, &p.OrgID, &p.EpisodeID, &p.LearnerID, &p.PositionSeconds,
			&p.DurationSeconds, &p.Completed, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("models: scan podcast progress: %w", err)
		}
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list podcast progress: %w", err)
	}
	return out, nil
}

const prefixPodcastProgressColumns = `p.id, p.org_id, p.episode_id, p.learner_id, p.position_seconds, p.duration_seconds, p.completed, p.updated_at`
