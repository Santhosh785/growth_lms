// Package templates embeds the lightweight HTMX-driven server-rendered
// pages: Task 4's course-editor page, and Task 5's learner-journey pages
// (course landing, lesson player, dashboard, teacher grading queue,
// certificate verification). Every page follows the same pattern: plain
// html/template + a CDN htmx script tag + small inline <script> blocks
// where JSON-bodied API calls are needed — no client-side framework, no
// CSS/JS build step (see plans/task-5-implementation/main-plan.md Stage 8
// and grilling-record.md Q10).
package templates

import (
	"embed"
	"html/template"
)

//go:embed course_editor.html course_learn.html lesson_player.html dashboard.html submissions.html certificate_verify.html home.html
var fs embed.FS

// Home is the public "/" landing page: a login/signup form for anonymous
// visitors (HomePage redirects already-authenticated visitors to
// /dashboard before this template is ever rendered).
var Home = template.Must(template.ParseFS(fs, "home.html"))

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
