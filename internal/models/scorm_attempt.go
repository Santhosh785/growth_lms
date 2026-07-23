package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ScormAttempt is one learner's tracked runtime state for one attempt at a
// SCORM package (plan.md Task 9's "progress" + "reporting"). The denormalized
// summary columns drive reporting; CMIData holds the full committed element map
// for exact resume and detailed 2004 interaction reporting. Learner-owned via
// flat RLS on LearnerID, the same shape as PodcastProgress. org_id is
// denormalized from the parent package for flat RLS.
type ScormAttempt struct {
	ID                 string
	OrgID              string
	PackageID          string
	LearnerID          string
	AttemptNumber      int
	LessonStatus       string
	CompletionStatus   string
	SuccessStatus      string
	ScoreRaw           *float64
	ScoreMin           *float64
	ScoreMax           *float64
	ScoreScaled        *float64
	TotalTimeSeconds   int
	SessionTimeSeconds int
	Location           string
	SuspendData        string
	CMIData            json.RawMessage
	IsComplete         bool
	StartedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
}

// ScormRuntimeUpdate is the mutable subset a Commit writes onto an attempt: the
// summarized tracking fields plus the full CMI element map. SessionTimeSeconds
// is the current session's reported time; Commit stores it verbatim AND folds
// its growth since the row's last-committed session time into the accumulated
// total_time — the delta is computed in SQL against the pre-update column value
// so concurrent commits on the same attempt (auto-commit retries, two tabs)
// can't double-count via a stale app-level read.
type ScormRuntimeUpdate struct {
	LessonStatus       string
	CompletionStatus   string
	SuccessStatus      string
	ScoreRaw           *float64
	ScoreMin           *float64
	ScoreMax           *float64
	ScoreScaled        *float64
	SessionTimeSeconds int
	Location           string
	SuspendData        string
	CMIData            json.RawMessage
	IsComplete         bool
}

type ScormAttemptRepo struct{}

func NewScormAttemptRepo() *ScormAttemptRepo { return &ScormAttemptRepo{} }

const scormAttemptColumns = `id, org_id, package_id, learner_id, attempt_number,
	lesson_status, completion_status, success_status,
	score_raw, score_min, score_max, score_scaled,
	total_time_seconds, session_time_seconds, location, suspend_data, cmi_data,
	is_complete, started_at, updated_at, completed_at`

func scanScormAttempt(row pgx.Row) (*ScormAttempt, error) {
	var a ScormAttempt
	if err := row.Scan(&a.ID, &a.OrgID, &a.PackageID, &a.LearnerID, &a.AttemptNumber,
		&a.LessonStatus, &a.CompletionStatus, &a.SuccessStatus,
		&a.ScoreRaw, &a.ScoreMin, &a.ScoreMax, &a.ScoreScaled,
		&a.TotalTimeSeconds, &a.SessionTimeSeconds, &a.Location, &a.SuspendData, &a.CMIData,
		&a.IsComplete, &a.StartedAt, &a.UpdatedAt, &a.CompletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// latest returns the highest-numbered attempt for (package, learner), or
// ErrNotFound if the learner has never launched this package.
func (r *ScormAttemptRepo) latest(ctx context.Context, q Querier, packageID, learnerID string) (*ScormAttempt, error) {
	row := q.QueryRow(ctx, `SELECT `+scormAttemptColumns+`
		FROM scorm_attempts WHERE package_id = $1 AND learner_id = $2
		ORDER BY attempt_number DESC LIMIT 1`, packageID, learnerID)
	return scanScormAttempt(row)
}

// StartOrResume returns the attempt a learner launches into: the latest one if
// it is still in progress (resume), otherwise a fresh attempt (the first ever,
// or the next number after a completed one). Runs in the request transaction,
// so the read-then-insert is consistent under RLS.
func (r *ScormAttemptRepo) StartOrResume(ctx context.Context, q Querier, orgID, packageID, learnerID string) (*ScormAttempt, error) {
	prev, err := r.latest(ctx, q, packageID, learnerID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("models: scorm start/resume lookup: %w", err)
	}
	if err == nil && !prev.IsComplete {
		return prev, nil
	}
	next := 1
	if prev != nil {
		next = prev.AttemptNumber + 1
	}
	row := q.QueryRow(ctx, `
		INSERT INTO scorm_attempts (org_id, package_id, learner_id, attempt_number)
		VALUES ($1, $2, $3, $4)
		RETURNING `+scormAttemptColumns,
		orgID, packageID, learnerID, next)
	a, err := scanScormAttempt(row)
	if err != nil {
		return nil, fmt.Errorf("models: start scorm attempt: %w", err)
	}
	return a, nil
}

// Get returns one attempt by id (RLS scopes visibility to the owning learner or
// an org teacher/owner).
func (r *ScormAttemptRepo) Get(ctx context.Context, q Querier, id string) (*ScormAttempt, error) {
	row := q.QueryRow(ctx, `SELECT `+scormAttemptColumns+` FROM scorm_attempts WHERE id = $1`, id)
	a, err := scanScormAttempt(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get scorm attempt: %w", err)
	}
	return a, nil
}

// Commit folds a runtime update onto an attempt: the summary fields are
// overwritten, session time is added to the accumulated total, and
// completed_at is stamped the first time the attempt becomes complete (sticky —
// a later commit never clears it). RLS restricts UPDATE to the owning learner.
func (r *ScormAttemptRepo) Commit(ctx context.Context, q Querier, id string, u ScormRuntimeUpdate) (*ScormAttempt, error) {
	row := q.QueryRow(ctx, `
		UPDATE scorm_attempts SET
			lesson_status = $2,
			completion_status = $3,
			success_status = $4,
			score_raw = $5, score_min = $6, score_max = $7, score_scaled = $8,
			-- Fold the growth of the reported session time since the last commit
			-- into the accumulated total. The RHS references the pre-update
			-- session_time_seconds, so this is atomic against a concurrent commit
			-- (which serializes on the row lock) — no stale app-level delta. A
			-- smaller reading than the stored one means a new session after a
			-- resume, so its full value is added.
			total_time_seconds = total_time_seconds + CASE
				WHEN $9 >= scorm_attempts.session_time_seconds
					THEN $9 - scorm_attempts.session_time_seconds
				ELSE $9 END,
			session_time_seconds = $9,
			location = $10,
			suspend_data = $11,
			cmi_data = COALESCE($12, '{}')::jsonb,
			is_complete = scorm_attempts.is_complete OR $13,
			completed_at = CASE
				WHEN scorm_attempts.completed_at IS NOT NULL THEN scorm_attempts.completed_at
				WHEN $13 THEN now()
				ELSE NULL END,
			updated_at = now()
		WHERE id = $1
		RETURNING `+scormAttemptColumns,
		id, u.LessonStatus, u.CompletionStatus, u.SuccessStatus,
		u.ScoreRaw, u.ScoreMin, u.ScoreMax, u.ScoreScaled,
		u.SessionTimeSeconds, u.Location, u.SuspendData, jsonOrNil(u.CMIData), u.IsComplete)
	a, err := scanScormAttempt(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: commit scorm attempt: %w", err)
	}
	return a, nil
}

// ListForLearner returns a learner's own attempts across every package, newest
// first, for their in-app SCORM progress view.
func (r *ScormAttemptRepo) ListForLearner(ctx context.Context, q Querier, learnerID string, limit int) ([]*ScormAttempt, error) {
	rows, err := q.Query(ctx, `SELECT `+scormAttemptColumns+`
		FROM scorm_attempts WHERE learner_id = $1 ORDER BY updated_at DESC LIMIT $2`,
		learnerID, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("models: list learner scorm attempts: %w", err)
	}
	defer rows.Close()
	return collectScormAttempts(rows)
}

// ListByPackage returns every learner's attempts at one package, newest first,
// for the teacher/owner reporting view (RLS grants teachers org-wide read).
func (r *ScormAttemptRepo) ListByPackage(ctx context.Context, q Querier, packageID string, limit int) ([]*ScormAttempt, error) {
	rows, err := q.Query(ctx, `SELECT `+scormAttemptColumns+`
		FROM scorm_attempts WHERE package_id = $1 ORDER BY updated_at DESC LIMIT $2`,
		packageID, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("models: list scorm attempts by package: %w", err)
	}
	defer rows.Close()
	return collectScormAttempts(rows)
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 500 {
		return 100
	}
	return limit
}

func collectScormAttempts(rows pgx.Rows) ([]*ScormAttempt, error) {
	var out []*ScormAttempt
	for rows.Next() {
		a, err := scanScormAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("models: scan scorm attempt: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate scorm attempts: %w", err)
	}
	return out, nil
}
