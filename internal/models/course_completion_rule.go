package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	CompletionRuleAllLessons     = "all_lessons"
	CompletionRulePercentLessons = "percent_lessons"
	CompletionRuleAllQuizzes     = "all_quizzes"
	CompletionRulePercentQuizzes = "percent_quizzes"
)

type CourseCompletionRule struct {
	ID        string
	CourseID  string
	OrgID     string
	RuleType  string
	Threshold int
	CreatedBy string
	UpdatedAt time.Time
}

type CourseCompletionRuleRepo struct{}

func NewCourseCompletionRuleRepo() *CourseCompletionRuleRepo { return &CourseCompletionRuleRepo{} }

const completionRuleColumns = `id, course_id, org_id, rule_type, threshold, created_by, updated_at`

func (r *CourseCompletionRuleRepo) Create(ctx context.Context, q Querier, courseID, orgID, createdBy, ruleType string, threshold int) (*CourseCompletionRule, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO course_completion_rules (course_id, org_id, rule_type, threshold, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+completionRuleColumns, courseID, orgID, ruleType, threshold, createdBy)
	return scanCompletionRule(row)
}

func (r *CourseCompletionRuleRepo) ListForCourse(ctx context.Context, q Querier, courseID string) ([]*CourseCompletionRule, error) {
	rows, err := q.Query(ctx, `SELECT `+completionRuleColumns+` FROM course_completion_rules WHERE course_id = $1`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list completion rules: %w", err)
	}
	defer rows.Close()

	var out []*CourseCompletionRule
	for rows.Next() {
		var cr CourseCompletionRule
		if err := rows.Scan(&cr.ID, &cr.CourseID, &cr.OrgID, &cr.RuleType, &cr.Threshold, &cr.CreatedBy, &cr.UpdatedAt); err != nil {
			return nil, fmt.Errorf("models: scan completion rule: %w", err)
		}
		out = append(out, &cr)
	}
	return out, rows.Err()
}

func (r *CourseCompletionRuleRepo) Update(ctx context.Context, q Querier, id, ruleType string, threshold int) (*CourseCompletionRule, error) {
	row := q.QueryRow(ctx, `
		UPDATE course_completion_rules SET rule_type = $2, threshold = $3, updated_at = now()
		WHERE id = $1 RETURNING `+completionRuleColumns, id, ruleType, threshold)
	return scanCompletionRule(row)
}

func (r *CourseCompletionRuleRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM course_completion_rules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete completion rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCompletionRule(row pgx.Row) (*CourseCompletionRule, error) {
	var cr CourseCompletionRule
	if err := row.Scan(&cr.ID, &cr.CourseID, &cr.OrgID, &cr.RuleType, &cr.Threshold, &cr.CreatedBy, &cr.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan completion rule: %w", err)
	}
	return &cr, nil
}
