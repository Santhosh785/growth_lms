package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/testutil"
)

// TestResolveCourseOrg_FlatPathRouting exercises ResolveCourseOrg over
// real HTTP against real Postgres: a flat /api/courses/:courseId path (no
// :org_slug segment) still resolves org context correctly, and a
// different org's member gets 404 on someone else's course, not a leaked
// row or a 500.
func TestResolveCourseOrg_FlatPathRouting(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerAID := uuid.NewString()
	ownerBID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerAID, "owner-a-"+ownerAID+"@example.com")
	seedAuthUser(t, dbPool, ownerBID, "owner-b-"+ownerBID+"@example.com")

	slugA := "course-mw-a-" + uuid.NewString()
	slugB := "course-mw-b-" + uuid.NewString()
	tokenA := mintToken(t, ownerAID, "owner-a@example.com")
	tokenB := mintToken(t, ownerBID, "owner-b@example.com")

	createOrg := func(token, name, slug string) {
		body, _ := json.Marshal(map[string]string{"name": name, "slug": slug})
		req := httptest.NewRequest(http.MethodPost, "/api/orgs", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}
	createOrg(tokenA, "Org A", slugA)
	createOrg(tokenB, "Org B", slugB)

	var courseBID string
	require.NoError(t, dbPool.QueryRow(context.Background(), `
		INSERT INTO courses (org_id, title, created_by)
		SELECT id, 'Org B Course', $2 FROM organizations WHERE slug = $1
		RETURNING id
	`, slugB, ownerBID).Scan(&courseBID))

	// Owner A (a different org entirely) must get 404, not a leaked
	// course or a 500, for a course belonging to org B.
	req := httptest.NewRequest(http.MethodGet, "/api/courses/"+courseBID, nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	// Owner B (the actual owner) can fetch their own course via the flat
	// path.
	req = httptest.NewRequest(http.MethodGet, "/api/courses/"+courseBID, nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
