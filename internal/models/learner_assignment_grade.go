package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LearnerAssignmentGrade is teacher-authored and keyed to a submission
// rather than a learner directly (see the migration's header comment) —
// there is no learner_id column here; learner read-access is expressed at
// the RLS layer via a join back to learner_assignment_submission.learner_id.
type LearnerAssignmentGrade struct {
	ID                string
	OrgID             string
	SubmissionID      string
	GradePercentage   float64
	FeedbackText      *string
	GradedByTeacherID string
	GradedAt          time.Time
}

type LearnerAssignmentGradeRepo struct{}

func NewLearnerAssignmentGradeRepo() *LearnerAssignmentGradeRepo {
	return &LearnerAssignmentGradeRepo{}
}

const learnerAssignmentGradeColumns = `id, org_id, submission_id, grade_percentage, feedback_text, graded_by_teacher_id, graded_at`

// Create records a grade for a submission. UNIQUE (submission_id) means
// this fails on a second Create for the same submission — resubmission is
// modeled as a NEW submission row (new submission_number), not a
// re-graded old one, so grade history stays append-only per submission.
func (r *LearnerAssignmentGradeRepo) Create(ctx context.Context, q Querier, orgID, submissionID string, gradePercentage float64, feedbackText *string, gradedByTeacherID string) (*LearnerAssignmentGrade, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_assignment_grade (org_id, submission_id, grade_percentage, feedback_text, graded_by_teacher_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+learnerAssignmentGradeColumns, orgID, submissionID, gradePercentage, feedbackText, gradedByTeacherID)
	return scanLearnerAssignmentGrade(row)
}

func (r *LearnerAssignmentGradeRepo) GetBySubmission(ctx context.Context, q Querier, submissionID string) (*LearnerAssignmentGrade, error) {
	row := q.QueryRow(ctx, `SELECT `+learnerAssignmentGradeColumns+` FROM learner_assignment_grade WHERE submission_id = $1`, submissionID)
	return scanLearnerAssignmentGrade(row)
}

// ListSubmissionsWithGrades pairs every submission a learner has made on
// an assignment block with its grade (LEFT JOIN — ungraded submissions
// still appear, with a nil grade), for the grading-history view.
func (r *LearnerAssignmentGradeRepo) ListSubmissionsWithGrades(ctx context.Context, q Querier, learnerID, assignmentBlockID string) ([]*LearnerAssignmentSubmission, []*LearnerAssignmentGrade, error) {
	rows, err := q.Query(ctx, `
		SELECT
			s.id, s.org_id, s.learner_id, s.assignment_block_id, s.submission_number, s.file_path, s.submitted_at, s.submission_status, s.due_date_status,
			g.id, g.org_id, g.submission_id, g.grade_percentage, g.feedback_text, g.graded_by_teacher_id, g.graded_at
		FROM learner_assignment_submission s
		LEFT JOIN learner_assignment_grade g ON g.submission_id = s.id
		WHERE s.learner_id = $1 AND s.assignment_block_id = $2
		ORDER BY s.submission_number DESC`, learnerID, assignmentBlockID)
	if err != nil {
		return nil, nil, fmt.Errorf("models: list submissions with grades: %w", err)
	}
	defer rows.Close()

	var submissions []*LearnerAssignmentSubmission
	var grades []*LearnerAssignmentGrade
	for rows.Next() {
		var s LearnerAssignmentSubmission
		var g LearnerAssignmentGrade
		var gID, gOrgID, gSubmissionID, gGradedByTeacherID *string
		var gGradePercentage *float64
		var gFeedbackText *string
		var gGradedAt *time.Time

		if err := rows.Scan(
			&s.ID, &s.OrgID, &s.LearnerID, &s.AssignmentBlockID, &s.SubmissionNumber, &s.FilePath, &s.SubmittedAt, &s.SubmissionStatus, &s.DueDateStatus,
			&gID, &gOrgID, &gSubmissionID, &gGradePercentage, &gFeedbackText, &gGradedByTeacherID, &gGradedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("models: scan submission with grade: %w", err)
		}
		submissions = append(submissions, &s)

		if gID != nil {
			g.ID = *gID
			g.OrgID = *gOrgID
			g.SubmissionID = *gSubmissionID
			g.GradePercentage = *gGradePercentage
			g.FeedbackText = gFeedbackText
			g.GradedByTeacherID = *gGradedByTeacherID
			g.GradedAt = *gGradedAt
			grades = append(grades, &g)
		} else {
			grades = append(grades, nil)
		}
	}
	return submissions, grades, rows.Err()
}

func scanLearnerAssignmentGrade(row pgx.Row) (*LearnerAssignmentGrade, error) {
	var g LearnerAssignmentGrade
	if err := row.Scan(&g.ID, &g.OrgID, &g.SubmissionID, &g.GradePercentage, &g.FeedbackText, &g.GradedByTeacherID, &g.GradedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner assignment grade: %w", err)
	}
	return &g, nil
}
