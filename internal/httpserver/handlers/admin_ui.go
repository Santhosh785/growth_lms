// Package handlers' admin_ui.go implements Task 9's read-only admin
// dashboard pages: an org-scoped dashboard for organization owners
// (GET /orgs/:org_slug/admin) and a cross-organization dashboard for
// platform owners (GET /admin/organizations, with a per-org drill-down at
// GET /admin/organizations/:org_slug). These are pure reporting pages —
// no form or link here ever issues a POST/PATCH/PUT/DELETE. The refund
// button ("refund.initiate") and entitlement-grant form
// ("entitlement.grant") are commerce_refunds.go's job, not this file's;
// this file does not build any mutation UI.
//
// Follows learner_ui.go's precedent: plain html/template, a package-level
// view struct per template, Content-Type set explicitly, data loaded via
// existing internal/models repo methods (MembershipRepo.ListByOrg,
// CourseRepo.List reused verbatim, per this task's explicit instruction
// not to duplicate those queries).
package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
	"growth-lms/internal/models"
)

// ---- shared view types ----

type adminMemberView struct {
	Email    string
	FullName string
	Role     string
	JoinedAt time.Time
}

type adminCourseView struct {
	ID        string
	Title     string
	Status    string
	CreatedAt time.Time
}

// adminRevenueRow is one (course, currency) line in the "enrollment &
// revenue overview" table — never a single row summing currencies
// together (grilling-record.md Q1).
type adminRevenueRow struct {
	CourseID        string
	CourseTitle     string
	Currency        string
	EnrollmentCount int
	OrderCount      int
	NetRevenue      float64
}

// adminDashboardView is the shape both the org-scoped dashboard
// (OrgAdminDashboardPage) and the platform-owner per-org drill-down
// (PlatformAdminOrgDetailPage) render via the same admin_dashboard.html
// template, per the task doc's explicit instruction to reuse one
// template rather than hand-rolling a near-duplicate.
type adminDashboardView struct {
	OrgName   string
	OrgSlug   string
	Members   []adminMemberView
	Courses   []adminCourseView
	Revenue   []adminRevenueRow
	From      string
	To        string
	OfferType string
	// IsPlatformDrilldown controls whether the page shows a "back to
	// /admin/organizations" link — cosmetic only, no behavioral branch.
	IsPlatformDrilldown bool
}

// loadAdminDashboardData loads the three data sections shared by
// OrgAdminDashboardPage and PlatformAdminOrgDetailPage: members (via
// MembershipRepo.ListByOrg, reused verbatim), courses (via
// CourseRepo.List, reused verbatim), and the per-course enrollment-count
// + net-of-commission-revenue table (via LearnerCourseAccessRepo.
// CountActiveByOrg and OrderRepo.RevenueByCourse, both added by this
// task). No raw SQL here — every DB access goes through internal/models.
func loadAdminDashboardData(c *gin.Context, d *AuthDeps, orgID string, filter models.RevenueFilter) ([]adminMemberView, []adminCourseView, []adminRevenueRow, error) {
	tx, _ := middleware.RequestTxFromGin(c)
	ctx := c.Request.Context()

	members, err := d.Memberships.ListByOrg(ctx, tx, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	memberViews := make([]adminMemberView, len(members))
	for i, m := range members {
		fullName := ""
		if m.FullName != nil {
			fullName = *m.FullName
		}
		memberViews[i] = adminMemberView{Email: m.Email, FullName: fullName, Role: m.Role, JoinedAt: m.JoinedAt}
	}

	courses, err := d.Courses.List(ctx, tx, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	courseViews := make([]adminCourseView, len(courses))
	for i, course := range courses {
		courseViews[i] = adminCourseView{ID: course.ID, Title: course.Title, Status: course.Status, CreatedAt: course.CreatedAt}
	}

	enrollmentCounts, err := d.LearnerCourseAccess.CountActiveByOrg(ctx, tx, orgID)
	if err != nil {
		return nil, nil, nil, err
	}

	revenueSummaries, err := d.Orders.RevenueByCourse(ctx, tx, orgID, filter)
	if err != nil {
		return nil, nil, nil, err
	}
	revenueRows := make([]adminRevenueRow, len(revenueSummaries))
	for i, s := range revenueSummaries {
		revenueRows[i] = adminRevenueRow{
			CourseID:        s.CourseID,
			CourseTitle:     s.CourseTitle,
			Currency:        s.Currency,
			EnrollmentCount: enrollmentCounts[s.CourseID],
			OrderCount:      s.OrderCount,
			NetRevenue:      s.NetRevenue,
		}
	}

	return memberViews, courseViews, revenueRows, nil
}

// parseRevenueFilterQuery reads the optional ?from=/?to=/?offer_type=
// query params (from/to as YYYY-MM-DD date strings, matching a plain
// HTML <input type="date">) into a models.RevenueFilter, returning the
// raw string values too so the template can echo them back into the
// filter form's inputs. Unparseable from/to values are silently ignored
// (left unset) rather than erroring the whole page — this is a reporting
// convenience filter, not a validated API input.
func parseRevenueFilterQuery(c *gin.Context) (models.RevenueFilter, string, string, string) {
	var filter models.RevenueFilter

	fromStr := c.Query("from")
	if fromStr != "" {
		if t, err := time.Parse("2006-01-02", fromStr); err == nil {
			filter.From = &t
		}
	}
	toStr := c.Query("to")
	if toStr != "" {
		if t, err := time.Parse("2006-01-02", toStr); err == nil {
			// Treat "to" as inclusive of the whole day by bumping to the
			// start of the next day, matching RevenueByCourse's
			// created_at < filter.To semantics.
			t = t.Add(24 * time.Hour)
			filter.To = &t
		}
	}
	offerType := c.Query("offer_type")
	if offerType != "" {
		filter.OfferType = &offerType
	}

	return filter, fromStr, toStr, offerType
}

// OrgAdminDashboardPage renders GET /orgs/:org_slug/admin: the org's
// member list with roles, course list with publish status, and a
// per-course enrollment-count + net-of-commission-revenue table
// segmented by currency, optionally narrowed by ?from=/?to=/?offer_type=
// via a plain <form method="get">. Gated by
// middleware.RequireRole(auth.RoleOwner) at the route (see server.go) —
// "visible to organization owners" per the commerce spec's literal
// wording, matching permissions-matrix's owner-only "dashboard.org.view"
// action. RequireRole already lets a platform owner through too, so a
// platform owner can open any org's /admin page without being a member —
// intentional, matching the support-purposes framing the platform-owner
// drill-down (PlatformAdminOrgDetailPage) links here for.
func OrgAdminDashboardPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)

		filter, fromStr, toStr, offerType := parseRevenueFilterQuery(c)
		members, courses, revenue, err := loadAdminDashboardData(c, d, oc.OrgID, filter)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.AdminDashboard.Execute(c.Writer, adminDashboardView{
			OrgName:   oc.Slug,
			OrgSlug:   oc.Slug,
			Members:   members,
			Courses:   courses,
			Revenue:   revenue,
			From:      fromStr,
			To:        toStr,
			OfferType: offerType,
		})
	}
}

// adminOrgSummaryView is one row of the platform-owner cross-org list.
type adminOrgSummaryView struct {
	Slug            string
	Name            string
	MemberCount     int
	CourseCount     int
	EnrollmentCount int
	Commission      []adminCommissionView
}

type adminCommissionView struct {
	Currency string
	Amount   float64
}

// PlatformAdminDashboardPage renders GET /admin/organizations: every
// organization on the platform with member count, course count,
// enrollment count, and commission revenue segmented by currency (INR
// and USD never summed together). Gated ONLY by
// middleware.RequirePlatformOwner (see server.go) — no permissionMatrix/
// Can() involvement at all, per the commerce spec's literal wording
// ("visible only to profiles.is_platform_owner") and
// task-3-permissions-matrix.md's note that this view is not org-scoped.
//
// See OrgRepo.ListAll, CourseRepo.CountByOrg, and
// LearnerCourseAccessRepo.CountActiveByOrg's doc comments for the RLS
// gaps this handler surfaces (courses/learner_course_access have no
// app_is_platform_owner() bypass, so a platform owner viewing an org
// they don't belong to will see zero courses/enrollments here until
// those policies are fixed in a follow-up migration) — flagged, not
// silently worked around with a service-role connection.
func PlatformAdminDashboardPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		orgs, err := d.Orgs.ListAll(ctx, tx)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		commissionRows, err := d.Orders.CommissionByOrg(ctx, tx)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}
		commissionByOrg := map[string][]adminCommissionView{}
		for _, row := range commissionRows {
			commissionByOrg[row.OrgID] = append(commissionByOrg[row.OrgID], adminCommissionView{Currency: row.Currency, Amount: row.Commission})
		}

		views := make([]adminOrgSummaryView, len(orgs))
		for i, org := range orgs {
			memberCount, err := d.Memberships.CountByOrg(ctx, tx, org.ID)
			if err != nil {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			courseCount, err := d.Courses.CountByOrg(ctx, tx, org.ID)
			if err != nil {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			enrollmentByCourse, err := d.LearnerCourseAccess.CountActiveByOrg(ctx, tx, org.ID)
			if err != nil {
				c.String(http.StatusInternalServerError, "internal error")
				return
			}
			enrollmentTotal := 0
			for _, n := range enrollmentByCourse {
				enrollmentTotal += n
			}

			views[i] = adminOrgSummaryView{
				Slug:            org.Slug,
				Name:            org.Name,
				MemberCount:     memberCount,
				CourseCount:     courseCount,
				EnrollmentCount: enrollmentTotal,
				Commission:      commissionByOrg[org.ID],
			}
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.AdminOrganizations.Execute(c.Writer, gin.H{
			"Orgs": views,
		})
	}
}

// PlatformAdminOrgDetailPage renders GET /admin/organizations/:org_slug:
// a read-only drill-down into a single org's members/courses/enrollment-
// and-revenue, reusing loadAdminDashboardData (the exact same data load
// OrgAdminDashboardPage uses) and rendering it through the exact same
// admin_dashboard.html template — this is explicitly a VIEW, not an edit
// page, matching the commerce spec's "ability to view (not edit)...
// explicitly no bulk actions, no edit capability" wording. Gated the same
// way as PlatformAdminDashboardPage (middleware.RequirePlatformOwner
// only); looks the org up directly by slug via OrgRepo.GetBySlug rather
// than going through ResolveOrg, since a platform owner viewing an
// arbitrary org here should not require ResolveOrg's membership-or-
// platform-owner resolution dance (see server.go's routing comment).
func PlatformAdminOrgDetailPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		org, err := d.Orgs.GetBySlug(ctx, tx, c.Param("org_slug"))
		if err != nil {
			c.String(http.StatusNotFound, "organization not found")
			return
		}

		filter, fromStr, toStr, offerType := parseRevenueFilterQuery(c)
		members, courses, revenue, err := loadAdminDashboardData(c, d, org.ID, filter)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.AdminDashboard.Execute(c.Writer, adminDashboardView{
			OrgName:             org.Name,
			OrgSlug:             org.Slug,
			Members:             members,
			Courses:             courses,
			Revenue:             revenue,
			From:                fromStr,
			To:                  toStr,
			OfferType:           offerType,
			IsPlatformDrilldown: true,
		})
	}
}
