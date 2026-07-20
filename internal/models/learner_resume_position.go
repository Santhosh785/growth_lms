package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LearnerResumePosition is the "continue learning" pointer: one row per
// (learner, course), upserted by the player as the learner navigates
// lessons. There is no ID column — the natural key (learner_id, course_id)
// is the primary key, matching the migration.
type LearnerResumePosition struct {
	OrgID           string
	LearnerID       string
	CourseID        string
	CurrentLessonID string
	LastResumedAt   time.Time
}

type LearnerResumePositionRepo struct{}

func NewLearnerResumePositionRepo() *LearnerResumePositionRepo { return &LearnerResumePositionRepo{} }

const learnerResumePositionColumns = `org_id, learner_id, course_id, current_lesson_id, last_resumed_at`

// Upsert sets the learner's current lesson within a course and bumps
// last_resumed_at to now — called every time the player navigates to a
// new lesson. ON CONFLICT targets the (learner_id, course_id) primary key.
func (r *LearnerResumePositionRepo) Upsert(ctx context.Context, q Querier, orgID, learnerID, courseID, currentLessonID string) (*LearnerResumePosition, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_resume_position (org_id, learner_id, course_id, current_lesson_id, last_resumed_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (learner_id, course_id) DO UPDATE
		SET current_lesson_id = EXCLUDED.current_lesson_id, last_resumed_at = now()
		RETURNING `+learnerResumePositionColumns, orgID, learnerID, courseID, currentLessonID)
	return scanLearnerResumePosition(row)
}

// Get returns the learner's resume pointer for a course, or ErrNotFound if
// they've never opened a lesson in it yet — callers should fall back to
// the course's first lesson in that case.
func (r *LearnerResumePositionRepo) Get(ctx context.Context, q Querier, learnerID, courseID string) (*LearnerResumePosition, error) {
	row := q.QueryRow(ctx, `SELECT `+learnerResumePositionColumns+` FROM learner_resume_position WHERE learner_id = $1 AND course_id = $2`, learnerID, courseID)
	return scanLearnerResumePosition(row)
}

func scanLearnerResumePosition(row pgx.Row) (*LearnerResumePosition, error) {
	var p LearnerResumePosition
	if err := row.Scan(&p.OrgID, &p.LearnerID, &p.CourseID, &p.CurrentLessonID, &p.LastResumedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner resume position: %w", err)
	}
	return &p, nil
}
