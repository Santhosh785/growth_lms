## Grilling Record

> Reference only — not part of the spec. This session grilled the
> IMPLEMENTATION approach for Task 5, after the requirements spec itself
> (`plans/lms-mvp/task-5-learner-journey.md`) was written *before* Task 4
> existed, so several of its assumptions about Task 4's schema turned out
> to be wrong. Grilled against the actual, merged Task 4 code.

<details>
<summary>Complete decision history</summary>

### Q1: Assignment block type
Task 4 built exactly 5 block types (text/image/video/file/quiz) — no
"assignment" type exists, but the spec assumes learners submit files to an
"assignment block."
**Chosen:** Add `'assignment'` as a 6th allowed value on `blocks.type` via
a Task 5 migration (`ALTER TABLE blocks DROP CONSTRAINT ...; ADD
CONSTRAINT ... CHECK (type IN (..., 'assignment'))`). Content shape:
`{ "instructions": "...", "due_date": "...", "allow_resubmission": true }`.

### Q2: Quiz scoring model
Task 4's `QuizQuestion` has no `points` field and only
mcq/true_false/short_answer types (no `essay`) — the spec assumed
point-weighted scoring plus essay questions needing manual teacher
grading.
**Chosen:** Equal-weight auto-scoring only:
`percentage_score = (correct / total) × 100`. Drop the entire
essay/pending-teacher-review subsystem — no schema support exists and none
is added. Every quiz attempt is scored immediately on submit.

### Q3: Watch-threshold storage
No column anywhere in Task 4's schema stores a video watch-completion
threshold.
**Chosen:** `lessons.watch_threshold_percent` (nullable int), added via
Task 5 migration. NULL falls back to a hardcoded default (80%) applied in
Go, not a second table.

### Q4: Entitlement stub
The spec says to stub `canAccessCourse()` to return true for hardcoded
test learners, since Task 6 (payments) doesn't exist yet.
**Chosen:** Build a real free-enrollment path instead:
`POST /api/courses/:courseId/enroll` creates a `learner_course_access` row
(`entitlement_id` NULL) for any org member, only on a `published` course.
`canAccessCourse` becomes a real query against `learner_course_access` —
no hardcoding. Task 6 later populates `entitlement_id` from a verified
payment as a second way to reach the same table; it does not replace this
path.

### Q5: Learner route access pattern
Task 4's course routes are gated by `RequireRole(owner, teacher)` — all
403 for learners. Task 5's player/progress/quiz endpoints need the
opposite: any *enrolled* org member, regardless of role.
**Chosen:** New learner routes reuse the existing flat
`/api/courses/:courseId/...` path convention and Task 4's
`ResolveCourseOrg` middleware for org-context resolution, but gate on a
new `RequireEntitlement` middleware (checks `learner_course_access`
instead of role) rather than `RequireRole`. For non-owner/teacher callers,
`RequireEntitlement` also requires `course.status == 'published'`.

### Q6: Certificate PDF library
Spec requires server-side Go PDF generation, no third-party vendor API.
**Chosen:** `github.com/go-pdf/fpdf` (maintained fork of the
unmaintained `jung-kurt/gofpdf`) — pure Go, no headless-Chrome/wkhtmltopdf
runtime dependency in the Docker image. Programmatic layout (draw
text/shapes) against a fixed certificate template with placeholders
(learner name, course name, completion date, certificate ID) — not
HTML/CSS-templated.

### Q7: In-app notifications
Spec marks `learner_notification` as "optional; minimal MVP."
**Chosen:** Skip it. Email-only via Resend + Task 2's Redis job queue for
all notification events (assignment graded, certificate issued, course
reminder, announcement posted). No in-app table/list/bell.

### Q8: Video watch-progress tracking mechanism
Bunny Stream's server-side analytics are aggregate/delayed and not
identified per-learner — unsuited to real-time per-learner threshold
detection, contrary to the spec's "use bunny.net's analytics API"
suggestion.
**Chosen:** Client-side progress pings: the player's video element posts
periodic watch-progress events (every ~10s, plus on pause/seek/end) to a
backend endpoint with current watch time/percentage; the backend persists
the high-water mark per (learner, lesson) and crosses the configured
threshold when reached.

### Q9: Lesson-level prerequisites
Spec mentions lesson-level prerequisites "if defined in Task 4's schema"
— Task 4 only built course-level (`course_prerequisites`), no
lesson-level table.
**Chosen:** Enforce course-level prerequisites only (on enroll: all
`course_prerequisites` for the target course must already be
`complete` for that learner). No lesson-to-lesson prerequisite gating —
lessons remain sequentially navigable but not individually locked. Matches
the spec's own hedge and avoids adding schema Task 4 never scoped.

### Q10: Implementation approach
**Chosen:** Same as Task 4 — staged implementation in an isolated
worktree (`worktree-task-5-learner-journey`), one commit per stage
(schema → models → enrollment/player/progress → quizzes → assignments →
completion/certificates → notifications → HTMX UI → tests), then a
code-review pass and merge into `main`.

</details>
