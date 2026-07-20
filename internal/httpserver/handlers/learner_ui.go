// Package handlers' learner_ui.go implements Task 5 Stage 8's lightweight
// HTMX learner-facing HTML pages: course landing, lesson player, learner
// dashboard, and the teacher grading queue. These are read-mostly,
// server-rendered pages (matching course_editor_ui.go's precedent for
// Task 4's authoring UI) that call into the SAME JSON API handlers from
// Stages 3-7 for every mutation — via small inline fetch() scripts, since
// those endpoints expect/return JSON, which plain HTMX form-encoded
// requests don't produce natively (see main-plan.md Stage 8 and
// grilling-record.md Q10 for the "lightweight, not a JS-heavy build"
// scoping decision). No handler business logic from Stages 3-7 is
// duplicated or modified here.
package handlers

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/auth"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
	"growth-lms/internal/models"
)

// ---- shared view types ----

type learnCourseView struct {
	ID          string
	Title       string
	Description string
	Status      string
}

type learnLessonView struct {
	ID        string
	Title     string
	Completed bool
}

type learnChapterView struct {
	ID      string
	Title   string
	Lessons []learnLessonView
}

// hasStaffAccess reports whether the caller resolved by ResolveCourseOrg
// is an owner/teacher/platform-owner of the course's org — the same
// "always allowed regardless of enrollment" bypass RequireEntitlement
// grants JSON API routes, mirrored here so staff previewing their own
// course never see an "Enroll" button.
func hasStaffAccess(c *gin.Context) bool {
	oc, ok := middleware.OrgContextFromGin(c)
	if !ok {
		return false
	}
	return oc.IsPlatformOwner || oc.Role == auth.RoleOwner || oc.Role == auth.RoleTeacher
}

// CourseLearnPage renders GET /courses/:courseId/learn: title/description,
// the chapter/lesson list with per-lesson completion, a progress bar, and
// either a Resume/Start-Course button (already enrolled, or staff) or an
// Enroll button (not yet enrolled) that POSTs to the existing
// POST /api/courses/:courseId/enroll JSON endpoint via a small inline
// fetch() script. Gated only by ResolveCourseOrg (authentication + org
// membership) — deliberately NOT RequireEntitlement, since a non-enrolled
// member must still be able to load this page to see the Enroll button.
func CourseLearnPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		staff := hasStaffAccess(c)
		enrolled := false
		if !staff {
			if _, err := d.LearnerCourseAccess.Get(ctx, tx, ac.UserID, course.ID); err == nil {
				enrolled = true
			} else if !errors.Is(err, models.ErrNotFound) {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
		}
		hasAccess := staff || enrolled

		var chapterViews []learnChapterView
		var completedCount, totalCount int
		var percentage float64
		var startLessonID string

		if hasAccess {
			chapters, lessonsByChapter, completed, err := loadCourseStructureWithProgress(c, d, course.ID, ac.UserID)
			if err != nil {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			chapterViews = make([]learnChapterView, len(chapters))
			for i, ch := range chapters {
				lessons := lessonsByChapter[ch.ID]
				lviews := make([]learnLessonView, len(lessons))
				for j, lsn := range lessons {
					lviews[j] = learnLessonView{ID: lsn.ID, Title: lsn.Title, Completed: completed[lsn.ID]}
					totalCount++
					if completed[lsn.ID] {
						completedCount++
					}
				}
				chapterViews[i] = learnChapterView{ID: ch.ID, Title: ch.Title, Lessons: lviews}
			}
			if totalCount > 0 {
				percentage = float64(completedCount) / float64(totalCount) * 100
			}

			if resume, err := d.ResumePositions.Get(ctx, tx, ac.UserID, course.ID); err == nil {
				startLessonID = resume.CurrentLessonID
			} else if !errors.Is(err, models.ErrNotFound) {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			if startLessonID == "" {
				for _, ch := range chapterViews {
					if len(ch.Lessons) > 0 {
						startLessonID = ch.Lessons[0].ID
						break
					}
				}
			}
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.CourseLearn.Execute(c.Writer, gin.H{
			"Course":         learnCourseView{ID: course.ID, Title: course.Title, Description: course.Description, Status: course.Status},
			"HasAccess":      hasAccess,
			"Enrolled":       enrolled,
			"Chapters":       chapterViews,
			"CompletedCount": completedCount,
			"TotalCount":     totalCount,
			"Percentage":     percentage,
			"StartLessonID":  startLessonID,
		})
	}
}

// learnBlockView is the lesson-player's per-type rendering shape — a
// deliberately separate, HTML-page-only struct from the JSON API's
// preview/quiz/assignment response shapes (never reuses
// models.QuizQuestion directly for the same answer-key-redaction reason
// quiz.go's redactedQuizQuestion exists).
type learnBlockView struct {
	ID   string
	Type string
	// TextHTML is template.HTML, not string: the content was already
	// sanitized through internal/sanitize's bluemonday allowlist at
	// authoring time (Task 4's AutosaveBlock/CreateBlock), so it's safe
	// to render unescaped here — the same trust boundary
	// course_editor.html's own text-block textarea relies on.
	TextHTML               template.HTML
	ImageURL               string
	AltText                string
	VideoURL               string
	FileURL                string
	Filename               string
	Questions              []redactedQuizQuestion
	AssignmentInstructions string
}

// signedURLFromEnriched extracts the "preview_url" field
// enrichBlockContentWithSignedURL (preview.go) attaches to a media
// block's decoded content map, reusing that exact helper rather than
// duplicating Bunny/Supabase signed-URL logic here.
func signedURLFromEnriched(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	url, _ := m["preview_url"].(string)
	return url
}

// LessonPlayerPage renders GET /courses/:courseId/learn/lessons/:lessonId:
// the lesson's blocks in order (text as sanitized HTML, image/file as a
// signed-URL link, video as a <video> element wired to periodically POST
// progress, quiz as the redacted question list with a submit form,
// assignment as the instructions plus a file-upload form), Previous/Next
// navigation, and a progress indicator. Gated by RequireEntitlement (see
// server.go).
//
// Opening this page updates the learner's resume pointer, mirroring
// POST .../resume's side effect — called directly here (server-side)
// rather than requiring a separate client request, since this handler
// already has everything ResumeLesson needs.
//
// Non-interactive lessons (no video/quiz/assignment block — i.e. only
// text/image/file) are marked complete on render via a tiny inline
// fetch() to the existing POST .../complete endpoint, matching
// CompleteLesson's own doc comment ("the player calls this the moment it
// renders such a lesson").
func LessonPlayerPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}

		chapters, err := d.Chapters.ListByCourse(ctx, tx, course.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}
		var orderedLessonIDs []string
		for _, ch := range chapters {
			lessons, err := d.Lessons.ListByChapter(ctx, tx, ch.ID)
			if err != nil {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			for _, lsn := range lessons {
				orderedLessonIDs = append(orderedLessonIDs, lsn.ID)
			}
		}
		var prevLessonID, nextLessonID string
		for i, id := range orderedLessonIDs {
			if id == lesson.ID {
				if i > 0 {
					prevLessonID = orderedLessonIDs[i-1]
				}
				if i < len(orderedLessonIDs)-1 {
					nextLessonID = orderedLessonIDs[i+1]
				}
				break
			}
		}

		if _, err := d.ResumePositions.Upsert(ctx, tx, course.OrgID, ac.UserID, course.ID, lesson.ID); err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		blocks, err := d.Blocks.ListByLesson(ctx, tx, lesson.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		autoComplete := true
		blockViews := make([]learnBlockView, len(blocks))
		for i, b := range blocks {
			bv := learnBlockView{ID: b.ID, Type: b.Type}
			switch b.Type {
			case models.BlockTypeText:
				var content models.TextBlockContent
				_ = json.Unmarshal(b.Content, &content)
				bv.TextHTML = template.HTML(content.HTML) //nolint:gosec // sanitized at authoring time, see field doc comment
			case models.BlockTypeImage:
				var content models.ImageBlockContent
				_ = json.Unmarshal(b.Content, &content)
				bv.ImageURL = signedURLFromEnriched(d.enrichBlockContentWithSignedURL(ctx, tx, map[string]any{"asset_id": content.AssetID}))
				bv.AltText = content.AltText
			case models.BlockTypeVideo:
				autoComplete = false
				var content models.VideoBlockContent
				_ = json.Unmarshal(b.Content, &content)
				bv.VideoURL = signedURLFromEnriched(d.enrichBlockContentWithSignedURL(ctx, tx, map[string]any{"asset_id": content.AssetID}))
			case models.BlockTypeFile:
				var content models.FileBlockContent
				_ = json.Unmarshal(b.Content, &content)
				bv.FileURL = signedURLFromEnriched(d.enrichBlockContentWithSignedURL(ctx, tx, map[string]any{"asset_id": content.AssetID}))
				bv.Filename = content.Filename
			case models.BlockTypeQuiz:
				autoComplete = false
				var content models.QuizBlockContent
				_ = json.Unmarshal(b.Content, &content)
				bv.Questions = redactQuizQuestions(content.Questions)
			case models.BlockTypeAssignment:
				autoComplete = false
				var content models.AssignmentBlockContent
				_ = json.Unmarshal(b.Content, &content)
				bv.AssignmentInstructions = content.Instructions
			}
			blockViews[i] = bv
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.LessonPlayer.Execute(c.Writer, gin.H{
			"Course":       learnCourseView{ID: course.ID, Title: course.Title},
			"Lesson":       learnLessonView{ID: lesson.ID, Title: lesson.Title},
			"Blocks":       blockViews,
			"PrevLessonID": prevLessonID,
			"NextLessonID": nextLessonID,
			"AutoComplete": autoComplete,
		})
	}
}

type dashboardCourseView struct {
	CourseID       string
	Title          string
	CompletedCount int
	TotalCount     int
	Percentage     float64
	AccessStatus   string
}

type dashboardCertificateView struct {
	CertificateID string
	CourseTitle   string
	IssuedAt      time.Time
	VerifyURL     string
	DownloadURL   string
}

// LearnerDashboardPage renders GET /dashboard: a continue-learning list
// (every course the caller holds an access row for, with progress),
// certificates earned (with verify/download links), and assignment
// submissions still awaiting grading — assembled server-side from the
// same repos the JSON API uses (ListCertificates, ListPendingByLearner,
// loadCourseStructureWithProgress), not client-side HTMX fetches, per
// this stage's scope. Needs only authentication, matching
// ListCertificates' own precedent (no course in the path to resolve org
// context from — every table queried here is scoped by
// `learner_id = app_current_user_id()` at the RLS layer, not by org
// membership, so no ResolveCourseOrg/ResolveOrg call is required).
func LearnerDashboardPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		accessRows, err := d.LearnerCourseAccess.ListByLearner(ctx, tx, ac.UserID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}
		courseViews := make([]dashboardCourseView, 0, len(accessRows))
		for _, a := range accessRows {
			course, cErr := d.Courses.Get(ctx, tx, a.CourseID)
			if cErr != nil {
				// The course may have since been deleted; skip it rather than
				// fail the whole dashboard for one stale access row.
				continue
			}
			_, lessonsByChapter, completed, pErr := loadCourseStructureWithProgress(c, d, course.ID, ac.UserID)
			if pErr != nil {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			total, doneCount := 0, 0
			for _, lessons := range lessonsByChapter {
				for _, lsn := range lessons {
					total++
					if completed[lsn.ID] {
						doneCount++
					}
				}
			}
			var pct float64
			if total > 0 {
				pct = float64(doneCount) / float64(total) * 100
			}
			courseViews = append(courseViews, dashboardCourseView{
				CourseID: course.ID, Title: course.Title,
				CompletedCount: doneCount, TotalCount: total, Percentage: pct,
				AccessStatus: a.AccessStatus,
			})
		}

		certs, err := d.Certificates.ListByLearner(ctx, tx, ac.UserID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}
		certViews := make([]dashboardCertificateView, len(certs))
		for i, cert := range certs {
			title := cert.CourseID
			if course, cErr := d.Courses.Get(ctx, tx, cert.CourseID); cErr == nil {
				title = course.Title
			}
			downloadURL, _ := d.Storage.CreateSignedURL(ctx, d.Config.Supabase.StorageBucket, cert.PDFStoragePath, time.Hour)
			certViews[i] = dashboardCertificateView{
				CertificateID: cert.CertificateID, CourseTitle: title, IssuedAt: cert.IssuedAt,
				VerifyURL: "/certificates/verify/" + cert.CertificateID, DownloadURL: downloadURL,
			}
		}

		pending, err := d.AssignmentSubmissions.ListPendingByLearner(ctx, tx, ac.UserID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Dashboard.Execute(c.Writer, gin.H{
			"Email":              ac.Email,
			"Courses":            courseViews,
			"Certificates":       certViews,
			"PendingSubmissions": pending,
		})
	}
}

type submissionGradeView struct {
	ID               string
	LearnerEmail     string
	LessonTitle      string
	SubmissionNumber int
	DownloadURL      string
	SubmittedAt      time.Time
	DueDateStatus    string
}

// CourseSubmissionsPage renders GET /courses/:courseId/submissions: the
// teacher grading queue, one grade+feedback form per pending submission,
// each POSTing to the existing POST /api/courses/:courseId/submissions/
// :submissionId/grade JSON endpoint via a small inline fetch() script.
// Gated by the authoring role (owner/teacher), same as its JSON sibling
// ListCourseSubmissions (see server.go).
func CourseSubmissionsPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		submissions, err := d.AssignmentSubmissions.ListPendingByCourse(ctx, tx, course.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		views := make([]submissionGradeView, len(submissions))
		for i, s := range submissions {
			learnerEmail := s.LearnerID
			if learner, lErr := d.Profiles.GetByID(ctx, tx, s.LearnerID); lErr == nil {
				learnerEmail = learner.Email
			}
			lessonTitle := ""
			if block, bErr := d.Blocks.Get(ctx, tx, s.AssignmentBlockID); bErr == nil {
				if lesson, lsErr := d.Lessons.Get(ctx, tx, block.LessonID); lsErr == nil {
					lessonTitle = lesson.Title
				}
			}
			downloadURL, _ := d.Storage.CreateSignedURL(ctx, d.Config.Supabase.StorageBucket, s.FilePath, time.Hour)
			views[i] = submissionGradeView{
				ID: s.ID, LearnerEmail: learnerEmail, LessonTitle: lessonTitle,
				SubmissionNumber: s.SubmissionNumber, DownloadURL: downloadURL,
				SubmittedAt: s.SubmittedAt, DueDateStatus: s.DueDateStatus,
			}
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Submissions.Execute(c.Writer, gin.H{
			"Course":      learnCourseView{ID: course.ID, Title: course.Title},
			"Submissions": views,
		})
	}
}
