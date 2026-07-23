package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/models"
)

// Task 7 community action routes are keyed by a thread/post/board id rather
// than an :org_slug or :courseId, so they need their own org-context
// resolvers. Each mirrors ResolveCourseOrg exactly: load the row (visible
// only to org members under RLS, since app.current_user_id is already set at
// request start), resolve the caller's role in the row's org, stamp
// dbctx.SetOrgContext, and store the SAME middleware.OrgContext type so
// RequireRole and OrgContextFromGin work unchanged.

const (
	threadContextKey = "thread_context"
	postContextKey   = "post_context"
	boardContextKey  = "board_context"
)

// ThreadFromGin returns the thread loaded by ResolveThreadOrg.
func ThreadFromGin(c *gin.Context) (*models.DiscussionThread, bool) {
	v, ok := c.Get(threadContextKey)
	if !ok {
		return nil, false
	}
	t, ok := v.(*models.DiscussionThread)
	return t, ok
}

// PostFromGin returns the post loaded by ResolvePostOrg.
func PostFromGin(c *gin.Context) (*models.DiscussionPost, bool) {
	v, ok := c.Get(postContextKey)
	if !ok {
		return nil, false
	}
	p, ok := v.(*models.DiscussionPost)
	return p, ok
}

// BoardFromGin returns the board loaded by ResolveBoardOrg.
func BoardFromGin(c *gin.Context) (*models.CollabBoard, bool) {
	v, ok := c.Get(boardContextKey)
	if !ok {
		return nil, false
	}
	b, ok := v.(*models.CollabBoard)
	return b, ok
}

// ResolveThreadOrg resolves org context from :threadId.
func ResolveThreadOrg(threads *models.DiscussionThreadRepo, memberships *models.MembershipRepo, profiles *models.ProfileRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, ac, ok := requestTxAndAuth(c)
		if !ok {
			return
		}
		thread, err := threads.Get(c.Request.Context(), tx, c.Param("threadId"))
		if err != nil {
			abortResolve(c, err, "thread not found")
			return
		}
		if !stampOrgContext(c, tx, ac, thread.OrgID, memberships, profiles, "thread not found") {
			return
		}
		c.Set(threadContextKey, thread)
		c.Next()
	}
}

// ResolvePostOrg resolves org context from :postId.
func ResolvePostOrg(posts *models.DiscussionPostRepo, memberships *models.MembershipRepo, profiles *models.ProfileRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, ac, ok := requestTxAndAuth(c)
		if !ok {
			return
		}
		post, err := posts.Get(c.Request.Context(), tx, c.Param("postId"))
		if err != nil {
			abortResolve(c, err, "post not found")
			return
		}
		if !stampOrgContext(c, tx, ac, post.OrgID, memberships, profiles, "post not found") {
			return
		}
		c.Set(postContextKey, post)
		c.Next()
	}
}

// ResolveBoardOrg resolves org context from :boardId.
func ResolveBoardOrg(boards *models.CollabBoardRepo, memberships *models.MembershipRepo, profiles *models.ProfileRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, ac, ok := requestTxAndAuth(c)
		if !ok {
			return
		}
		board, err := boards.Get(c.Request.Context(), tx, c.Param("boardId"))
		if err != nil {
			abortResolve(c, err, "board not found")
			return
		}
		if !stampOrgContext(c, tx, ac, board.OrgID, memberships, profiles, "board not found") {
			return
		}
		c.Set(boardContextKey, board)
		c.Next()
	}
}

// requestTxAndAuth pulls the request tx + auth context, aborting on absence.
func requestTxAndAuth(c *gin.Context) (tx pgx.Tx, ac AuthContext, ok bool) {
	rtx, hasTx := RequestTxFromGin(c)
	if !hasTx {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, AuthContext{}, false
	}
	a, hasAuth := AuthContextFromGin(c)
	if !hasAuth {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return nil, AuthContext{}, false
	}
	return rtx, a, true
}

// abortResolve maps a load error to 404 (not found / not visible under RLS)
// or 500.
func abortResolve(c *gin.Context, err error, notFoundMsg string) {
	if errors.Is(err, models.ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": notFoundMsg})
		return
	}
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}

// stampOrgContext resolves the caller's role in orgID and stamps RLS context,
// returning false (and aborting) if the caller may not access the org.
func stampOrgContext(c *gin.Context, tx pgx.Tx, ac AuthContext, orgID string, memberships *models.MembershipRepo, profiles *models.ProfileRepo, forbidMsg string) bool {
	ctx := c.Request.Context()
	role, err := memberships.GetRole(ctx, tx, ac.UserID, orgID)
	isPlatformOwner := false
	if err != nil {
		if !errors.Is(err, models.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return false
		}
		profile, perr := profiles.GetByID(ctx, tx, ac.UserID)
		if perr != nil || !profile.IsPlatformOwner {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": forbidMsg})
			return false
		}
		isPlatformOwner = true
		role = ""
	}
	if err := dbctx.SetOrgContext(ctx, tx, orgID, role); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}
	c.Set(orgContextKey, OrgContext{OrgID: orgID, Role: role, IsPlatformOwner: isPlatformOwner})
	return true
}
