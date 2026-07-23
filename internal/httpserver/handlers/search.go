package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Search is GET /api/orgs/:org_slug/search?q=... — cross-entity search
// across courses, lessons, discussions, and org members (plan.md Task
// 8's "search across courses, lessons, users, and discussions"). Any org
// member can search; each sub-query is scoped to the org either by RLS
// (courses/lessons/discussion_threads) or by the search_org_members()
// SQL function's own is_org_member() check.
func Search(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			c.JSON(http.StatusOK, gin.H{"results": []any{}})
			return
		}

		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		courses, err := d.Search.Courses(ctx, tx, oc.OrgID, query)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		lessons, err := d.Search.Lessons(ctx, tx, oc.OrgID, query)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		discussions, err := d.Search.Discussions(ctx, tx, oc.OrgID, query)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		members, err := d.Search.Members(ctx, tx, oc.OrgID, query)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		results := append(append(append(courses, lessons...), discussions...), members...)

		_ = d.AnalyticsEvents.Record(ctx, tx, oc.OrgID, models.EventSearch, ac.UserID, "", nil)

		c.JSON(http.StatusOK, gin.H{"results": results, "query": query})
	}
}
