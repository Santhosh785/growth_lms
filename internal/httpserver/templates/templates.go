// Package templates embeds the lightweight HTMX-driven server-rendered
// pages: Task 4's course-editor page, Task 5's learner-journey pages
// (course landing, lesson player, dashboard, teacher grading queue,
// certificate verification), Task 6's checkout page, and Task 9's
// read-only admin dashboard pages (org-scoped and cross-org). Every page
// follows the same pattern: plain html/template + a CDN htmx script tag +
// small inline <script> blocks where JSON-bodied API calls are needed —
// no client-side framework, no CSS/JS build step (see
// plans/task-5-implementation/main-plan.md Stage 8 and
// grilling-record.md Q10). The Task 9 admin pages are pure GET/read-only:
// no inline scripts, no mutating forms at all.
package templates

import (
	"embed"
	"html/template"
)

//go:embed course_editor.html course_learn.html lesson_player.html dashboard.html submissions.html certificate_verify.html home.html checkout.html admin_dashboard.html admin_organizations.html order_status.html nav.html discussions.html thread.html notifications.html board.html moderation.html legal_privacy.html legal_terms.html
var fs embed.FS

// Nav is the shared nav bar every authenticated/semi-public page embeds
// via {{template "nav-placeholder" .}} (see nav.html). It's htmx-loaded
// from GET /nav rather than rendered inline by each page's own handler,
// so pages don't need to thread auth/role data through just to show a
// nav bar — see handlers.NavFragment's doc comment for why.
var Nav = template.Must(template.ParseFS(fs, "nav.html"))

// parseWithNav parses a page template together with nav.html, so the
// page can reference the "nav-styles" and "nav-placeholder" block
// templates defined there. name must come first in ParseFS's argument
// list: the resulting *Template is named after (and defaults to
// executing) whichever file is parsed first, and it must be the page
// itself, not nav.html (which contains only {{define}} blocks and no
// top-level content of its own).
func parseWithNav(name string) *template.Template {
	return template.Must(template.ParseFS(fs, name, "nav.html"))
}

// Home is the public "/" landing page: a login/signup form for anonymous
// visitors (HomePage redirects already-authenticated visitors to
// /dashboard before this template is ever rendered).
var Home = template.Must(template.ParseFS(fs, "home.html"))

// CourseEditor is the parsed course-editor page template (Task 4).
var CourseEditor = parseWithNav("course_editor.html")

// CourseLearn is the learner-facing course landing page (Task 5 Stage 8).
var CourseLearn = parseWithNav("course_learn.html")

// LessonPlayer is the learner-facing lesson player page (Task 5 Stage 8).
var LessonPlayer = parseWithNav("lesson_player.html")

// Dashboard is the learner dashboard page (Task 5 Stage 8).
var Dashboard = parseWithNav("dashboard.html")

// Submissions is the teacher grading-queue page (Task 5 Stage 8).
var Submissions = parseWithNav("submissions.html")

// CertificateVerify is the public certificate-verification HTML page
// (Task 5 Stage 8; the JSON API sibling is handlers.VerifyCertificate).
var CertificateVerify = template.Must(template.ParseFS(fs, "certificate_verify.html"))

// Checkout is the learner-facing checkout page (Task 6 commerce-handlers;
// the JSON API siblings are handlers.CreateOrder/handlers.OrderStatus).
var Checkout = parseWithNav("checkout.html")

// AdminDashboard is the org-scoped and platform-owner-drill-down
// read-only admin dashboard page (Task 9 admin-dashboard;
// handlers.OrgAdminDashboardPage and handlers.PlatformAdminOrgDetailPage
// both render through this one template — see loadAdminDashboardData's
// doc comment for why they share it).
var AdminDashboard = parseWithNav("admin_dashboard.html")

// AdminOrganizations is the platform-owner cross-organization dashboard
// page (Task 9 admin-dashboard; handlers.PlatformAdminDashboardPage).
var AdminOrganizations = parseWithNav("admin_organizations.html")

// Discussions is the Task 7 org community page (thread list + composer).
var Discussions = parseWithNav("discussions.html")

// Thread is the Task 7 discussion thread page (posts, replies, reactions,
// @-mention picker, report, and moderator actions).
var Thread = parseWithNav("thread.html")

// Notifications is the Task 7 in-app notification inbox page.
var Notifications = parseWithNav("notifications.html")

// Board is the Task 7 collaborative whiteboard page (presence + live notes
// over the /ws/boards/:boardId socket).
var Board = parseWithNav("board.html")

// Moderation is the Task 7 moderator report-queue page.
var Moderation = parseWithNav("moderation.html")

// Privacy and Terms are the public, standalone legal pages (Task 12). Static
// content, no nav or auth — served at /privacy and /terms.
var Privacy = template.Must(template.ParseFS(fs, "legal_privacy.html"))
var Terms = template.Must(template.ParseFS(fs, "legal_terms.html"))

// OrderStatus is the "processing your payment" page a learner's browser
// lands on after Razorpay's checkout.js success callback (Task 10
// routes-wiring; handlers.OrderStatusPage/handlers.OrderStatusFragment —
// see order_status_ui.go). It htmx-polls status-fragment every 2 seconds
// until an HX-Redirect response carries it into the course.
var OrderStatus = parseWithNav("order_status.html")
