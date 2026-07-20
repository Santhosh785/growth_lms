package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LearnerQuizScore is the auto-scored result of a quiz attempt (equal
// weight, no partial credit — see grilling-record.md Q2), created
// immediately on attempt submission.
type LearnerQuizScore struct {
	ID            string
	OrgID         string
	LearnerID     string
	QuizBlockID   string
	AttemptNumber int
	ScoreEarned   int
	ScoreMax      int
	Percentage    float64
	Passed        bool
	GradedAt      time.Time
}

type LearnerQuizScoreRepo struct{}

func NewLearnerQuizScoreRepo() *LearnerQuizScoreRepo { return &LearnerQuizScoreRepo{} }

const learnerQuizScoreColumns = `id, org_id, learner_id, quiz_block_id, attempt_number, score_earned, score_max, percentage, passed, graded_at`

// Create records the auto-scored result for an attempt. attemptNumber
// must match the sibling LearnerQuizAttempt row (same UNIQUE key shape)
// so the two tables stay in lockstep per attempt.
func (r *LearnerQuizScoreRepo) Create(ctx context.Context, q Querier, orgID, learnerID, quizBlockID string, attemptNumber, scoreEarned, scoreMax int, percentage float64, passed bool) (*LearnerQuizScore, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_quiz_score (org_id, learner_id, quiz_block_id, attempt_number, score_earned, score_max, percentage, passed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+learnerQuizScoreColumns, orgID, learnerID, quizBlockID, attemptNumber, scoreEarned, scoreMax, percentage, passed)
	return scanLearnerQuizScore(row)
}

func (r *LearnerQuizScoreRepo) Get(ctx context.Context, q Querier, learnerID, quizBlockID string, attemptNumber int) (*LearnerQuizScore, error) {
	row := q.QueryRow(ctx, `
		SELECT `+learnerQuizScoreColumns+` FROM learner_quiz_score
		WHERE learner_id = $1 AND quiz_block_id = $2 AND attempt_number = $3`, learnerID, quizBlockID, attemptNumber)
	return scanLearnerQuizScore(row)
}

// GetBestScore returns the highest-percentage score across all of a
// learner's attempts on a quiz block — what completion-rule evaluation
// and dashboards care about, not any single attempt.
func (r *LearnerQuizScoreRepo) GetBestScore(ctx context.Context, q Querier, learnerID, quizBlockID string) (*LearnerQuizScore, error) {
	row := q.QueryRow(ctx, `
		SELECT `+learnerQuizScoreColumns+` FROM learner_quiz_score
		WHERE learner_id = $1 AND quiz_block_id = $2
		ORDER BY percentage DESC, attempt_number DESC
		LIMIT 1`, learnerID, quizBlockID)
	return scanLearnerQuizScore(row)
}

func scanLearnerQuizScore(row pgx.Row) (*LearnerQuizScore, error) {
	var s LearnerQuizScore
	if err := row.Scan(&s.ID, &s.OrgID, &s.LearnerID, &s.QuizBlockID, &s.AttemptNumber, &s.ScoreEarned, &s.ScoreMax, &s.Percentage, &s.Passed, &s.GradedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner quiz score: %w", err)
	}
	return &s, nil
}
