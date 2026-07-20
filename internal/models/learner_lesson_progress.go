package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LearnerLessonProgress is one row per (learner, lesson); course_id is
// denormalized down from the parent lesson for RLS/query convenience,
// matching Task 4's chapters/lessons denormalization precedent.
type LearnerLessonProgress struct {
	OrgID             string
	LearnerID         string
	LessonID          string
	CourseID          string
	CompletedAt       *time.Time
	WatchedDurationMs int64
	WatchPercentage   float64
}

type LearnerLessonProgressRepo struct{}

func NewLearnerLessonProgressRepo() *LearnerLessonProgressRepo { return &LearnerLessonProgressRepo{} }

const learnerLessonProgressColumns = `org_id, learner_id, lesson_id, course_id, completed_at, watched_duration_ms, watch_percentage`

// UpsertWatchProgress records a watch-progress ping (video lessons):
// updates watched_duration_ms/watch_percentage but never touches
// completed_at — completion is decided separately (by the handler
// comparing watch_percentage against the lesson's watch threshold) via
// MarkComplete, so a progress ping alone can never mark a lesson done.
func (r *LearnerLessonProgressRepo) UpsertWatchProgress(ctx context.Context, q Querier, orgID, learnerID, lessonID, courseID string, watchedDurationMs int64, watchPercentage float64) (*LearnerLessonProgress, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_lesson_progress (org_id, learner_id, lesson_id, course_id, watched_duration_ms, watch_percentage)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (learner_id, lesson_id) DO UPDATE
		SET watched_duration_ms = EXCLUDED.watched_duration_ms, watch_percentage = EXCLUDED.watch_percentage
		RETURNING `+learnerLessonProgressColumns, orgID, learnerID, lessonID, courseID, watchedDurationMs, watchPercentage)
	return scanLearnerLessonProgress(row)
}

// MarkComplete sets completed_at = now() (idempotent — re-marking an
// already-complete lesson doesn't reset the original timestamp) for
// non-video completion events (text viewed, quiz passed, assignment
// submitted) as well as video lessons that crossed their watch threshold.
// It upserts because a learner may complete a lesson (e.g. a text block)
// before any progress row exists for it.
func (r *LearnerLessonProgressRepo) MarkComplete(ctx context.Context, q Querier, orgID, learnerID, lessonID, courseID string) (*LearnerLessonProgress, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_lesson_progress (org_id, learner_id, lesson_id, course_id, completed_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (learner_id, lesson_id) DO UPDATE
		SET completed_at = COALESCE(learner_lesson_progress.completed_at, EXCLUDED.completed_at)
		RETURNING `+learnerLessonProgressColumns, orgID, learnerID, lessonID, courseID)
	return scanLearnerLessonProgress(row)
}

func (r *LearnerLessonProgressRepo) Get(ctx context.Context, q Querier, learnerID, lessonID string) (*LearnerLessonProgress, error) {
	row := q.QueryRow(ctx, `SELECT `+learnerLessonProgressColumns+` FROM learner_lesson_progress WHERE learner_id = $1 AND lesson_id = $2`, learnerID, lessonID)
	return scanLearnerLessonProgress(row)
}

// ListByCourse returns every progress row a learner has for a course,
// used to compute the course-progress percentage (completed lessons /
// total lessons) and to render per-lesson completion state in the player.
func (r *LearnerLessonProgressRepo) ListByCourse(ctx context.Context, q Querier, learnerID, courseID string) ([]*LearnerLessonProgress, error) {
	rows, err := q.Query(ctx, `SELECT `+learnerLessonProgressColumns+` FROM learner_lesson_progress WHERE learner_id = $1 AND course_id = $2`, learnerID, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list learner lesson progress: %w", err)
	}
	defer rows.Close()

	var out []*LearnerLessonProgress
	for rows.Next() {
		p, err := scanLearnerLessonProgressRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanLearnerLessonProgress(row pgx.Row) (*LearnerLessonProgress, error) {
	var p LearnerLessonProgress
	if err := row.Scan(&p.OrgID, &p.LearnerID, &p.LessonID, &p.CourseID, &p.CompletedAt, &p.WatchedDurationMs, &p.WatchPercentage); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner lesson progress: %w", err)
	}
	return &p, nil
}

func scanLearnerLessonProgressRows(rows pgx.Rows) (*LearnerLessonProgress, error) {
	var p LearnerLessonProgress
	if err := rows.Scan(&p.OrgID, &p.LearnerID, &p.LessonID, &p.CourseID, &p.CompletedAt, &p.WatchedDurationMs, &p.WatchPercentage); err != nil {
		return nil, fmt.Errorf("models: scan learner lesson progress: %w", err)
	}
	return &p, nil
}
