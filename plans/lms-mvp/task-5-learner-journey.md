---
task: 5
name: learner-journey
parallel_group: 5
depends_on: [4]
issue: TBD
---

# Task 5: Learner Journey — Player, Progress, Assessments, Certificates

## What to build

Implement the complete learner experience: course enrollment, lesson playback with resume-position tracking, progress tracking, quiz-taking with scoring, assignment submission and grading, course completion rules, certificate issuance and verification, and notification dispatch.

### 1. Public and Authenticated Course Pages

- **Public course preview page** (unauthenticated): displays course title, description, curriculum outline, instructor info, course-level completion requirements, and a call-to-action for enrollment.
- **Course access check**: before a learner can view course content or the player, verify they have an active entitlement for that course (stub an "entitlement check" function that Task 6 will implement; for now, allow hardcoded test users).
- **Authenticated course landing page**: shows course overview, chapters/lessons list (collapsed/expandable), progress bar, and a "Resume Learning" or "Start Course" button.

### 2. Course Player and Resume-Position Tracking

- **Lesson player**: sequential navigation through chapters and lessons within a course.
  - Display current lesson content (text, video, embedded blocks from Task 4).
  - "Previous" and "Next" buttons for sequential lesson navigation; handle chapter boundaries (last lesson of a chapter → first lesson of next chapter, or end course).
  - Persist the learner's current position (course + lesson) in the database, keyed by (learner_id, course_id).
  - Resume-position tracking must work correctly across devices and browser sessions: when a learner returns to a course, they resume at the last visited lesson.

- **Video playback integration** (bunny.net):
  - Embed video player using bunny.net's player SDK (learner can only play videos from their organization's namespace).
  - Track watch progress: record the amount of time watched and the final percentage of the video watched.
  - Implement a watch-threshold rule (e.g., a lesson counts as "watched" only if the learner reaches 80% of the video); make this configurable per course or per lesson (store threshold in the lesson schema from Task 4 or in a lesson-specific override table).
  - When a lesson is marked as "watched" (threshold crossed), automatically set lesson completion status.

- **Lesson completion tracking**:
  - Mark a lesson as "complete" when:
    - A video lesson is watched beyond the threshold, OR
    - A text/static lesson is viewed (mark complete on first page load), OR
    - A quiz block within the lesson is passed, OR
    - An assignment block within the lesson is submitted.
  - Store lesson completion in a learner_lesson_progress table (org_id, learner_id, lesson_id, completed_at, watched_duration, watch_percentage).

### 3. Progress and Course Completion Tracking

- **Per-course progress percentage**: calculate as (completed_lessons / total_lessons) × 100; update this whenever a lesson completion status changes.
- **Progress display**: show on course landing page, player header, and learner dashboard.
- **Course completion rules** (defined by the teacher in Task 4, consumed here):
  - A course is "complete" for a learner when all completion rules pass (e.g., all required lessons are completed AND the final quiz score is >= passing grade).
  - Evaluate completion rules whenever a learner completes a lesson or quiz; if all rules pass, immediately mark the course as "complete" and trigger certificate issuance (see section 6).

### 4. Prerequisite Enforcement

- **Lesson prerequisites** (if defined in Task 4's schema): a learner cannot access a lesson until all its prerequisites are completed.
- **Course prerequisites** (if defined in Task 4's schema): a learner cannot enroll in or access a course until all prerequisite courses are completed.
- **Access control**: in the player, check prerequisites before rendering a lesson; if prerequisites are not met, display a locked state with a message ("Complete X lesson first").

### 5. Quizzes: Authoring, Attempts, Scoring, and Passing Logic

**Build on top of the quiz-authoring content created in Task 4.** Assume Task 4 has created a question bank structure with question types (multiple choice, essay, matching, etc.), correct answers, and point values.

- **Quiz-taking flow**:
  - Learner navigates to a quiz within a lesson or course.
  - Display questions from the question bank one at a time or all on one page (teacher-configurable in Task 4, use that config here).
  - Accept learner answers (store in learner_quiz_attempt table: org_id, learner_id, quiz_id, attempt_number, answer_json, submitted_at).
  - Submit the quiz; immediately score it (auto-scoring for objective questions like multiple choice; essay questions are marked for teacher review, pending = true).

- **Scoring and passing**:
  - Calculate total points earned: sum up points for all correct answers (learner_quiz_score table: learner_id, quiz_id, attempt_number, total_points, max_points, percentage_score).
  - Compare percentage_score against the quiz's passing_grade_percentage (configured by teacher in Task 4).
  - If percentage_score >= passing_grade, mark the attempt as "passed"; otherwise "failed".
  - Allow multiple attempts (configurable per quiz; store attempt_number in the score table).
  - Show learner's best score and/or all attempts (configurable).

- **Data isolation and security**:
  - Quiz attempts and scores must be org-scoped and learner-scoped (use the RLS pattern from Task 3).
  - **CRITICAL**: learner-facing APIs and HTML templates must NEVER expose the correct answer, correct_answer field, or teacher-only notes for quiz questions. Verify this with a test that attempts to fetch quiz question details as a learner and confirms that answer keys are absent.
  - Return only the question text, question type, and answer options (without marking which is correct) to the learner during quiz-taking.

- **Essay/pending-review questions**:
  - After a learner submits, if the quiz contains essay or other pending-review questions, mark the quiz attempt as pending_teacher_review = true.
  - Teachers see these attempts in a review queue (build a basic teacher review page that lists pending quiz attempts and allows adding a score/feedback).

### 6. Assignments: Submission, Grading, and Resubmission

- **Assignment submission**:
  - Learner uploads a file to an assignment block (integrated in the lesson player).
  - File is stored in Supabase Storage (per Task 4's non-video storage decision).
  - Record the submission in learner_assignment_submission table: org_id, learner_id, assignment_id, submitted_at, file_path, submission_status (submitted/graded/pending_review).
  - If the assignment has a due date, record due_date_status (on_time / late).

- **Teacher grading and feedback**:
  - Build a teacher grading interface (simple page listing pending submissions for a course).
  - Teacher can view the submitted file, add a grade (points or percentage), and add text feedback.
  - Record grade in learner_assignment_grade table: learner_id, assignment_id, submission_id, grade, feedback_text, graded_at, graded_by_teacher_id.
  - Send the learner a notification (async via Redis job queue, see section 8) when their assignment is graded.

- **Resubmission flow**:
  - If the teacher's feedback indicates resubmission is needed (configurable per assignment), allow the learner to upload a new file.
  - Store multiple submissions per learner per assignment.
  - Grading history is visible to the learner (they can see all previous feedback).

### 7. Learner Dashboard and Course Continuation

- **Learner dashboard**: authenticated page showing:
  - "Continue Learning" section: list of enrolled courses sorted by most-recently-resumed, with progress bars and resume buttons.
  - "Certificates" section: list of earned certificates (with download link and verification link, see section 8).
  - "Pending Submissions" or "My Assignments": list of ungraded or pending-review assignments (for engagement).
  - Quick stats: total courses enrolled, total completed, etc.

- **Resume Learning button**: clicking takes the learner to their last-viewed lesson in that course (uses resume_position data from section 2).

### 8. Course Announcements

- **Teacher-authored announcements**: teachers can publish text announcements to a course (integrated in Task 4's course-authoring UI or a separate endpoint here).
- **Learner view**: display announcements on the course landing page or in a dedicated "Announcements" section, sorted newest first.
- **Organization-scoped**: announcements are visible only to learners in the course's organization (use org_id for RLS).

### 9. Certificate Generation, Issuance, and Verification

- **Certificate templates**: store a template (PDF template with placeholders for learner name, course name, completion date, certificate ID).
  - Allow the teacher to configure which fields appear on the certificate (or use a default template).

- **Automatic issuance**: when course completion rules are evaluated and all rules pass (section 3), automatically trigger certificate generation:
  - Generate a unique certificate_id (e.g., UUID).
  - Render the template with the learner's name, course name, completion date, and certificate_id.
  - **SERVER-SIDE PDF GENERATION IN GO**: use a Go PDF library (e.g., github.com/jung-kurt/gofpdf or github.com/go-echarts/go-echarts for simple templating, or wkhtmltopdf via exec if HTML-to-PDF is preferred) to generate the PDF from the template.
  - Do NOT integrate a third-party PDF-generation vendor (e.g., no API calls to external certificate services).
  - Store the certificate metadata in learner_certificate table: org_id, learner_id, course_id, certificate_id, issued_at, pdf_storage_path (path in Supabase Storage).

- **Learner can only receive certificates automatically** when completion rules pass; they cannot manually generate or claim certificates.

- **Certificate download**: learner can download the certificate PDF from their dashboard (fetch the file from Supabase Storage by pdf_storage_path).

- **Public certificate verification URL**: create an unauthenticated endpoint `/certificates/verify/:certificate_id` that returns the certificate's metadata (learner name, course name, issued date) and allows the public to verify that a certificate is legitimate without exposing unnecessary data.

### 10. Learner Notifications and Email Reminders

- **Notification types**:
  - Assignment graded: "Your assignment has been graded. Feedback: [teacher feedback]."
  - Certificate issued: "Congratulations! You've completed [Course Name]. Your certificate is ready."
  - Course reminder: "Continue learning [Course Name]. You're [X%] complete."
  - Course announcement: "[Announcement title] posted in [Course Name]."

- **Email dispatch via Resend and Redis job queue**:
  - Do NOT send emails synchronously in the request path.
  - When a notification-triggering event occurs (e.g., assignment graded), enqueue a job in Redis (using the job queue from Task 2) with the event type, learner email, and event data.
  - A separate worker/consumer processes the queue, fetches the necessary data, and calls the Resend API to send the email.
  - Learner email is fetched from the auth_users table or learner profile table.

- **Notification preferences** (optional; minimal MVP): learners can opt out of email reminders (store a flag in their profile). Do not send emails to opted-out learners.

### 11. Database Schema and RLS

- **New tables** (all org-scoped):
  - `learner_course_access` (org_id, learner_id, course_id, entitlement_id, enrolled_at, access_status) — stores enrollment/entitlement link; RLS: learners see only their own rows; teachers see learners in their org/course.
  - `learner_resume_position` (org_id, learner_id, course_id, current_lesson_id, last_resumed_at) — tracks where learner left off.
  - `learner_lesson_progress` (org_id, learner_id, lesson_id, completed_at, watched_duration_ms, watch_percentage) — tracks lesson completion and video watch metrics.
  - `learner_quiz_attempt` (org_id, learner_id, quiz_id, attempt_number, answers_json, submitted_at, pending_review_flag) — stores quiz answers.
  - `learner_quiz_score` (org_id, learner_id, quiz_id, attempt_number, score_earned, score_max, percentage, passed, graded_at) — stores scores.
  - `learner_assignment_submission` (org_id, learner_id, assignment_id, submission_number, file_path, submitted_at, submission_status, due_date_status).
  - `learner_assignment_grade` (org_id, learner_id, assignment_id, submission_id, grade_points, grade_percentage, feedback_text, graded_by_teacher_id, graded_at).
  - `course_announcement` (org_id, course_id, title, body, created_by_teacher_id, published_at) — teacher announcements.
  - `learner_certificate` (org_id, learner_id, course_id, certificate_id, issued_at, pdf_storage_path).
  - `learner_notification` (org_id, learner_id, notification_type, event_data_json, created_at, read_at) — optional, for in-app notifications.

- **RLS policies**: all tables use org_id as the organization scope. Learners can only read/write their own rows; teachers can read rows for their org and courses they teach.

### 12. Integration Assumptions

- **Entitlement check stub** (Task 6 will implement the real gate): create a function `canAccessCourse(learner_id, course_id)` that currently returns true for hardcoded test learners; Task 6 will replace this with a real entitlement check.
- **Bunny.net integration** (from Task 4): assume videos are stored and playable via bunny.net; use bunny.net's analytics API or embed tracking events to record watch progress.
- **Redis job queue** (from Task 2): assume a Redis-backed job queue exists; enqueue notification/email jobs to it.
- **Resend email provider** (from Task 2): assume Resend API credentials are configured; use the Resend Go client to send emails.

## Acceptance criteria

- [ ] Learners can resume a course accurately across devices and browser sessions; resume_position correctly persists and updates on each page load.
- [ ] Video watch-threshold and lesson-completion tracking integrate with bunny.net playback; a lesson is marked "watched" only when the learner's watch percentage crosses the configured threshold.
- [ ] Course completion rules are evaluated correctly; a course is marked complete only when all rules (e.g., all required lessons + passing quiz score) actually pass.
- [ ] Certificates are issued automatically and only when a course's completion rules pass; learners cannot manually generate or claim certificates via UI actions alone.
- [ ] Certificate PDFs are generated server-side in Go (no third-party PDF-generation vendor calls); PDFs are downloadable from the learner dashboard and independently verifiable via a public certificate verification URL.
- [ ] A test confirms that learner-facing API responses and HTML templates never expose quiz answer keys, correct_answer fields, or teacher-only question metadata; attempts to fetch quiz details as a learner return only question text and answer options.
- [ ] Teachers can review, grade, and return assignments, including handling resubmissions; learners can see grading history and feedback for all submission attempts.
- [ ] Email reminders and notifications are dispatched asynchronously via the Redis job queue, not synchronously in the request path; verify by checking that notification events enqueue jobs without blocking the learner's request.
- [ ] Learners cannot access lessons until course and lesson prerequisites are completed; locked lessons display a clear message indicating the prerequisite that must be completed first.
- [ ] All learner-scoped and org-scoped data (quiz attempts, assignments, certificates, progress, etc.) are protected by RLS policies; cross-org data access is denied, and learners cannot access other learners' data.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.

Example: `Implement learner course player and progress tracking (Closes #47)`
