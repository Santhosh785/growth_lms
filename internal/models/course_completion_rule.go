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

// EvaluateCompletion checks whether learnerID has satisfied every one of
// courseID's completion rules (AND across rules, matching the spec — a
// course with multiple rules requires all of them to pass, not any one).
//
// If the course has NO completion rules defined at all, this treats it as
// "complete when all_lessons" — a deliberate default (not in the written
// spec, a judgment call made here): a course still needs *some* definition
// of "complete" to ever issue a certificate, and "every lesson viewed" is
// the least surprising default absent explicit teacher configuration.
func EvaluateCompletion(ctx context.Context, q Querier, courseID, learnerID string) (bool, error) {
	rules, err := (&CourseCompletionRuleRepo{}).ListForCourse(ctx, q, courseID)
	if err != nil {
		return false, err
	}
	if len(rules) == 0 {
		rules = []*CourseCompletionRule{{RuleType: CompletionRuleAllLessons, Threshold: 100}}
	}

	totalLessons, completedLessons, err := countCourseLessons(ctx, q, courseID, learnerID)
	if err != nil {
		return false, err
	}

	for _, rule := range rules {
		var ok bool
		switch rule.RuleType {
		case CompletionRuleAllLessons:
			ok = totalLessons > 0 && completedLessons == totalLessons
		case CompletionRulePercentLessons:
			ok = totalLessons > 0 && percentOf(completedLessons, totalLessons) >= float64(rule.Threshold)
		case CompletionRuleAllQuizzes:
			totalQuizzes, passedQuizzes, err := countCourseQuizzes(ctx, q, courseID, learnerID)
			if err != nil {
				return false, err
			}
			ok = totalQuizzes > 0 && passedQuizzes == totalQuizzes
		case CompletionRulePercentQuizzes:
			totalQuizzes, passedQuizzes, err := countCourseQuizzes(ctx, q, courseID, learnerID)
			if err != nil {
				return false, err
			}
			ok = totalQuizzes > 0 && percentOf(passedQuizzes, totalQuizzes) >= float64(rule.Threshold)
		default:
			// Unrecognized rule_type (shouldn't happen given the CHECK
			// constraint) — fail closed rather than silently ignore it.
			ok = false
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func percentOf(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// countCourseLessons returns the course's total lesson count and how many
// of them learnerID has completed (learner_lesson_progress.completed_at
// set).
func countCourseLessons(ctx context.Context, q Querier, courseID, learnerID string) (total, completed int, err error) {
	if err := q.QueryRow(ctx, `SELECT count(*) FROM lessons WHERE course_id = $1`, courseID).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("models: count course lessons: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM learner_lesson_progress
		WHERE course_id = $1 AND learner_id = $2 AND completed_at IS NOT NULL
	`, courseID, learnerID).Scan(&completed); err != nil {
		return 0, 0, fmt.Errorf("models: count completed course lessons: %w", err)
	}
	return total, completed, nil
}

// countCourseQuizzes returns the course's total quiz-block count and how
// many of them learnerID has a passing learner_quiz_score for (best score
// across attempts, per quiz block).
func countCourseQuizzes(ctx context.Context, q Querier, courseID, learnerID string) (total, passed int, err error) {
	if err := q.QueryRow(ctx, `SELECT count(*) FROM blocks WHERE course_id = $1 AND type = 'quiz'`, courseID).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("models: count course quiz blocks: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(DISTINCT b.id) FROM blocks b
		WHERE b.course_id = $1 AND b.type = 'quiz'
		AND EXISTS (
			SELECT 1 FROM learner_quiz_score s
			WHERE s.quiz_block_id = b.id AND s.learner_id = $2 AND s.passed
		)
	`, courseID, learnerID).Scan(&passed); err != nil {
		return 0, 0, fmt.Errorf("models: count passed course quiz blocks: %w", err)
	}
	return total, passed, nil
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
