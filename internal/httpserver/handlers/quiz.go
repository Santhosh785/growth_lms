package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// defaultQuizPassingPercent is the passing threshold applied to every
// quiz attempt. Task 4's QuizBlockContent/QuizQuestion structs have no
// passing_grade field, and main-plan.md's Stage 4 scope doesn't add one
// (that would be a Task 4 schema change out of this stage's scope) — so
// this is a hardcoded MVP value, flagged here and in the stage report as
// a gap: a real product would want this configurable per quiz.
const defaultQuizPassingPercent = 70.0

// redactedQuizQuestion is the learner-facing "take quiz" shape. It is a
// deliberately separate struct from models.QuizQuestion (NOT a re-marshal
// of it) so that CorrectAnswerIndex and AcceptedAnswers — which
// QuizQuestion's own json tags would include — can never leak into a
// pre-attempt response. Only add fields here after confirming they don't
// reveal the answer key.
type redactedQuizQuestion struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Question string   `json:"question"`
	Answers  []string `json:"answers,omitempty"`
}

// redactQuizQuestions strips every answer-key field from a quiz block's
// questions, returning only what a learner taking the quiz should see.
func redactQuizQuestions(questions []models.QuizQuestion) []redactedQuizQuestion {
	out := make([]redactedQuizQuestion, len(questions))
	for i, q := range questions {
		out[i] = redactedQuizQuestion{
			ID:       q.ID,
			Type:     q.Type,
			Question: q.Question,
			Answers:  q.Answers,
		}
	}
	return out
}

// quizBlockInLesson loads :blockId, verifies it belongs to the given
// lesson (which lessonInCourse has already verified belongs to the
// course), and that it is actually a quiz block — writing a 404 response
// and returning ok=false otherwise. Mirrors lessonInCourse's
// don't-trust-client-supplied-IDs pattern one level deeper.
func quizBlockInLesson(c *gin.Context, d *AuthDeps, lessonID string) (*models.Block, *models.QuizBlockContent, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	block, err := d.Blocks.Get(c.Request.Context(), tx, c.Param("blockId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "quiz not found"})
			return nil, nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}
	if block.LessonID != lessonID || block.Type != models.BlockTypeQuiz {
		c.JSON(http.StatusNotFound, gin.H{"error": "quiz not found"})
		return nil, nil, false
	}

	var content models.QuizBlockContent
	if err := json.Unmarshal(block.Content, &content); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}
	return block, &content, true
}

// GetQuiz returns the redacted question list (never correct_answer_index
// or accepted_answers), plus the learner's attempt count so far and best
// score if any. Attempts are unlimited for MVP (grilling-record.md Q2
// dropped any per-quiz max-attempts config; Task 4's QuizBlockContent has
// no such field, and adding one is out of this stage's scope) — so no
// attempts_remaining field is reported. Gated by RequireEntitlement.
func GetQuiz(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}
		block, content, ok := quizBlockInLesson(c, d, lesson.ID)
		if !ok {
			return
		}

		attemptsMade, err := d.QuizAttempts.CountByLearnerAndBlock(ctx, tx, ac.UserID, block.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		var bestScore gin.H
		if best, err := d.QuizScores.GetBestScore(ctx, tx, ac.UserID, block.ID); err == nil {
			bestScore = gin.H{
				"attempt_number": best.AttemptNumber,
				"percentage":     best.Percentage,
				"passed":         best.Passed,
			}
		} else if !errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"block_id":      block.ID,
			"lesson_id":     lesson.ID,
			"questions":     redactQuizQuestions(content.Questions),
			"attempts_made": attemptsMade,
			"best_score":    bestScore,
		})
	}
}

// quizAnswerRequest is one learner-submitted answer. For mcq/true_false
// questions AnswerIndex identifies the chosen option (matching
// QuizQuestion.Answers' ordering); for short_answer questions AnswerText
// holds the free-text response instead. Exactly one of the two is
// expected to be set, depending on the question's type.
type quizAnswerRequest struct {
	QuestionID  string `json:"question_id" binding:"required"`
	AnswerIndex *int   `json:"answer_index"`
	AnswerText  string `json:"answer_text"`
}

type submitQuizRequest struct {
	Answers []quizAnswerRequest `json:"answers" binding:"required"`
}

type quizQuestionResult struct {
	QuestionID string `json:"question_id"`
	Correct    bool   `json:"correct"`
}

// SubmitQuiz auto-scores a learner's answers server-side (equal-weight,
// no partial credit — grilling-record.md Q2), records the attempt and
// score, and — if the attempt passed — marks the lesson complete the same
// way Stage 3's CompleteLesson does (reusing
// LearnerLessonProgressRepo.MarkComplete directly rather than duplicating
// its upsert logic). Gated by RequireEntitlement.
func SubmitQuiz(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitQuizRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}
		block, content, ok := quizBlockInLesson(c, d, lesson.ID)
		if !ok {
			return
		}
		if len(content.Questions) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "quiz has no questions"})
			return
		}

		submitted := make(map[string]quizAnswerRequest, len(req.Answers))
		for _, a := range req.Answers {
			submitted[a.QuestionID] = a
		}

		results := make([]quizQuestionResult, 0, len(content.Questions))
		correctCount := 0
		for _, q := range content.Questions {
			ans, hasAnswer := submitted[q.ID]
			correct := hasAnswer && questionIsCorrect(q, ans)
			if correct {
				correctCount++
			}
			results = append(results, quizQuestionResult{QuestionID: q.ID, Correct: correct})
		}

		total := len(content.Questions)
		percentage := float64(correctCount) / float64(total) * 100
		passed := percentage >= defaultQuizPassingPercent

		attemptCount, err := d.QuizAttempts.CountByLearnerAndBlock(ctx, tx, ac.UserID, block.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		attemptNumber := attemptCount + 1

		answersJSON, err := json.Marshal(req.Answers)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		if _, err := d.QuizAttempts.Create(ctx, tx, course.OrgID, ac.UserID, block.ID, attemptNumber, answersJSON); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		score, err := d.QuizScores.Create(ctx, tx, course.OrgID, ac.UserID, block.ID, attemptNumber, correctCount, total, percentage, passed)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		if passed {
			if _, err := d.LearnerProgress.MarkComplete(ctx, tx, course.OrgID, ac.UserID, lesson.ID, course.ID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"attempt_number":  attemptNumber,
			"correct_count":   correctCount,
			"total_questions": total,
			"score_earned":    score.ScoreEarned,
			"score_max":       score.ScoreMax,
			"percentage":      score.Percentage,
			"passed":          score.Passed,
			"results":         results,
		})
	}
}

// questionIsCorrect scores a single question against a learner's
// submitted answer. mcq/true_false compare the chosen option's index
// against CorrectAnswerIndex; short_answer case-insensitively,
// whitespace-trimmed matches AnswerText against any AcceptedAnswers
// entry. Any other/unrecognized question type is never counted correct.
func questionIsCorrect(q models.QuizQuestion, ans quizAnswerRequest) bool {
	switch q.Type {
	case "mcq", "true_false":
		if ans.AnswerIndex == nil || q.CorrectAnswerIndex == nil {
			return false
		}
		return *ans.AnswerIndex == *q.CorrectAnswerIndex
	case "short_answer":
		given := strings.TrimSpace(strings.ToLower(ans.AnswerText))
		if given == "" {
			return false
		}
		for _, accepted := range q.AcceptedAnswers {
			if given == strings.TrimSpace(strings.ToLower(accepted)) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
