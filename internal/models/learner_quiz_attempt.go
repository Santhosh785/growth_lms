package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LearnerQuizAttempt is an immutable submission record (answers_json), one
// row per attempt. Scoring lives separately in LearnerQuizScore so a
// quiz's answer key can be redacted from any response containing an
// attempt without also having to redact score data.
type LearnerQuizAttempt struct {
	ID            string
	OrgID         string
	LearnerID     string
	QuizBlockID   string
	AttemptNumber int
	AnswersJSON   json.RawMessage
	SubmittedAt   time.Time
}

type LearnerQuizAttemptRepo struct{}

func NewLearnerQuizAttemptRepo() *LearnerQuizAttemptRepo { return &LearnerQuizAttemptRepo{} }

const learnerQuizAttemptColumns = `id, org_id, learner_id, quiz_block_id, attempt_number, answers_json, submitted_at`

// Create inserts a new attempt. attemptNumber is caller-supplied (the
// handler computes next-attempt-number from CountByLearnerAndBlock) rather
// than DB-generated, since the UNIQUE (learner_id, quiz_block_id,
// attempt_number) constraint needs a value to check against.
func (r *LearnerQuizAttemptRepo) Create(ctx context.Context, q Querier, orgID, learnerID, quizBlockID string, attemptNumber int, answersJSON json.RawMessage) (*LearnerQuizAttempt, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_quiz_attempt (org_id, learner_id, quiz_block_id, attempt_number, answers_json)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+learnerQuizAttemptColumns, orgID, learnerID, quizBlockID, attemptNumber, answersJSON)
	return scanLearnerQuizAttempt(row)
}

// ListByLearnerAndBlock returns every attempt a learner has made on a
// quiz block, newest first, used for attempt-history and best-score views.
func (r *LearnerQuizAttemptRepo) ListByLearnerAndBlock(ctx context.Context, q Querier, learnerID, quizBlockID string) ([]*LearnerQuizAttempt, error) {
	rows, err := q.Query(ctx, `
		SELECT `+learnerQuizAttemptColumns+` FROM learner_quiz_attempt
		WHERE learner_id = $1 AND quiz_block_id = $2
		ORDER BY attempt_number DESC`, learnerID, quizBlockID)
	if err != nil {
		return nil, fmt.Errorf("models: list learner quiz attempts: %w", err)
	}
	defer rows.Close()

	var out []*LearnerQuizAttempt
	for rows.Next() {
		a, err := scanLearnerQuizAttemptRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountByLearnerAndBlock is used to compute the next attempt_number.
func (r *LearnerQuizAttemptRepo) CountByLearnerAndBlock(ctx context.Context, q Querier, learnerID, quizBlockID string) (int, error) {
	var count int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM learner_quiz_attempt WHERE learner_id = $1 AND quiz_block_id = $2`, learnerID, quizBlockID).Scan(&count); err != nil {
		return 0, fmt.Errorf("models: count learner quiz attempts: %w", err)
	}
	return count, nil
}

func scanLearnerQuizAttempt(row pgx.Row) (*LearnerQuizAttempt, error) {
	var a LearnerQuizAttempt
	if err := row.Scan(&a.ID, &a.OrgID, &a.LearnerID, &a.QuizBlockID, &a.AttemptNumber, &a.AnswersJSON, &a.SubmittedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner quiz attempt: %w", err)
	}
	return &a, nil
}

func scanLearnerQuizAttemptRows(rows pgx.Rows) (*LearnerQuizAttempt, error) {
	var a LearnerQuizAttempt
	if err := rows.Scan(&a.ID, &a.OrgID, &a.LearnerID, &a.QuizBlockID, &a.AttemptNumber, &a.AnswersJSON, &a.SubmittedAt); err != nil {
		return nil, fmt.Errorf("models: scan learner quiz attempt: %w", err)
	}
	return &a, nil
}
