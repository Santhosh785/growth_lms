package httpserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/testutil"
)

// TestQuizTaking_RedactsAnswerKey is the single most safety-critical test
// in Stage 4: it proves that a learner taking a quiz never receives the
// answer key. It exercises the full flow over real HTTP against real
// Postgres — teacher authors a quiz block with correct_answer_index and
// accepted_answers, publishes the course, a learner enrolls, then GETs
// the take-quiz endpoint — and asserts the raw JSON response body does
// NOT contain "correct_answer_index" or "accepted_answers" anywhere, not
// just that a typed struct happens to omit them.
func TestQuizTaking_RedactsAnswerKey(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-quiz-"+ownerID+"@example.com")
	slug := "quiz-flow-" + uuid.NewString()
	ownerToken := mintToken(t, ownerID, "owner-quiz@example.com")
	createTestOrg(t, engine, ownerToken, "Quiz Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", ownerToken, map[string]string{"org_slug": slug, "title": "Quiz Course"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var course map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &course))
	courseID := course["id"].(string)

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters", ownerToken, map[string]string{"title": "Chapter 1"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var chapter map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &chapter))
	chapterID := chapter["id"].(string)

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters/"+chapterID+"/lessons", ownerToken, map[string]string{"title": "Lesson 1"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var lesson map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &lesson))
	lessonID := lesson["id"].(string)

	quizContent := map[string]any{
		"questions": []map[string]any{
			{
				"id":                   "q1",
				"type":                 "mcq",
				"question":             "What is 2+2?",
				"answers":              []string{"3", "4", "5"},
				"correct_answer_index": 1,
			},
			{
				"id":               "q2",
				"type":             "short_answer",
				"question":         "Name a primary color.",
				"accepted_answers": []string{"red", "blue", "yellow"},
			},
		},
	}
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters/"+chapterID+"/lessons/"+lessonID+"/blocks", ownerToken, map[string]any{
		"type":    "quiz",
		"content": quizContent,
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var block map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &block))
	blockID := block["id"].(string)

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/transition", ownerToken, map[string]string{"status": "review"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/publish", ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	learnerID := uuid.NewString()
	seedAuthUser(t, dbPool, learnerID, "learner-quiz-"+learnerID+"@example.com")
	seedMembership(t, dbPool, learnerID, slug, "learner")
	learnerToken := mintToken(t, learnerID, "learner-quiz@example.com")

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/enroll", learnerToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/lessons/"+lessonID+"/blocks/"+blockID+"/quiz", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := rec.Body.String()
	require.NotContains(t, body, "correct_answer_index", "take-quiz response must never expose the answer key")
	require.NotContains(t, body, "accepted_answers", "take-quiz response must never expose accepted short-answer keys")

	var quiz map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &quiz))
	questions, ok := quiz["questions"].([]any)
	require.True(t, ok, "expected a questions array: %s", body)
	require.Len(t, questions, 2)
	q1 := questions[0].(map[string]any)
	require.Equal(t, "q1", q1["id"])
	require.Equal(t, []any{"3", "4", "5"}, q1["answers"])
	require.Equal(t, float64(0), quiz["attempts_made"])
	require.Nil(t, quiz["best_score"])

	// Submitting a correct mcq answer and an incorrect short_answer gives
	// 50%, below the hardcoded 70% passing threshold — lesson must not be
	// marked complete.
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/lessons/"+lessonID+"/blocks/"+blockID+"/quiz/submit", learnerToken, map[string]any{
		"answers": []map[string]any{
			{"question_id": "q1", "answer_index": 1},
			{"question_id": "q2", "answer_text": "green"},
		},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var result map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Equal(t, float64(1), result["attempt_number"])
	require.Equal(t, float64(1), result["correct_count"])
	require.Equal(t, float64(2), result["total_questions"])
	require.Equal(t, 50.0, result["percentage"])
	require.Equal(t, false, result["passed"])

	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/lessons/"+lessonID+"/blocks/"+blockID+"/quiz", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &quiz))
	require.Equal(t, float64(1), quiz["attempts_made"])
	best := quiz["best_score"].(map[string]any)
	require.Equal(t, 50.0, best["percentage"])
	require.Equal(t, false, best["passed"])

	// A second, fully-correct attempt passes and must mark the lesson
	// complete via the same LearnerLessonProgress path Stage 3 uses.
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/lessons/"+lessonID+"/blocks/"+blockID+"/quiz/submit", learnerToken, map[string]any{
		"answers": []map[string]any{
			{"question_id": "q1", "answer_index": 1},
			{"question_id": "q2", "answer_text": "Red"},
		},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Equal(t, float64(2), result["attempt_number"])
	require.Equal(t, 100.0, result["percentage"])
	require.Equal(t, true, result["passed"])

	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/progress", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var progress map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &progress))
	require.Equal(t, float64(1), progress["completed_lessons"], "quiz pass must mark the lesson complete")
}
