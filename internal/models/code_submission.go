package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CodeSubmission is one row of the append-only code-execution ledger (plan.md
// Task 9): a single run — an exercise submission or an ad-hoc run — with its
// captured output, exit code, measured resource usage, and terminal status.
// Written even for runs blocked by the daily cap (status = 'blocked_limit')
// or runner errors (status = 'error') so the ledger is a complete audit of
// every attempt per org.
type CodeSubmission struct {
	ID             string
	OrgID          string
	ExerciseID     *string
	LearnerID      string
	Language       string
	Source         string
	Stdin          string
	Stdout         string
	Stderr         string
	ExitCode       int
	DurationMillis int
	MemoryKB       int
	Runner         string
	Status         string
	Passed         bool
	Error          string
	CreatedAt      time.Time
}

// Submission status values, mirroring the code_submissions.status CHECK.
const (
	CodeStatusSucceeded    = "succeeded"
	CodeStatusFailed       = "failed"
	CodeStatusTimeout      = "timeout"
	CodeStatusOOM          = "oom"
	CodeStatusError        = "error"
	CodeStatusBlockedLimit = "blocked_limit"
)

type CodeSubmissionRepo struct{}

func NewCodeSubmissionRepo() *CodeSubmissionRepo { return &CodeSubmissionRepo{} }

const codeSubmissionColumns = `id, org_id, exercise_id, learner_id, language, source, stdin,
	stdout, stderr, exit_code, duration_millis, memory_kb, runner, status, passed, error, created_at`

func scanCodeSubmission(row pgx.Row) (*CodeSubmission, error) {
	var s CodeSubmission
	if err := row.Scan(&s.ID, &s.OrgID, &s.ExerciseID, &s.LearnerID, &s.Language, &s.Source, &s.Stdin,
		&s.Stdout, &s.Stderr, &s.ExitCode, &s.DurationMillis, &s.MemoryKB, &s.Runner,
		&s.Status, &s.Passed, &s.Error, &s.CreatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func scanCodeSubmissionRows(rows pgx.Rows) (*CodeSubmission, error) {
	s, err := scanCodeSubmission(rows)
	if err != nil {
		return nil, fmt.Errorf("models: scan code submission: %w", err)
	}
	return s, nil
}

// Log inserts one submission ledger row. exerciseID may be "" to store NULL
// (an ad-hoc run). Returns the created row.
func (r *CodeSubmissionRepo) Log(ctx context.Context, q Querier, s CodeSubmission) (*CodeSubmission, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO code_submissions
			(org_id, exercise_id, learner_id, language, source, stdin, stdout, stderr,
			 exit_code, duration_millis, memory_kb, runner, status, passed, error)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING `+codeSubmissionColumns,
		s.OrgID, strOrEmpty(s.ExerciseID), s.LearnerID, s.Language, s.Source, s.Stdin, s.Stdout, s.Stderr,
		s.ExitCode, s.DurationMillis, s.MemoryKB, s.Runner, s.Status, s.Passed, s.Error)
	out, err := scanCodeSubmission(row)
	if err != nil {
		return nil, fmt.Errorf("models: log code submission: %w", err)
	}
	return out, nil
}

// Get returns one submission by id (RLS scopes visibility to the owner or an
// org teacher/owner).
func (r *CodeSubmissionRepo) Get(ctx context.Context, q Querier, id string) (*CodeSubmission, error) {
	row := q.QueryRow(ctx, `SELECT `+codeSubmissionColumns+` FROM code_submissions WHERE id = $1`, id)
	s, err := scanCodeSubmission(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get code submission: %w", err)
	}
	return s, nil
}

// ListForLearner returns a learner's own recent submissions, optionally
// filtered to one exercise (exerciseID "" = all).
func (r *CodeSubmissionRepo) ListForLearner(ctx context.Context, q Querier, learnerID, exerciseID string, limit int) ([]*CodeSubmission, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var (
		rows pgx.Rows
		err  error
	)
	if exerciseID == "" {
		rows, err = q.Query(ctx, `SELECT `+codeSubmissionColumns+`
			FROM code_submissions WHERE learner_id = $1 ORDER BY created_at DESC LIMIT $2`, learnerID, limit)
	} else {
		rows, err = q.Query(ctx, `SELECT `+codeSubmissionColumns+`
			FROM code_submissions WHERE learner_id = $1 AND exercise_id = $2
			ORDER BY created_at DESC LIMIT $3`, learnerID, exerciseID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("models: list learner code submissions: %w", err)
	}
	defer rows.Close()
	return collectCodeSubmissions(rows)
}

// RecentByOrg returns the most recent submissions for an org's usage
// dashboard (owner/teacher only via RLS).
func (r *CodeSubmissionRepo) RecentByOrg(ctx context.Context, q Querier, orgID string, limit int) ([]*CodeSubmission, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.Query(ctx, `SELECT `+codeSubmissionColumns+`
		FROM code_submissions WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("models: recent org code submissions: %w", err)
	}
	defer rows.Close()
	return collectCodeSubmissions(rows)
}

func collectCodeSubmissions(rows pgx.Rows) ([]*CodeSubmission, error) {
	var out []*CodeSubmission
	for rows.Next() {
		s, err := scanCodeSubmissionRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate code submissions: %w", err)
	}
	return out, nil
}
