package httpserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/testutil"
)

// TestCertificateFlow_EndToEnd exercises Task 5 Stage 6 over real HTTP
// against real Postgres and real (local) Supabase Storage: a learner
// completing the only lesson in a course triggers automatic certificate
// issuance (no completion rules defined -> defaults to all_lessons), the
// certificate appears in both the course-scoped and cross-course learner
// endpoints with a working download URL, and the certificate can be
// verified through the PUBLIC, unauthenticated endpoint with no bearer
// token at all — proving that route needs no session/RLS context to work.
func TestCertificateFlow_EndToEnd(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t)
	engine, dbPool := newTestEngine(t, adminURL)

	ownerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-cert-"+ownerID+"@example.com")
	slug := "cert-flow-" + uuid.NewString()
	ownerToken := mintToken(t, ownerID, "owner-cert@example.com")
	createTestOrg(t, engine, ownerToken, "Cert Org", slug)

	rec := doJSON(t, engine, http.MethodPost, "/api/courses", ownerToken, map[string]string{"org_slug": slug, "title": "Cert Course"})
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

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/transition", ownerToken, map[string]string{"status": "review"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/publish", ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	learnerID := uuid.NewString()
	seedAuthUser(t, dbPool, learnerID, "learner-cert-"+learnerID+"@example.com")
	seedMembership(t, dbPool, learnerID, slug, "learner")
	learnerToken := mintToken(t, learnerID, "learner-cert@example.com")

	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/enroll", learnerToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// No certificate before the (only) lesson is completed.
	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/certificate", learnerToken, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	// Completing the lesson satisfies the default all_lessons rule (no
	// explicit course_completion_rules row exists for this course) and
	// must auto-issue a certificate.
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/lessons/"+lessonID+"/complete", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/certificate", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var cert map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cert))
	certificateID, _ := cert["certificate_id"].(string)
	require.NotEmpty(t, certificateID)
	require.NotEmpty(t, cert["download_url"])

	// Re-completing the same lesson must not issue a second certificate
	// (idempotent): certificate_id stays the same.
	rec = doJSON(t, engine, http.MethodPost, "/api/courses/"+courseID+"/lessons/"+lessonID+"/complete", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doJSON(t, engine, http.MethodGet, "/api/courses/"+courseID+"/certificate", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var certAgain map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &certAgain))
	require.Equal(t, certificateID, certAgain["certificate_id"])

	// Cross-course listing endpoint (GET /api/certificates) also sees it.
	rec = doJSON(t, engine, http.MethodGet, "/api/certificates", learnerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listing map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listing))
	certs, ok := listing["certificates"].([]any)
	require.True(t, ok)
	require.Len(t, certs, 1)
	require.Equal(t, certificateID, certs[0].(map[string]any)["certificate_id"])

	// PUBLIC verification: no Authorization header at all.
	rec = doJSON(t, engine, http.MethodGet, "/certificates/verify/"+certificateID, "", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var verify map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &verify))
	require.Equal(t, "Cert Course", verify["course_title"])
	require.NotEmpty(t, verify["issued_at"])
	// Only these three fields — no certificate row internals, no org_id,
	// no pdf_storage_path.
	require.ElementsMatch(t, []string{"learner_name", "course_title", "issued_at"}, mapKeys(verify))

	// A random/unknown certificate_id must 404, not error.
	rec = doJSON(t, engine, http.MethodGet, "/certificates/verify/"+uuid.NewString(), "", nil)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
