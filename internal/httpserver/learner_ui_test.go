package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/testutil"
)

// TestLearnerUI_CourseLearnPageAndPlayer exercises Task 5 Stage 8's
// lightweight HTML pages over real HTTP: the course-landing page shows an
// Enroll button before enrollment and a Resume/chapter list after, the
// lesson-player page renders the lesson's content and is entitlement-gated
// (403 before enrollment), and the learner dashboard lists the enrolled
// course. These are plain GET assertions on status code + key strings, not
// a browser-driven test, matching course_editor_ui_test.go's precedent.
func TestLearnerUI_CourseLearnPageAndPlayer(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-learnui-"+ownerID+"@example.com")
	slug := "learn-ui-" + uuid.NewString()
	ownerToken := mintToken(t, ownerID, "owner-learnui@example.com")
	createTestOrg(t, engine, ownerToken, "Learn UI Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", ownerToken, map[string]string{"org_slug": slug, "title": "Learn UI Course"})
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

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters/"+chapterID+"/lessons/"+lessonID+"/blocks", ownerToken, map[string]any{"type": "text", "content": map[string]string{"html": "<p>Hello learner</p>"}})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/transition", ownerToken, map[string]string{"status": "review"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/publish", ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	learnerID := uuid.NewString()
	seedAuthUser(t, dbPool, learnerID, "learner-learnui-"+learnerID+"@example.com")
	seedMembership(t, dbPool, learnerID, slug, "learner")
	learnerToken := mintToken(t, learnerID, "learner-learnui@example.com")

	// Before enrollment: the landing page renders with an Enroll button,
	// not chapter/lesson content.
	rec = getHTML(t, engine, "/courses/"+courseID+"/learn", learnerToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Learn UI Course")
	require.Contains(t, rec.Body.String(), "Enroll")
	require.NotContains(t, rec.Body.String(), "Resume Learning")

	// The lesson player must 403 for a non-entitled learner.
	rec = getHTML(t, engine, "/courses/"+courseID+"/learn/lessons/"+lessonID, learnerToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	// Enroll via the existing JSON API (the page's own Enroll button calls
	// this same endpoint via fetch()).
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/enroll", learnerToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// After enrollment: a Resume/Start button and the chapter/lesson list
	// appear (an enrolled-but-not-yet-started learner sees "Resume
	// Learning", same as the JSON player endpoint's own resume-or-first-
	// lesson fallback).
	rec = getHTML(t, engine, "/courses/"+courseID+"/learn", learnerToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Resume Learning")
	require.Contains(t, rec.Body.String(), "Chapter 1")
	require.Contains(t, rec.Body.String(), "Lesson 1")

	// The lesson player now renders for the entitled learner.
	rec = getHTML(t, engine, "/courses/"+courseID+"/learn/lessons/"+lessonID, learnerToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Lesson 1")
	require.Contains(t, rec.Body.String(), "/courses/"+courseID+"/lessons/"+lessonID+"/complete")

	// The dashboard lists the enrolled course.
	rec = getHTML(t, engine, "/dashboard", learnerToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Learn UI Course")

	// The teacher grading page is authoring-gated: the owner can load it
	// (empty queue, no submissions yet), the learner cannot.
	rec = getHTML(t, engine, "/courses/"+courseID+"/submissions", ownerToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Learn UI Course")

	rec = getHTML(t, engine, "/courses/"+courseID+"/submissions", learnerToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	// Completing the lesson (the only one, no explicit completion rule ->
	// defaults to all_lessons) auto-issues a certificate; the public
	// verification page renders it as HTML when the caller asks for
	// text/html, and stays JSON otherwise (unchanged Stage 6 behavior,
	// exercised by TestCertificateFlow_EndToEnd already).
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/lessons/"+lessonID+"/complete", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/certificate", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var cert map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cert))
	certificateID, _ := cert["certificate_id"].(string)
	require.NotEmpty(t, certificateID)

	rec = getHTML(t, engine, "/certificates/verify/"+certificateID, "", "text/html")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Certificate Verified")
	require.Contains(t, rec.Body.String(), "Learn UI Course")

	rec = getHTML(t, engine, "/certificates/verify/"+uuid.NewString(), "", "text/html")
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Certificate Not Found")
}

// getHTML issues a GET with an optional bearer token and Accept header —
// separate from doJSON since these routes are read-only HTML pages, not
// JSON endpoints, and certificate-verification's content negotiation
// specifically needs control over the Accept header doJSON never sets.
func getHTML(t *testing.T, engine *gin.Engine, path, token, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
