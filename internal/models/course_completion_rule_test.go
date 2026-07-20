package models_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/testutil"
)

// These tests use the admin (RLS-bypassing superuser) connection for
// every repo call, same as this package's other setup helpers
// (seedUser/seedOrgWithOwner) — EvaluateCompletion's own RLS behavior
// isn't the thing under test here (it's a plain Querier, exercised
// end-to-end over real HTTP/RLS elsewhere); what's under test is its rule
// evaluation logic, which needs a course/chapter/lesson/quiz-block fixture
// with mixed owner- and learner-authored rows that would otherwise
// require juggling two separate RLS-scoped transactions across
// uncommitted work.

// TestEvaluateCompletion covers the rule types (Task 5 Stage 6 spec):
// all_lessons/all_quizzes, the "no rules defined at all" default (treated
// as all_lessons), and that multiple rules on the same course are ANDed
// together, not ORed.
func TestEvaluateCompletion(t *testing.T) {
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgID := seedOrgWithOwner(t, admin, ownerID, "completion-"+uuid.NewString())

	courses := models.NewCourseRepo()
	chapters := models.NewChapterRepo()
	lessons := models.NewLessonRepo()
	blocks := models.NewBlockRepo()
	rules := models.NewCourseCompletionRuleRepo()
	progress := models.NewLearnerLessonProgressRepo()
	scores := models.NewLearnerQuizScoreRepo()

	course, err := courses.Create(ctx, admin, orgID, ownerID, "Completion Course", "desc", nil)
	require.NoError(t, err)
	chapter, err := chapters.Create(ctx, admin, course.ID, orgID, ownerID, "Ch1", 1)
	require.NoError(t, err)
	lesson1, err := lessons.Create(ctx, admin, chapter.ID, course.ID, orgID, ownerID, "Lesson 1", 1)
	require.NoError(t, err)
	lesson2, err := lessons.Create(ctx, admin, chapter.ID, course.ID, orgID, ownerID, "Lesson 2", 2)
	require.NoError(t, err)

	quizContent, err := json.Marshal(models.QuizBlockContent{Questions: []models.QuizQuestion{{ID: "q1", Type: "mcq"}}})
	require.NoError(t, err)
	quizBlock, err := blocks.Create(ctx, admin, lesson1.ID, course.ID, orgID, ownerID, models.BlockTypeQuiz, quizContent, 1)
	require.NoError(t, err)

	// No completion rules defined at all: defaults to all_lessons.
	complete, err := models.EvaluateCompletion(ctx, admin, course.ID, learnerID)
	require.NoError(t, err)
	require.False(t, complete, "no lessons completed yet")

	_, err = progress.MarkComplete(ctx, admin, orgID, learnerID, lesson1.ID, course.ID)
	require.NoError(t, err)
	complete, err = models.EvaluateCompletion(ctx, admin, course.ID, learnerID)
	require.NoError(t, err)
	require.False(t, complete, "one of two lessons completed, default all_lessons rule not satisfied")

	_, err = progress.MarkComplete(ctx, admin, orgID, learnerID, lesson2.ID, course.ID)
	require.NoError(t, err)
	complete, err = models.EvaluateCompletion(ctx, admin, course.ID, learnerID)
	require.NoError(t, err)
	require.True(t, complete, "all lessons now completed, default all_lessons rule satisfied")

	// Now add an explicit all_quizzes rule too — completion must AND with
	// it: even though all_lessons is (still) satisfied, the quiz hasn't
	// been passed yet.
	_, err = rules.Create(ctx, admin, course.ID, orgID, ownerID, models.CompletionRuleAllLessons, 100)
	require.NoError(t, err)
	_, err = rules.Create(ctx, admin, course.ID, orgID, ownerID, models.CompletionRuleAllQuizzes, 100)
	require.NoError(t, err)

	complete, err = models.EvaluateCompletion(ctx, admin, course.ID, learnerID)
	require.NoError(t, err)
	require.False(t, complete, "all_lessons passes but all_quizzes doesn't yet — AND, not OR")

	_, err = scores.Create(ctx, admin, orgID, learnerID, quizBlock.ID, 1, 1, 1, 100, true)
	require.NoError(t, err)
	complete, err = models.EvaluateCompletion(ctx, admin, course.ID, learnerID)
	require.NoError(t, err)
	require.True(t, complete, "both all_lessons and all_quizzes now satisfied")
}

// TestEvaluateCompletion_PercentRules covers percent_lessons threshold
// comparisons.
func TestEvaluateCompletion_PercentRules(t *testing.T) {
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgID := seedOrgWithOwner(t, admin, ownerID, "completion-pct-"+uuid.NewString())

	courses := models.NewCourseRepo()
	chapters := models.NewChapterRepo()
	lessons := models.NewLessonRepo()
	rules := models.NewCourseCompletionRuleRepo()
	progress := models.NewLearnerLessonProgressRepo()

	course, err := courses.Create(ctx, admin, orgID, ownerID, "Percent Course", "desc", nil)
	require.NoError(t, err)
	chapter, err := chapters.Create(ctx, admin, course.ID, orgID, ownerID, "Ch1", 1)
	require.NoError(t, err)
	lesson1, err := lessons.Create(ctx, admin, chapter.ID, course.ID, orgID, ownerID, "Lesson 1", 1)
	require.NoError(t, err)
	_, err = lessons.Create(ctx, admin, chapter.ID, course.ID, orgID, ownerID, "Lesson 2", 2)
	require.NoError(t, err)

	_, err = rules.Create(ctx, admin, course.ID, orgID, ownerID, models.CompletionRulePercentLessons, 50)
	require.NoError(t, err)

	complete, err := models.EvaluateCompletion(ctx, admin, course.ID, learnerID)
	require.NoError(t, err)
	require.False(t, complete, "0 of 2 lessons complete, below 50% threshold")

	_, err = progress.MarkComplete(ctx, admin, orgID, learnerID, lesson1.ID, course.ID)
	require.NoError(t, err)
	complete, err = models.EvaluateCompletion(ctx, admin, course.ID, learnerID)
	require.NoError(t, err)
	require.True(t, complete, "1 of 2 lessons complete meets the 50% threshold")
}
