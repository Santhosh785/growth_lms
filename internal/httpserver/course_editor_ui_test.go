package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/testutil"
)

// TestCourseEditorUI_RendersAndEnforcesCSRF exercises the lightweight
// HTMX course-editor page over real HTTP: a teacher can load it (and gets
// a CSRF cookie), a mutating form POST without the matching
// X-CSRF-Token header is rejected, and one with it succeeds and actually
// creates the chapter.
func TestCourseEditorUI_RendersAndEnforcesCSRF(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-editor-"+ownerID+"@example.com")
	slug := "course-editor-" + uuid.NewString()
	token := mintToken(t, ownerID, "owner-editor@example.com")
	createTestOrg(t, engine, token, "Editor Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", token, map[string]string{"org_slug": slug, "title": "Editor Course"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var course map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &course))
	courseID := course["id"].(string)

	// GET the editor page: renders, and issues a CSRF cookie.
	req := httptest.NewRequest(http.MethodGet, "/courses/"+courseID+"/edit", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Editor Course")

	var csrfCookie *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == middleware.CSRFCookieName {
			csrfCookie = ck
		}
	}
	require.NotNil(t, csrfCookie, "expected the editor page to issue a CSRF cookie")

	// POST without the CSRF header must be rejected.
	form := strings.NewReader("title=Chapter+One")
	req = httptest.NewRequest(http.MethodPost, "/courses/"+courseID+"/edit/chapters", form)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code, "expected CSRF rejection without a matching header")

	// POST with the matching header succeeds and actually creates the
	// chapter.
	form = strings.NewReader("title=Chapter+One")
	req = httptest.NewRequest(http.MethodPost, "/courses/"+courseID+"/edit/chapters", form)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	req.Header.Set(middleware.CSRFHeaderName, csrfCookie.Value)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	var chapterCount int
	require.NoError(t, dbPool.QueryRow(context.Background(), `SELECT count(*) FROM chapters WHERE course_id = $1 AND title = 'Chapter One'`, courseID).Scan(&chapterCount))
	require.Equal(t, 1, chapterCount, "expected the chapter to actually be created")
}
