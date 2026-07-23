package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/worker"
)

// Task 7 discussions: org-wide and course-scoped threads with one level of
// replies, reactions, @[uuid] mentions, and reporting. Mentions/replies fan
// out to the notification worker (never emailed synchronously in the request
// path). Every mutation is gated by RLS in the database independently of the
// role checks here.

type createThreadRequest struct {
	Title string `json:"title" binding:"required"`
}

// CreateOrgThread creates an org-wide (course_id NULL) discussion thread.
func CreateOrgThread(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createThreadRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		thread, err := d.Threads.Create(c.Request.Context(), tx, oc.OrgID, nil, req.Title, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, threadResponse(thread))
	}
}

// ListOrgThreads returns the org-wide threads.
func ListOrgThreads(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		threads, err := d.Threads.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"threads": threadList(threads)})
	}
}

// CreateCourseThread creates a course-scoped discussion thread.
func CreateCourseThread(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createThreadRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		thread, err := d.Threads.Create(c.Request.Context(), tx, course.OrgID, &course.ID, req.Title, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, threadResponse(thread))
	}
}

// ListCourseThreads returns a course's discussion threads.
func ListCourseThreads(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		threads, err := d.Threads.ListByCourse(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"threads": threadList(threads)})
	}
}

// GetThread returns a thread with its visible posts and aggregated reactions.
func GetThread(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		thread, _ := middleware.ThreadFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		posts, err := d.Posts.ListByThread(ctx, tx, thread.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		reactions, err := d.Reactions.ListByThread(ctx, tx, thread.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		// Aggregate emoji -> count per post.
		counts := map[string]map[string]int{}
		for _, r := range reactions {
			if counts[r.PostID] == nil {
				counts[r.PostID] = map[string]int{}
			}
			counts[r.PostID][r.Emoji]++
		}

		postsOut := make([]gin.H, len(posts))
		for i, p := range posts {
			postsOut[i] = postResponse(p, counts[p.ID])
		}
		c.JSON(http.StatusOK, gin.H{"thread": threadResponse(thread), "posts": postsOut})
	}
}

type createPostRequest struct {
	Body         string  `json:"body" binding:"required"`
	ParentPostID *string `json:"parent_post_id"`
}

// CreatePost adds a root post or a one-level reply to a thread, then fans out
// mention and reply notifications.
func CreatePost(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createPostRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		thread, _ := middleware.ThreadFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		if thread.IsLocked {
			c.JSON(http.StatusForbidden, gin.H{"error": "thread is locked"})
			return
		}

		// Enforce one level of nesting: a reply's parent must be a root post
		// in this same thread.
		var parentAuthorID string
		if req.ParentPostID != nil {
			parent, err := d.Posts.Get(ctx, tx, *req.ParentPostID)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "parent post not found"})
				return
			}
			if parent.ThreadID != thread.ID || parent.ParentPostID != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "replies may only target a top-level post in this thread"})
				return
			}
			parentAuthorID = parent.AuthorID
		}

		post, err := d.Posts.Create(ctx, tx, oc.OrgID, thread.ID, req.ParentPostID, ac.UserID, req.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if err := d.Threads.Touch(ctx, tx, thread.ID); err != nil {
			slog.Default().Error("handlers: touch thread after post", "error", err, "thread_id", thread.ID)
		}

		actorName := displayName(ctx, d, tx, ac.UserID)
		d.fanOutMentions(ctx, tx, post, thread, oc.OrgID, ac.UserID, actorName)
		if parentAuthorID != "" && parentAuthorID != ac.UserID {
			if err := worker.EnqueueReplyNotification(d.AsyncQueue, worker.NotifyReplyPayload{
				OrgID: oc.OrgID, RecipientID: parentAuthorID, ActorID: ac.UserID, ActorName: actorName,
				ThreadID: thread.ID, ThreadTitle: thread.Title, PostID: post.ID,
				Preview: previewOf(req.Body), LinkPath: threadLinkPath(thread.ID),
			}); err != nil {
				slog.Default().Error("handlers: enqueue reply notification", "error", err, "post_id", post.ID)
			}
		}

		c.JSON(http.StatusCreated, postResponse(post, nil))
	}
}

// fanOutMentions validates @[uuid] tokens against org membership and enqueues
// one mention notification per real, non-self member mentioned.
func (d *AuthDeps) fanOutMentions(ctx context.Context, tx models.Querier, post *models.DiscussionPost, thread *models.DiscussionThread, orgID, actorID, actorName string) {
	ids := models.ParseMentionTokens(post.Body)
	var valid []string
	for _, uid := range ids {
		if uid == actorID {
			continue
		}
		if _, err := d.Memberships.GetRole(ctx, tx, uid, orgID); err != nil {
			continue // not a member (or not visible) — skip
		}
		valid = append(valid, uid)
	}
	if len(valid) == 0 {
		return
	}
	if err := d.Mentions.AddMany(ctx, tx, post.ID, orgID, valid); err != nil {
		slog.Default().Error("handlers: persist mentions", "error", err, "post_id", post.ID)
	}
	for _, uid := range valid {
		if err := worker.EnqueueMentionNotification(d.AsyncQueue, worker.NotifyMentionPayload{
			OrgID: orgID, RecipientID: uid, ActorID: actorID, ActorName: actorName,
			ThreadID: thread.ID, ThreadTitle: thread.Title, PostID: post.ID,
			Preview: previewOf(post.Body), LinkPath: threadLinkPath(thread.ID),
		}); err != nil {
			slog.Default().Error("handlers: enqueue mention notification", "error", err, "post_id", post.ID)
		}
	}
}

type reactionRequest struct {
	Emoji string `json:"emoji" binding:"required"`
}

// ReactToPost adds the caller's reaction to a post.
func ReactToPost(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req reactionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		post, _ := middleware.PostFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		if err := d.Reactions.Add(c.Request.Context(), tx, post.ID, post.OrgID, ac.UserID, req.Emoji); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// Unreact removes the caller's reaction (emoji from ?emoji= query).
func Unreact(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		emoji := c.Query("emoji")
		if emoji == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "emoji required"})
			return
		}
		post, _ := middleware.PostFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		if err := d.Reactions.Remove(c.Request.Context(), tx, post.ID, ac.UserID, emoji); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

type reportRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// ReportPost files a content report and notifies the org's moderators.
func ReportPost(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req reportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		post, _ := middleware.PostFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		report, err := d.Reports.Create(ctx, tx, post.OrgID, post.ID, ac.UserID, req.Reason)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if err := worker.EnqueueReportFiledNotification(d.AsyncQueue, worker.NotifyReportFiledPayload{
			OrgID: post.OrgID, PostID: post.ID, ReportID: report.ID, Reason: req.Reason,
			LinkPath: threadLinkPath(post.ThreadID),
		}); err != nil {
			slog.Default().Error("handlers: enqueue report notification", "error", err, "report_id", report.ID)
		}
		c.JSON(http.StatusCreated, gin.H{"status": "reported"})
	}
}

// ThreadMembers lists the thread org's members for the @-mention picker.
func ThreadMembers(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		thread, _ := middleware.ThreadFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		members, err := d.Memberships.ListByOrg(c.Request.Context(), tx, thread.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(members))
		for i, m := range members {
			name := m.Email
			if m.FullName != nil && *m.FullName != "" {
				name = *m.FullName
			}
			out[i] = gin.H{"user_id": m.UserID, "name": name}
		}
		c.JSON(http.StatusOK, gin.H{"members": out})
	}
}

// --- helpers -------------------------------------------------------------

func threadLinkPath(threadID string) string { return "/community/threads/" + threadID }

// previewOf trims a post body to a short single-line snippet for notification
// previews, stripping @[uuid] mention tokens.
func previewOf(body string) string {
	s := models.StripMentionTokens(body)
	s = strings.Join(strings.Fields(s), " ")
	const max = 140
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func displayName(ctx context.Context, d *AuthDeps, tx models.Querier, userID string) string {
	profile, err := d.Profiles.GetByID(ctx, tx, userID)
	if err != nil {
		return "Someone"
	}
	if profile.FullName != nil && *profile.FullName != "" {
		return *profile.FullName
	}
	return profile.Email
}

func threadResponse(t *models.DiscussionThread) gin.H {
	return gin.H{
		"id": t.ID, "org_id": t.OrgID, "course_id": t.CourseID,
		"title": t.Title, "created_by": t.CreatedBy,
		"is_pinned": t.IsPinned, "is_locked": t.IsLocked,
		"status": t.Status, "created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
	}
}

func threadList(threads []*models.DiscussionThread) []gin.H {
	out := make([]gin.H, len(threads))
	for i, t := range threads {
		out[i] = threadResponse(t)
	}
	return out
}

func postResponse(p *models.DiscussionPost, reactions map[string]int) gin.H {
	return gin.H{
		"id": p.ID, "thread_id": p.ThreadID, "parent_post_id": p.ParentPostID,
		"author_id": p.AuthorID, "body": p.Body, "status": p.Status,
		"edited_at": p.EditedAt, "created_at": p.CreatedAt, "reactions": reactions,
	}
}

// errNotFoundResponse is a shared 404 for handlers that Get by id directly.
func errNotFoundResponse(c *gin.Context, err error, msg string) bool {
	if errors.Is(err, models.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": msg})
		return true
	}
	return false
}
