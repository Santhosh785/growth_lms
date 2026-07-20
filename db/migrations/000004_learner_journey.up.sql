-- Task 5 (Stage 1): learner journey schema — enrollment/entitlement,
-- player resume position, lesson/quiz/assignment progress, course
-- announcements, and certificates.
--
-- All new tables here are org-scoped (org_id NOT NULL, RLS ENABLE +
-- FORCE) and introduce this codebase's first "row belongs to this
-- specific user" RLS pattern, layered on top of Task 3's helpers
-- (see 000002_auth_tenancy.up.sql for app_current_user_id/app_current_role/
-- is_org_member/is_org_owner):
--
--   - Learners may SELECT/INSERT/UPDATE only their own rows
--     (learner_id = app_current_user_id()).
--   - Org owners/teachers may additionally SELECT every row for their org
--     (is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')),
--     since grading queues, progress dashboards, and announcements all
--     need org-wide read access regardless of whose row it is.
--
-- Two tables have no direct learner_id column and so don't fit that
-- pattern verbatim:
--   - learner_assignment_grade is teacher-authored, keyed to a submission
--     (learner-read access is expressed via a join back to
--     learner_assignment_submission.learner_id).
--   - course_announcement has no learner scope at all — it's teacher/owner
--     authored and readable by every org member (matching Task 4's
--     shared-content RLS convention).

-- === learner_course_access ===============================================
-- Real entitlement table (see grilling-record.md Q4): a row here, not a
-- hardcoded check, is what canAccessCourse() queries. entitlement_id is
-- left as a plain nullable UUID with no FK — Task 6 (payments) hasn't
-- built an entitlements table yet; free self-enrollment leaves it NULL.

CREATE TABLE learner_course_access (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    entitlement_id UUID,
    enrolled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    access_status TEXT NOT NULL DEFAULT 'active'
      CHECK (access_status IN ('active', 'revoked', 'expired')),
    UNIQUE (learner_id, course_id)
);
CREATE INDEX learner_course_access_org_idx ON learner_course_access (org_id);
CREATE INDEX learner_course_access_learner_idx ON learner_course_access (learner_id);
CREATE INDEX learner_course_access_course_idx ON learner_course_access (course_id);

ALTER TABLE learner_course_access ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_course_access FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_course_access_select ON learner_course_access FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_course_access.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY learner_course_access_insert ON learner_course_access FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(learner_course_access.org_id));
CREATE POLICY learner_course_access_update ON learner_course_access FOR UPDATE
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_course_access.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );

-- === learner_resume_position ===============================================
-- One row per (learner, course): "continue learning" pointer, upserted by
-- the player as the learner navigates lessons.

CREATE TABLE learner_resume_position (
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    current_lesson_id UUID NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    last_resumed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (learner_id, course_id)
);
CREATE INDEX learner_resume_position_org_idx ON learner_resume_position (org_id);
CREATE INDEX learner_resume_position_course_idx ON learner_resume_position (course_id);

ALTER TABLE learner_resume_position ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_resume_position FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_resume_position_select ON learner_resume_position FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_resume_position.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY learner_resume_position_insert ON learner_resume_position FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(learner_resume_position.org_id));
CREATE POLICY learner_resume_position_update ON learner_resume_position FOR UPDATE
  USING (learner_id = app_current_user_id())
  WITH CHECK (learner_id = app_current_user_id());

-- === learner_lesson_progress ================================================
-- One row per (learner, lesson); course_id is denormalized down from the
-- parent lesson for RLS/query convenience, matching Task 4's
-- chapters/lessons denormalization precedent. Populated by watch-progress
-- pings (video) and completion events (text/quiz/assignment).

CREATE TABLE learner_lesson_progress (
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    lesson_id UUID NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    completed_at TIMESTAMPTZ,
    watched_duration_ms BIGINT NOT NULL DEFAULT 0,
    watch_percentage NUMERIC(5,2) NOT NULL DEFAULT 0,
    PRIMARY KEY (learner_id, lesson_id)
);
CREATE INDEX learner_lesson_progress_org_idx ON learner_lesson_progress (org_id);
CREATE INDEX learner_lesson_progress_learner_course_idx ON learner_lesson_progress (learner_id, course_id);
CREATE INDEX learner_lesson_progress_lesson_idx ON learner_lesson_progress (lesson_id);

ALTER TABLE learner_lesson_progress ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_lesson_progress FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_lesson_progress_select ON learner_lesson_progress FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_lesson_progress.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY learner_lesson_progress_insert ON learner_lesson_progress FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(learner_lesson_progress.org_id));
CREATE POLICY learner_lesson_progress_update ON learner_lesson_progress FOR UPDATE
  USING (learner_id = app_current_user_id())
  WITH CHECK (learner_id = app_current_user_id());

-- === learner_quiz_attempt ===================================================
-- Immutable submission record (answers_json), one row per attempt. Scoring
-- lives separately in learner_quiz_score so a quiz's answer key can be
-- redacted from any response containing an attempt.

CREATE TABLE learner_quiz_attempt (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    quiz_block_id UUID NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
    attempt_number INT NOT NULL,
    answers_json JSONB NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (learner_id, quiz_block_id, attempt_number)
);
CREATE INDEX learner_quiz_attempt_org_idx ON learner_quiz_attempt (org_id);
CREATE INDEX learner_quiz_attempt_learner_idx ON learner_quiz_attempt (learner_id, quiz_block_id);

ALTER TABLE learner_quiz_attempt ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_quiz_attempt FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_quiz_attempt_select ON learner_quiz_attempt FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_quiz_attempt.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY learner_quiz_attempt_insert ON learner_quiz_attempt FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(learner_quiz_attempt.org_id));

-- === learner_quiz_score ======================================================
-- Auto-scored (equal-weight, no partial credit for essay/manual review —
-- see grilling-record.md Q2) immediately on attempt submission.

CREATE TABLE learner_quiz_score (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    quiz_block_id UUID NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
    attempt_number INT NOT NULL,
    score_earned INT NOT NULL,
    score_max INT NOT NULL,
    percentage NUMERIC(5,2) NOT NULL,
    passed BOOLEAN NOT NULL,
    graded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (learner_id, quiz_block_id, attempt_number)
);
CREATE INDEX learner_quiz_score_org_idx ON learner_quiz_score (org_id);
CREATE INDEX learner_quiz_score_learner_idx ON learner_quiz_score (learner_id, quiz_block_id);

ALTER TABLE learner_quiz_score ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_quiz_score FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_quiz_score_select ON learner_quiz_score FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_quiz_score.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY learner_quiz_score_insert ON learner_quiz_score FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(learner_quiz_score.org_id));

-- === learner_assignment_submission ==========================================

CREATE TABLE learner_assignment_submission (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    assignment_block_id UUID NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
    submission_number INT NOT NULL,
    file_path TEXT NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    submission_status TEXT NOT NULL DEFAULT 'submitted'
      CHECK (submission_status IN ('submitted', 'graded', 'resubmitted')),
    due_date_status TEXT NOT NULL DEFAULT 'on_time'
      CHECK (due_date_status IN ('on_time', 'late')),
    UNIQUE (learner_id, assignment_block_id, submission_number)
);
CREATE INDEX learner_assignment_submission_org_idx ON learner_assignment_submission (org_id);
CREATE INDEX learner_assignment_submission_learner_idx ON learner_assignment_submission (learner_id, assignment_block_id);

ALTER TABLE learner_assignment_submission ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_assignment_submission FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_assignment_submission_select ON learner_assignment_submission FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_assignment_submission.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY learner_assignment_submission_insert ON learner_assignment_submission FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(learner_assignment_submission.org_id));
CREATE POLICY learner_assignment_submission_update ON learner_assignment_submission FOR UPDATE
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_assignment_submission.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );

-- === learner_assignment_grade ===============================================
-- Teacher-authored, keyed to a submission rather than a learner directly;
-- learner read-access is expressed via a join back to the owning
-- submission's learner_id since this table has no learner_id column of
-- its own.

CREATE TABLE learner_assignment_grade (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    submission_id UUID NOT NULL REFERENCES learner_assignment_submission(id) ON DELETE CASCADE,
    grade_percentage NUMERIC(5,2) NOT NULL,
    feedback_text TEXT,
    graded_by_teacher_id UUID NOT NULL REFERENCES profiles(id),
    graded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (submission_id)
);
CREATE INDEX learner_assignment_grade_org_idx ON learner_assignment_grade (org_id);
CREATE INDEX learner_assignment_grade_submission_idx ON learner_assignment_grade (submission_id);

ALTER TABLE learner_assignment_grade ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_assignment_grade FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_assignment_grade_select ON learner_assignment_grade FOR SELECT
  USING (
    (is_org_member(learner_assignment_grade.org_id) AND app_current_role() IN ('owner', 'teacher'))
    OR EXISTS (
      SELECT 1 FROM learner_assignment_submission s
      WHERE s.id = learner_assignment_grade.submission_id
        AND s.learner_id = app_current_user_id()
    )
  );
CREATE POLICY learner_assignment_grade_insert ON learner_assignment_grade FOR INSERT
  WITH CHECK (
    is_org_member(learner_assignment_grade.org_id)
    AND app_current_role() IN ('owner', 'teacher')
    AND graded_by_teacher_id = app_current_user_id()
  );
CREATE POLICY learner_assignment_grade_update ON learner_assignment_grade FOR UPDATE
  USING (is_org_member(learner_assignment_grade.org_id) AND app_current_role() IN ('owner', 'teacher'))
  WITH CHECK (is_org_member(learner_assignment_grade.org_id) AND app_current_role() IN ('owner', 'teacher'));

-- === course_announcement =====================================================
-- No learner scope: teacher/owner authored, readable by every org member
-- (matches Task 4's shared-content RLS convention, e.g. courses_select).

CREATE TABLE course_announcement (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES profiles(id),
    published_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX course_announcement_org_idx ON course_announcement (org_id);
CREATE INDEX course_announcement_course_idx ON course_announcement (course_id, published_at DESC);

ALTER TABLE course_announcement ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_announcement FORCE ROW LEVEL SECURITY;

CREATE POLICY course_announcement_select ON course_announcement FOR SELECT
  USING (is_org_member(course_announcement.org_id));
CREATE POLICY course_announcement_insert ON course_announcement FOR INSERT
  WITH CHECK (
    is_org_member(course_announcement.org_id)
    AND app_current_role() IN ('owner', 'teacher')
    AND created_by = app_current_user_id()
  );
CREATE POLICY course_announcement_update ON course_announcement FOR UPDATE
  USING (is_org_member(course_announcement.org_id) AND app_current_role() IN ('owner', 'teacher'))
  WITH CHECK (is_org_member(course_announcement.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY course_announcement_delete ON course_announcement FOR DELETE
  USING (is_org_member(course_announcement.org_id) AND app_current_role() IN ('owner', 'teacher'));

-- === learner_certificate =====================================================
-- Auto-issued (never learner-triggerable directly — see grilling-record.md
-- Q6) when course_completion_rules evaluation passes on a learner's own
-- request; INSERT policy still requires learner_id = app_current_user_id()
-- because the row is created inside that learner's authenticated request
-- context, not because the learner calls an issuance endpoint directly.
-- Public, unauthenticated certificate verification (Stage 6) will need a
-- SECURITY DEFINER lookup function analogous to find_api_token_by_prefix,
-- since app_current_user_id() is NULL for anonymous verification requests.

CREATE TABLE learner_certificate (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    certificate_id TEXT NOT NULL UNIQUE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    pdf_storage_path TEXT NOT NULL,
    UNIQUE (learner_id, course_id)
);
CREATE INDEX learner_certificate_org_idx ON learner_certificate (org_id);
CREATE INDEX learner_certificate_learner_idx ON learner_certificate (learner_id);
CREATE INDEX learner_certificate_course_idx ON learner_certificate (course_id);

ALTER TABLE learner_certificate ENABLE ROW LEVEL SECURITY;
ALTER TABLE learner_certificate FORCE ROW LEVEL SECURITY;

CREATE POLICY learner_certificate_select ON learner_certificate FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_certificate.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY learner_certificate_insert ON learner_certificate FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(learner_certificate.org_id));

-- === lessons: video watch-completion threshold ==============================
-- Nullable; NULL falls back to a hardcoded 80% default applied in Go
-- (see grilling-record.md Q3) rather than a second config table.

ALTER TABLE lessons ADD COLUMN watch_threshold_percent INT;

-- === blocks: add 'assignment' as a 6th block type ============================
-- See grilling-record.md Q1 — Task 4 built exactly 5 block types; this
-- task's spec requires learners to submit files against an "assignment"
-- block, so the CHECK constraint is widened here.

ALTER TABLE blocks DROP CONSTRAINT blocks_type_check;
ALTER TABLE blocks ADD CONSTRAINT blocks_type_check
  CHECK (type IN ('text', 'image', 'video', 'file', 'quiz', 'assignment'));

-- === profiles: notification opt-out ==========================================
-- Consulted by Stage 7's async notification handlers (Resend + Redis;
-- see grilling-record.md Q7) before sending any email.

ALTER TABLE profiles ADD COLUMN notification_opt_out BOOLEAN NOT NULL DEFAULT false;
