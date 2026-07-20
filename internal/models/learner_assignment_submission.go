package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Submission statuses and due-date statuses, matching the CHECK
// constraints in db/migrations/000004_learner_journey.up.sql.
const (
	SubmissionStatusSubmitted   = "submitted"
	SubmissionStatusGraded      = "graded"
	SubmissionStatusResubmitted = "resubmitted"

	DueDateStatusOnTime = "on_time"
	DueDateStatusLate   = "late"
)

type LearnerAssignmentSubmission struct {
	ID                string
	OrgID             string
	LearnerID         string
	AssignmentBlockID string
	SubmissionNumber  int
	FilePath          string
	SubmittedAt       time.Time
	SubmissionStatus  string
	DueDateStatus     string
}

type LearnerAssignmentSubmissionRepo struct{}

func NewLearnerAssignmentSubmissionRepo() *LearnerAssignmentSubmissionRepo {
	return &LearnerAssignmentSubmissionRepo{}
}

const learnerAssignmentSubmissionColumns = `id, org_id, learner_id, assignment_block_id, submission_number, file_path, submitted_at, submission_status, due_date_status`

// Create inserts a new submission. submissionNumber is caller-supplied
// (via CountByLearnerAndBlock) so resubmission increments it correctly
// against the UNIQUE (learner_id, assignment_block_id, submission_number)
// constraint; submissionStatus is 'submitted' for the first attempt and
// 'resubmitted' for later ones, decided by the handler.
func (r *LearnerAssignmentSubmissionRepo) Create(ctx context.Context, q Querier, orgID, learnerID, assignmentBlockID string, submissionNumber int, filePath, submissionStatus, dueDateStatus string) (*LearnerAssignmentSubmission, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_assignment_submission (org_id, learner_id, assignment_block_id, submission_number, file_path, submission_status, due_date_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+learnerAssignmentSubmissionColumns, orgID, learnerID, assignmentBlockID, submissionNumber, filePath, submissionStatus, dueDateStatus)
	return scanLearnerAssignmentSubmission(row)
}

func (r *LearnerAssignmentSubmissionRepo) Get(ctx context.Context, q Querier, id string) (*LearnerAssignmentSubmission, error) {
	row := q.QueryRow(ctx, `SELECT `+learnerAssignmentSubmissionColumns+` FROM learner_assignment_submission WHERE id = $1`, id)
	return scanLearnerAssignmentSubmission(row)
}

// CountByLearnerAndBlock is used to compute the next submission_number.
func (r *LearnerAssignmentSubmissionRepo) CountByLearnerAndBlock(ctx context.Context, q Querier, learnerID, assignmentBlockID string) (int, error) {
	var count int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM learner_assignment_submission WHERE learner_id = $1 AND assignment_block_id = $2`, learnerID, assignmentBlockID).Scan(&count); err != nil {
		return 0, fmt.Errorf("models: count learner assignment submissions: %w", err)
	}
	return count, nil
}

// ListByLearnerAndBlock returns every submission (across resubmissions) a
// learner has made on an assignment block, newest first — paired with
// grades by ListSubmissionsWithGrades for the grading-history view.
func (r *LearnerAssignmentSubmissionRepo) ListByLearnerAndBlock(ctx context.Context, q Querier, learnerID, assignmentBlockID string) ([]*LearnerAssignmentSubmission, error) {
	rows, err := q.Query(ctx, `
		SELECT `+learnerAssignmentSubmissionColumns+` FROM learner_assignment_submission
		WHERE learner_id = $1 AND assignment_block_id = $2
		ORDER BY submission_number DESC`, learnerID, assignmentBlockID)
	if err != nil {
		return nil, fmt.Errorf("models: list learner assignment submissions: %w", err)
	}
	defer rows.Close()

	var out []*LearnerAssignmentSubmission
	for rows.Next() {
		s, err := scanLearnerAssignmentSubmissionRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListPendingByCourse is the teacher grading queue: every submission in a
// course whose status is still 'submitted' or 'resubmitted' (i.e. not yet
// graded), oldest first so teachers work through the backlog in order.
// assignment_block_id joins to blocks to scope by course_id since
// learner_assignment_submission has no course_id column of its own.
func (r *LearnerAssignmentSubmissionRepo) ListPendingByCourse(ctx context.Context, q Querier, courseID string) ([]*LearnerAssignmentSubmission, error) {
	rows, err := q.Query(ctx, `
		SELECT s.id, s.org_id, s.learner_id, s.assignment_block_id, s.submission_number, s.file_path, s.submitted_at, s.submission_status, s.due_date_status
		FROM learner_assignment_submission s
		JOIN blocks b ON b.id = s.assignment_block_id
		WHERE b.course_id = $1 AND s.submission_status IN ('submitted', 'resubmitted')
		ORDER BY s.submitted_at ASC`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list pending assignment submissions: %w", err)
	}
	defer rows.Close()

	var out []*LearnerAssignmentSubmission
	for rows.Next() {
		s, err := scanLearnerAssignmentSubmissionRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PendingSubmissionSummary is a dashboard-only read model (Task 5 Stage
// 8): a learner's own not-yet-graded submission, joined with just enough
// course/lesson context to link back to it from the dashboard.
type PendingSubmissionSummary struct {
	SubmissionID  string
	CourseID      string
	CourseTitle   string
	LessonID      string
	LessonTitle   string
	SubmittedAt   time.Time
	DueDateStatus string
}

// ListPendingByLearner returns every submission a learner has made that
// is still awaiting grading ('submitted'/'resubmitted'), oldest first,
// across every course — powers the learner dashboard's "pending
// submissions" section.
func (r *LearnerAssignmentSubmissionRepo) ListPendingByLearner(ctx context.Context, q Querier, learnerID string) ([]PendingSubmissionSummary, error) {
	rows, err := q.Query(ctx, `
		SELECT s.id, c.id, c.title, l.id, l.title, s.submitted_at, s.due_date_status
		FROM learner_assignment_submission s
		JOIN blocks b ON b.id = s.assignment_block_id
		JOIN lessons l ON l.id = b.lesson_id
		JOIN courses c ON c.id = b.course_id
		WHERE s.learner_id = $1 AND s.submission_status IN ('submitted', 'resubmitted')
		ORDER BY s.submitted_at ASC`, learnerID)
	if err != nil {
		return nil, fmt.Errorf("models: list pending assignment submissions by learner: %w", err)
	}
	defer rows.Close()

	var out []PendingSubmissionSummary
	for rows.Next() {
		var s PendingSubmissionSummary
		if err := rows.Scan(&s.SubmissionID, &s.CourseID, &s.CourseTitle, &s.LessonID, &s.LessonTitle, &s.SubmittedAt, &s.DueDateStatus); err != nil {
			return nil, fmt.Errorf("models: scan pending assignment submission summary: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetStatus is used once a grade is recorded, moving the submission from
// 'submitted'/'resubmitted' to 'graded'.
func (r *LearnerAssignmentSubmissionRepo) SetStatus(ctx context.Context, q Querier, id, status string) (*LearnerAssignmentSubmission, error) {
	row := q.QueryRow(ctx, `
		UPDATE learner_assignment_submission SET submission_status = $2
		WHERE id = $1 RETURNING `+learnerAssignmentSubmissionColumns, id, status)
	return scanLearnerAssignmentSubmission(row)
}

func scanLearnerAssignmentSubmission(row pgx.Row) (*LearnerAssignmentSubmission, error) {
	var s LearnerAssignmentSubmission
	if err := row.Scan(&s.ID, &s.OrgID, &s.LearnerID, &s.AssignmentBlockID, &s.SubmissionNumber, &s.FilePath, &s.SubmittedAt, &s.SubmissionStatus, &s.DueDateStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner assignment submission: %w", err)
	}
	return &s, nil
}

func scanLearnerAssignmentSubmissionRows(rows pgx.Rows) (*LearnerAssignmentSubmission, error) {
	var s LearnerAssignmentSubmission
	if err := rows.Scan(&s.ID, &s.OrgID, &s.LearnerID, &s.AssignmentBlockID, &s.SubmissionNumber, &s.FilePath, &s.SubmittedAt, &s.SubmissionStatus, &s.DueDateStatus); err != nil {
		return nil, fmt.Errorf("models: scan learner assignment submission: %w", err)
	}
	return &s, nil
}
