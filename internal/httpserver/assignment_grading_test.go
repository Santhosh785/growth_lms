package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/testutil"
)

// TestAssignmentGradingFlow_EnqueuesNotificationWithoutBlocking exercises
// Task 5 Stage 7's second hook point end-to-end: teacher uploads an
// assignment block, a learner submits, the teacher grades it. newTestEngine
// deliberately points Redis at an unreachable address (see its own
// comment), so this test also proves — by the grading request still
// succeeding — that GradeSubmission's notification enqueue call is
// best-effort: a failed enqueue is logged, not propagated as a request
// failure. (internal/httpserver/handlers/notifications_wiring_test.go
// separately proves, by static source inspection, that the handler never
// calls the Resend client directly.)
func TestAssignmentGradingFlow_EnqueuesNotificationWithoutBlocking(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-grade-"+ownerID+"@example.com")
	slug := "grade-flow-" + uuid.NewString()
	ownerToken := mintToken(t, ownerID, "owner-grade@example.com")
	createTestOrg(t, engine, ownerToken, "Grade Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", ownerToken, map[string]string{"org_slug": slug, "title": "Grade Course"})
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

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters/"+chapterID+"/lessons/"+lessonID+"/blocks", ownerToken, map[string]any{
		"type": "assignment",
		"content": map[string]any{
			"instructions":       "Submit your work",
			"allow_resubmission": false,
		},
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
	seedAuthUser(t, dbPool, learnerID, "learner-grade-"+learnerID+"@example.com")
	seedMembership(t, dbPool, learnerID, slug, "learner")
	learnerToken := mintToken(t, learnerID, "learner-grade@example.com")

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/enroll", learnerToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	uploadPath := "/api/courses/" + courseID + "/lessons/" + lessonID + "/blocks/" + blockID + "/assignment/upload"
	rec = doJSON(t, engine, http.MethodPost, uploadPath, learnerToken, map[string]string{"filename": "homework.pdf"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var upload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &upload))
	storageKey := upload["storage_key"].(string)
	uploadURL := upload["upload_url"].(string)

	// Actually PUT the file bytes to the signed upload URL against real
	// (local) Supabase Storage, so SubmitAssignment's server-side
	// HeadObject existence check finds something real, matching Task 4's
	// "never trust client-reported completion alone" precedent.
	putReq, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader([]byte("homework contents")))
	require.NoError(t, err)
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putResp, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	putResp.Body.Close()
	require.Less(t, putResp.StatusCode, 300, "signed upload PUT must succeed")

	submitPath := "/api/courses/" + courseID + "/lessons/" + lessonID + "/blocks/" + blockID + "/assignment/submit"
	rec = doJSON(t, engine, http.MethodPost, submitPath, learnerToken, map[string]string{"storage_key": storageKey})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var submission map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &submission))
	submissionID := submission["id"].(string)

	// Grading must succeed (and return the grade in its response) even
	// though newTestEngine's Redis is unreachable — the notification
	// enqueue failure is logged, not surfaced as a request failure.
	gradePath := "/api/courses/" + courseID + "/submissions/" + submissionID + "/grade"
	rec = doJSON(t, engine, http.MethodPost, gradePath, ownerToken, map[string]any{
		"grade_percentage": 92.5,
		"feedback_text":    "Well done",
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var graded map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &graded))
	gradeInfo, ok := graded["grade"].(map[string]any)
	require.True(t, ok, "response must include the grade")
	require.Equal(t, 92.5, gradeInfo["grade_percentage"])
	require.Equal(t, "Well done", gradeInfo["feedback_text"])
}
