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

//go:embed course_editor.html course_learn.html lesson_player.html dashboard.html submissions.html certificate_verify.html checkout.html admin_dashboard.html admin_organizations.html order_status.html
var fs embed.FS

// CourseEditor is the parsed course-editor page template (Task 4).
var CourseEditor = template.Must(template.ParseFS(fs, "course_editor.html"))

// CourseLearn is the learner-facing course landing page (Task 5 Stage 8).
var CourseLearn = template.Must(template.ParseFS(fs, "course_learn.html"))

// LessonPlayer is the learner-facing lesson player page (Task 5 Stage 8).
var LessonPlayer = template.Must(template.ParseFS(fs, "lesson_player.html"))

// Dashboard is the learner dashboard page (Task 5 Stage 8).
var Dashboard = template.Must(template.ParseFS(fs, "dashboard.html"))

// Submissions is the teacher grading-queue page (Task 5 Stage 8).
var Submissions = template.Must(template.ParseFS(fs, "submissions.html"))

// CertificateVerify is the public certificate-verification HTML page
// (Task 5 Stage 8; the JSON API sibling is handlers.VerifyCertificate).
var CertificateVerify = template.Must(template.ParseFS(fs, "certificate_verify.html"))

// Checkout is the learner-facing checkout page (Task 6 commerce-handlers;
// the JSON API siblings are handlers.CreateOrder/handlers.OrderStatus).
var Checkout = template.Must(template.ParseFS(fs, "checkout.html"))

// AdminDashboard is the org-scoped and platform-owner-drill-down
// read-only admin dashboard page (Task 9 admin-dashboard;
// handlers.OrgAdminDashboardPage and handlers.PlatformAdminOrgDetailPage
// both render through this one template — see loadAdminDashboardData's
// doc comment for why they share it).
var AdminDashboard = template.Must(template.ParseFS(fs, "admin_dashboard.html"))

// AdminOrganizations is the platform-owner cross-organization dashboard
// page (Task 9 admin-dashboard; handlers.PlatformAdminDashboardPage).
var AdminOrganizations = template.Must(template.ParseFS(fs, "admin_organizations.html"))

// OrderStatus is the "processing your payment" page a learner's browser
// lands on after Razorpay's checkout.js success callback (Task 10
// routes-wiring; handlers.OrderStatusPage/handlers.OrderStatusFragment —
// see order_status_ui.go). It htmx-polls status-fragment every 2 seconds
// until an HX-Redirect response carries it into the course.
var OrderStatus = template.Must(template.ParseFS(fs, "order_status.html"))
