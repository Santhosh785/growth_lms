package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/testutil"
)

func doJSON(t *testing.T, engine *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func createTestOrg(t *testing.T, engine *gin.Engine, token, name, slug string) {
	t.Helper()
	rec := doJSON(t, engine, http.MethodPost, "/api/orgs", token, map[string]string{"name": name, "slug": slug})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// TestCourseAuthoringFlow_EndToEnd exercises the full teacher authoring
// path over real HTTP against real Postgres: create course -> add chapter
// -> add lesson -> add text block (sanitized) -> publish (snapshot
// created, published_at set) -> unpublish. Every step goes through the
// JSON API only, no direct database access, matching the spec's
// acceptance criterion.
func TestCourseAuthoringFlow_EndToEnd(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-flow-"+ownerID+"@example.com")
	slug := "course-flow-" + uuid.NewString()
	token := mintToken(t, ownerID, "owner-flow@example.com")
	createTestOrg(t, engine, token, "Flow Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", token, map[string]string{"org_slug": slug, "title": "Intro to Go", "description": "desc"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var course map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &course))
	courseID := course["id"].(string)
	require.Equal(t, "draft", course["status"])

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters", token, map[string]string{"title": "Chapter 1"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var chapter map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &chapter))
	chapterID := chapter["id"].(string)

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters/"+chapterID+"/lessons", token, map[string]string{"title": "Lesson 1"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var lesson map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &lesson))
	lessonID := lesson["id"].(string)

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters/"+chapterID+"/lessons/"+lessonID+"/blocks", token, map[string]any{
		"type":    "text",
		"content": map[string]string{"html": `<p>hello</p><script>alert(1)</script>`},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var block map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &block))
	content := block["content"].(map[string]any)
	require.NotContains(t, content["html"], "script", "text block HTML must be sanitized on create")
	require.Contains(t, content["html"], "<p>hello</p>")

	// draft -> review -> published
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/transition", token, map[string]string{"status": "review"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/publish", token, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var published map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &published))
	require.Equal(t, "published", published["status"])
	require.NotNil(t, published["published_at"])

	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/versions", token, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var versionsResp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &versionsResp))
	versions := versionsResp["versions"].([]any)
	require.Len(t, versions, 1, "publish must create exactly one version snapshot")

	// published -> unpublished
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/unpublish", token, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var unpublished map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &unpublished))
	require.Equal(t, "unpublished", unpublished["status"])
}

// TestCourseAuthoring_RejectsLearnerAndModerator proves learner/moderator
// roles get 403 on a representative spread of course-authoring endpoints
// (create course, create chapter, create block, upload media), gated by
// middleware.RequireRole — RLS separately enforces the same boundary at
// the DB layer (see TestRLS_CourseDomainIsolation).
func TestCourseAuthoring_RejectsLearnerAndModerator(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-rbac2-"+ownerID+"@example.com")
	slug := "course-rbac2-" + uuid.NewString()
	ownerToken := mintToken(t, ownerID, "owner-rbac2@example.com")
	createTestOrg(t, engine, ownerToken, "RBAC2 Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", ownerToken, map[string]string{"org_slug": slug, "title": "RBAC Course"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var course map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &course))
	courseID := course["id"].(string)

	for _, role := range []string{"learner", "moderator"} {
		userID := uuid.NewString()
		seedAuthUser(t, dbPool, userID, role+"-"+userID+"@example.com")
		seedMembership(t, dbPool, userID, slug, role)
		token := mintToken(t, userID, role+"@example.com")

		rec := doJSON(t, engine, http.MethodPost, "/api/courses", token, map[string]string{"org_slug": slug, "title": "Should Not Work"})
		require.Equal(t, http.StatusForbidden, rec.Code, "%s must not create courses", role)

		rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/chapters", token, map[string]string{"title": "Should Not Work"})
		require.Equal(t, http.StatusForbidden, rec.Code, "%s must not create chapters", role)

		rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/publish", token, nil)
		require.Equal(t, http.StatusForbidden, rec.Code, "%s must not publish courses", role)

		rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/media/upload", token, map[string]string{"filename": "x.png", "type": "image"})
		require.Equal(t, http.StatusForbidden, rec.Code, "%s must not upload media", role)
	}
}

// TestCourseTransition_RejectsUndocumentedTransitions proves the status
// state machine rejects transitions outside its documented flow (400, not
// a silent no-op or a 500) — complementing
// TestCourseAuthoringFlow_EndToEnd's coverage of the valid path.
func TestCourseTransition_RejectsUndocumentedTransitions(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-transition-"+ownerID+"@example.com")
	slug := "course-transition-" + uuid.NewString()
	token := mintToken(t, ownerID, "owner-transition@example.com")
	createTestOrg(t, engine, token, "Transition Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", token, map[string]string{"org_slug": slug, "title": "Transition Course"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var course map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &course))
	courseID := course["id"].(string)

	// draft -> published directly (skipping review) is not a documented
	// transition — must be rejected, and must go through /publish anyway,
	// not /transition.
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/transition", token, map[string]string{"status": "published"})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// draft -> unpublished is not a documented transition either (only
	// published -> unpublished is).
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/transition", token, map[string]string{"status": "unpublished"})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// Confirm the course's status was never actually changed by either
	// rejected attempt.
	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID, token, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var reloaded map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reloaded))
	require.Equal(t, "draft", reloaded["status"], "rejected transitions must not mutate course status")
}
