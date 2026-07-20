// Package handlers' course_editor_ui.go implements the lightweight HTMX
// course-editor UI: a functional-but-modest server-rendered page (metadata,
// chapter/lesson lists with up/down move buttons instead of drag-drop,
// per-type block editors, autosave, publish/preview/version-history
// buttons) added after the JSON API was complete and tested, per this
// task's scoping decision (see plans/task-4-implementation/main-plan.md).
//
// These handlers are form-encoded (c.PostForm), NOT JSON — a deliberate
// split from the JSON API handlers in courses.go/chapters.go/etc, which
// this page never calls directly. Every mutating route here sits behind
// middleware.RequireCSRF (see server.go's HTML route group).
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
	"growth-lms/internal/models"
	"growth-lms/internal/sanitize"
)

type courseView struct {
	ID     string
	Title  string
	Status string
}

type blockView struct {
	ID   string
	Type string
	HTML string
}

type lessonView struct {
	ID     string
	Title  string
	Blocks []blockView
}

type chapterView struct {
	ID      string
	Title   string
	IsFirst bool
	IsLast  bool
	Lessons []lessonView
}

// CourseEditorPage renders GET /courses/:id/edit — the full editor page,
// re-rendered wholesale after every mutation (hx-target="body"), which
// keeps this handler's data-assembly logic reusable for every POST below.
func CourseEditorPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		chapters, err := d.Chapters.ListByCourse(ctx, tx, course.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		views := make([]chapterView, len(chapters))
		for i, ch := range chapters {
			lessons, err := d.Lessons.ListByChapter(ctx, tx, ch.ID)
			if err != nil {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			lviews := make([]lessonView, len(lessons))
			for j, lsn := range lessons {
				blocks, err := d.Blocks.ListByLesson(ctx, tx, lsn.ID)
				if err != nil {
					c.String(http.StatusInternalServerError, "internal error")
					return
				}
				bviews := make([]blockView, len(blocks))
				for k, b := range blocks {
					bv := blockView{ID: b.ID, Type: b.Type}
					if b.Type == models.BlockTypeText {
						var text models.TextBlockContent
						_ = json.Unmarshal(b.Content, &text)
						bv.HTML = text.HTML
					}
					bviews[k] = bv
				}
				lviews[j] = lessonView{ID: lsn.ID, Title: lsn.Title, Blocks: bviews}
			}
			views[i] = chapterView{
				ID: ch.ID, Title: ch.Title,
				IsFirst: i == 0, IsLast: i == len(chapters)-1,
				Lessons: lviews,
			}
		}

		versions, err := d.CourseVersions.List(ctx, tx, course.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.CourseEditor.Execute(c.Writer, gin.H{
			"Course":    courseView{ID: course.ID, Title: course.Title, Status: course.Status},
			"Chapters":  views,
			"Versions":  versions,
			"CSRFToken": middleware.CSRFTokenFromGin(c),
		})
	}
}

func redirectToEditor(c *gin.Context, courseID string) {
	c.Redirect(http.StatusSeeOther, "/courses/"+courseID+"/edit")
}

func CourseEditorCreateChapter(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		existing, _ := d.Chapters.ListByCourse(c.Request.Context(), tx, course.ID)
		sortOrders := make([]float64, len(existing))
		for i, ch := range existing {
			sortOrders[i] = ch.SortOrder
		}
		_, _ = d.Chapters.Create(c.Request.Context(), tx, course.ID, course.OrgID, ac.UserID, c.PostForm("title"), models.NextSortOrder(sortOrders))
		redirectToEditor(c, course.ID)
	}
}

func CourseEditorMoveChapter(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		chapters, _ := d.Chapters.ListByCourse(c.Request.Context(), tx, course.ID)

		idx := -1
		for i, ch := range chapters {
			if ch.ID == c.Param("chapterId") {
				idx = i
				break
			}
		}
		if idx < 0 {
			redirectToEditor(c, course.ID)
			return
		}

		swapWith := idx - 1
		if c.PostForm("direction") == "down" {
			swapWith = idx + 1
		}
		if swapWith >= 0 && swapWith < len(chapters) {
			a, b := chapters[idx].SortOrder, chapters[swapWith].SortOrder
			_ = d.Chapters.SetSortOrder(c.Request.Context(), tx, chapters[idx].ID, b)
			_ = d.Chapters.SetSortOrder(c.Request.Context(), tx, chapters[swapWith].ID, a)
		}
		redirectToEditor(c, course.ID)
	}
}

func CourseEditorCreateLesson(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		chapterID := c.Param("chapterId")

		existing, _ := d.Lessons.ListByChapter(c.Request.Context(), tx, chapterID)
		sortOrders := make([]float64, len(existing))
		for i, lsn := range existing {
			sortOrders[i] = lsn.SortOrder
		}
		_, _ = d.Lessons.Create(c.Request.Context(), tx, chapterID, course.ID, course.OrgID, ac.UserID, c.PostForm("title"), models.NextSortOrder(sortOrders))
		redirectToEditor(c, course.ID)
	}
}

func CourseEditorCreateBlock(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		lessonID := c.Param("lessonId")

		blockType := c.PostForm("type")
		if !validBlockTypes[blockType] {
			redirectToEditor(c, course.ID)
			return
		}

		var content json.RawMessage
		switch blockType {
		case models.BlockTypeText:
			content, _ = json.Marshal(models.TextBlockContent{})
		case models.BlockTypeQuiz:
			content, _ = json.Marshal(models.QuizBlockContent{})
		default:
			content = json.RawMessage(`{}`)
		}

		existing, _ := d.Blocks.ListByLesson(c.Request.Context(), tx, lessonID)
		sortOrders := make([]float64, len(existing))
		for i, b := range existing {
			sortOrders[i] = b.SortOrder
		}
		_, _ = d.Blocks.Create(c.Request.Context(), tx, lessonID, course.ID, course.OrgID, ac.UserID, blockType, content, models.NextSortOrder(sortOrders))
		redirectToEditor(c, course.ID)
	}
}

// CourseEditorAutosaveBlock saves a text block's HTML (sanitized, same
// bluemonday allowlist as the JSON API) without a full page redirect —
// hx-swap="none" on the form means this response body is ignored by
// design; only the save itself matters.
func CourseEditorAutosaveBlock(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		content, _ := json.Marshal(models.TextBlockContent{HTML: sanitize.TextBlockHTML(c.PostForm("html"))})
		_, err := d.Blocks.Autosave(c.Request.Context(), tx, c.Param("blockId"), content)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func CourseEditorTransition(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		status := c.PostForm("status")
		if validTransitions[course.Status][status] {
			_, _ = d.Courses.SetStatus(c.Request.Context(), tx, course.ID, status)
		}
		redirectToEditor(c, course.ID)
	}
}

func CourseEditorPublish(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		videoBlocks, _ := d.Blocks.ListVideoBlocksByCourse(ctx, tx, course.ID)
		ready := true
		for _, b := range videoBlocks {
			var content models.VideoBlockContent
			if json.Unmarshal(b.Content, &content) == nil {
				if asset, err := d.Assets.Get(ctx, tx, content.AssetID); err != nil || asset.ProcessingStatus != models.ProcessingStatusReady {
					ready = false
				}
			}
		}
		if ready {
			if _, err := d.Courses.Publish(ctx, tx, course.ID); err == nil {
				_, _ = d.CourseVersions.Snapshot(ctx, tx, course.ID, course.OrgID, ac.UserID)
			}
		}
		redirectToEditor(c, course.ID)
	}
}

func CourseEditorUnpublish(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		_, _ = d.Courses.SetStatus(c.Request.Context(), tx, course.ID, models.CourseStatusUnpublished)
		redirectToEditor(c, course.ID)
	}
}

func CourseEditorRestoreVersion(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		_, _ = d.CourseVersions.Restore(c.Request.Context(), tx, course.ID, c.Param("versionId"), ac.UserID)
		redirectToEditor(c, course.ID)
	}
}
