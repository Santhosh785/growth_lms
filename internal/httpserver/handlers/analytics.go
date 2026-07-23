package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// analyticsDashboardWindow is how far back the dashboard's trend charts
// and course leaderboard look — 30 days, matching most SaaS analytics
// dashboards' default range. Not user-configurable in this MVP pass.
const analyticsDashboardWindow = 30 * 24 * time.Hour

// OrgAnalytics is GET /api/orgs/:org_slug/analytics — creator/org
// analytics (plan.md Task 8): daily trend series for enrollment/
// completion/revenue-proxy-via-purchase-count/learner-activity, plus a
// per-course leaderboard. Owner/teacher only (see server.go route
// wiring, mirroring is_org_teacher's RLS gate on the underlying tables).
func OrgAnalytics(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		until := time.Now().UTC()
		since := until.Add(-analyticsDashboardWindow)

		series := gin.H{}
		for _, metric := range []string{
			models.EventEnrollment, models.EventLessonCompletion,
			models.EventPurchase, models.EventCourseView,
		} {
			s, err := d.AnalyticsRollups.OrgSeries(ctx, tx, oc.OrgID, metric, since, until)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			series[metric] = s
		}

		enrollmentsByCourse, err := d.AnalyticsRollups.CourseTotals(ctx, tx, oc.OrgID, models.EventEnrollment, since, until)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		completionsByCourse, err := d.AnalyticsRollups.CourseTotals(ctx, tx, oc.OrgID, models.EventLessonCompletion, since, until)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		purchasesByCourse, err := d.AnalyticsRollups.CourseTotals(ctx, tx, oc.OrgID, models.EventPurchase, since, until)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"window_days": int(analyticsDashboardWindow.Hours() / 24),
			"series":      series,
			"by_course": gin.H{
				"enrollments": enrollmentsByCourse,
				"completions": completionsByCourse,
				"purchases":   purchasesByCourse,
			},
		})
	}
}
