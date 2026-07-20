# Plan: task-5-learner-journey implementation

## Goal

Implement Task 5 (`plans/lms-mvp/task-5-learner-journey.md`): course
enrollment, lesson playback with resume-position, progress tracking,
quiz-taking with auto-scoring, assignment submission/grading, course
completion evaluation, certificate issuance/verification, and async
notification dispatch — on top of Task 3 (auth/tenancy) and Task 4
(course domain), both merged into `main`.

## Decisions

See `grilling-record.md` in this directory for full rationale (Q1-Q10).
Summary:

- Assignment is a 6th `blocks.type` value, added via this task's
  migration (Q1).
- Quiz scoring is equal-weight auto-scoring only; no points, no essay
  type, no teacher-review queue (Q2).
- `lessons.watch_threshold_percent` (nullable, default-80-in-Go) stores
  the video watch-completion threshold (Q3).
- `canAccessCourse` is a real check against a new `learner_course_access`
  table, populated by a real free-enrollment endpoint — not a hardcoded
  stub (Q4).
- Learner-facing routes reuse the flat `/api/courses/:courseId/...`
  convention + `ResolveCourseOrg`, gated by a new `RequireEntitlement`
  middleware instead of `RequireRole` (Q5).
- Certificates render via `github.com/go-pdf/fpdf`, pure Go, no headless
  browser (Q6).
- No in-app notification table; email-only via Resend + Redis (Q7).
- Watch progress is tracked via client-side progress pings, not Bunny's
  server-side analytics API (Q8).
- Only course-level prerequisites are enforced (already built in Task 4);
  no lesson-level prerequisite table (Q9).
- Staged worktree implementation, reviewed and merged like Task 4 (Q10).

## Schema (Stage 1 migration: `000004_learner_journey`)

All new tables are org-scoped (`org_id NOT NULL`, RLS `FORCE`d): learners
can read/write only their own rows (`learner_id = app_current_user_id()`);
`owner`/`teacher` can read all rows for their org (needed for grading
queues, progress dashboards, announcements).

- `learner_course_access(id, org_id, learner_id, course_id, entitlement_id NULL, enrolled_at, access_status)`
  — unique (learner_id, course_id).
- `learner_resume_position(org_id, learner_id, course_id, current_lesson_id, last_resumed_at)`
  — PK (learner_id, course_id).
- `learner_lesson_progress(org_id, learner_id, lesson_id, course_id, completed_at NULL, watched_duration_ms, watch_percentage)`
  — PK (learner_id, lesson_id); `course_id` denormalized for RLS/query
  convenience matching Task 4's chapters/lessons pattern.
- `learner_quiz_attempt(id, org_id, learner_id, quiz_block_id, attempt_number, answers_json, submitted_at)`.
- `learner_quiz_score(id, org_id, learner_id, quiz_block_id, attempt_number, score_earned, score_max, percentage, passed, graded_at)`
  — `quiz_block_id` references `blocks.id` (a quiz IS a block in Task 4's
  model, not a separate entity).
- `learner_assignment_submission(id, org_id, learner_id, assignment_block_id, submission_number, file_path, submitted_at, submission_status, due_date_status)`.
- `learner_assignment_grade(id, org_id, submission_id, grade_percentage, feedback_text, graded_by_teacher_id, graded_at)`.
- `course_announcement(id, org_id, course_id, title, body, created_by, published_at)`.
- `learner_certificate(id, org_id, learner_id, course_id, certificate_id UNIQUE, issued_at, pdf_storage_path)`.
- `ALTER TABLE lessons ADD COLUMN watch_threshold_percent INT NULL`.
- `ALTER TABLE blocks DROP CONSTRAINT blocks_type_check, ADD CONSTRAINT blocks_type_check CHECK (type IN ('text','image','video','file','quiz','assignment'))`.
- `ALTER TABLE profiles ADD COLUMN notification_opt_out BOOLEAN NOT NULL DEFAULT false`.

## Stages (one commit each)

1. **Schema migration** — table above, RLS policies, indexes.
2. **Models/repositories** — Go structs + repos for every new table,
   following `internal/models` conventions (`Querier`, `scanX` helpers).
3. **Enrollment, entitlement, player, resume, progress** —
   `POST /api/courses/:courseId/enroll` (checks course_prerequisites,
   course.status=published), `RequireEntitlement` middleware, player
   endpoints (current lesson, next/prev), resume-position read/write,
   watch-progress ping endpoint, lesson-completion evaluation
   (video-threshold / text-viewed / quiz-passed / assignment-submitted),
   course-progress percentage.
4. **Quizzes** — fetch quiz for taking (answer keys stripped), submit
   attempt, auto-score, passing-grade comparison, multiple attempts,
   best-score. Dedicated test proving no `correct_answer_index`/
   `accepted_answers` ever appears in a learner-facing response.
5. **Assignments** — learner file upload (reuse Task 4's Supabase Storage
   signed-URL media client), submission record, teacher grading
   endpoints + queue, resubmission, grading-history visibility.
6. **Completion + certificates** — evaluate `course_completion_rules` on
   every lesson/quiz completion event; auto-issue certificate
   (`go-pdf/fpdf`) when all rules pass; store PDF via Task 4's Supabase
   Storage client; `GET /certificates/verify/:certificate_id` (public,
   unauthenticated, minimal fields only).
7. **Notifications** — asynq task types + handlers for
   assignment-graded/certificate-issued/course-reminder/
   announcement-posted, enqueued from the relevant handlers, calling
   Resend from the worker; respect `profiles.notification_opt_out`.
8. **HTMX UI** — learner dashboard (continue learning, certificates,
   pending submissions), lesson player template, teacher grading page,
   announcements view — lightweight, matching Task 4's "buttons not
   drag-drop" precedent for non-critical UI polish.
9. **Tests** — RLS isolation (one combined test across the new tables,
   matching Task 4's precedent), entitlement gating (non-enrolled learner
   403s), answer-key redaction, completion-rule evaluation → certificate
   issuance, async (not sync) notification dispatch. Run against a real
   local Supabase Postgres.

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` clean after every
  stage.
- Full suite (including RLS/entitlement/redaction/completion tests) run
  with `LMS_TEST_DATABASE_URL` against `supabase start`'s local Postgres.
- Code-review pass (code-reviewer subagent) before merge, focused on:
  RLS on every new table, `RequireEntitlement` never trusting a
  client-supplied learner_id, answer-key redaction, certificate
  auto-issuance never learner-triggerable directly, notification jobs
  never sent synchronously in the request path.
