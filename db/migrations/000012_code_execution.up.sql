-- Task 9 (advanced learning features), Sandboxed code execution module:
-- teacher-authored coding exercises, an append-only submission ledger that
-- doubles as this module's usage record, and a per-org daily execution
-- counter for cap enforcement.
--
-- Like every Task 9 advanced module (see 000010_ai_authoring / 000011_podcasts)
-- this is feature-flagged (organizations.code_exec_enabled AND the platform
-- level LMS_CODE_EXEC_ENABLED), tenant-scoped (every table carries org_id +
-- RLS), and observable (every run — success, failure, timeout, OOM, or a run
-- blocked by the daily cap — is written to code_submissions). The actual CPU/
-- memory/time/network/filesystem sandboxing lives in the runner backend
-- (internal/codeexec); this schema records what each run asked for and what
-- it produced. There is no anonymous surface, so — unlike podcasts — no
-- SECURITY DEFINER bypass functions are needed.

-- === organizations: code execution feature flag + per-org daily cap =======
-- code_exec_enabled is the org-owner-controlled toggle; the platform-level
-- LMS_CODE_EXEC_ENABLED flag (checked in the handler layer) is the operator
-- kill-switch on top of it, mirroring the AI/podcasts two-flag gate.
-- code_exec_daily_limit is an optional per-org override of the platform
-- default daily execution cap (NULL = use LMS_CODE_EXEC_DAILY_LIMIT).
-- code_exec_daily_limit is BIGINT to match the Go int64 representation and the
-- sibling ai_monthly_token_limit column (migration 000010), avoiding a silent
-- 32-bit ceiling on a field typed 64-bit everywhere else.
ALTER TABLE organizations
  ADD COLUMN code_exec_enabled BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN code_exec_daily_limit BIGINT;

-- === code_exercises =======================================================
-- A coding exercise belonging to one org, optionally tied to a course/lesson.
-- slug is a stable per-org identifier. The per-exercise resource limits are
-- the ceiling the runner enforces for a submission; the handler clamps them
-- to the platform maxima so an author can only ever request equal-or-less
-- than the operator allows.
CREATE TABLE code_exercises (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID REFERENCES courses(id) ON DELETE SET NULL,
    lesson_id UUID REFERENCES lessons(id) ON DELETE SET NULL,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL,
    starter_code TEXT NOT NULL DEFAULT '',
    solution_code TEXT NOT NULL DEFAULT '',
    -- Reference stdin fed to the program and the stdout expected of a correct
    -- solution; the handler compares a submission's stdout against this.
    stdin TEXT NOT NULL DEFAULT '',
    expected_output TEXT NOT NULL DEFAULT '',
    cpu_millis_limit INTEGER NOT NULL DEFAULT 5000,
    memory_bytes_limit BIGINT NOT NULL DEFAULT 268435456,
    wall_time_millis_limit INTEGER NOT NULL DEFAULT 10000,
    is_published BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX code_exercises_org_idx ON code_exercises (org_id, created_at DESC);
CREATE INDEX code_exercises_course_idx ON code_exercises (course_id);

ALTER TABLE code_exercises ENABLE ROW LEVEL SECURITY;
ALTER TABLE code_exercises FORCE ROW LEVEL SECURITY;

-- Any org member reads the org's exercises (published-or-not) for the in-app
-- catalog; only teachers/owners author them. solution_code is only ever
-- served to authors by the handler layer, never to learners.
CREATE POLICY code_exercises_select ON code_exercises FOR SELECT
  USING (is_org_member(code_exercises.org_id) OR app_is_platform_owner());
CREATE POLICY code_exercises_insert ON code_exercises FOR INSERT
  WITH CHECK (is_org_teacher(code_exercises.org_id));
CREATE POLICY code_exercises_update ON code_exercises FOR UPDATE
  USING (is_org_teacher(code_exercises.org_id));
CREATE POLICY code_exercises_delete ON code_exercises FOR DELETE
  USING (is_org_teacher(code_exercises.org_id));

-- === code_submissions =====================================================
-- One append-only row per execution — an exercise submission or an ad-hoc
-- run. This is both the learner-visible result record AND this module's
-- observability ledger: rows are written even when a run is blocked by the
-- daily cap (status = 'blocked_limit') or the runner errors (status =
-- 'error'), so the ledger is a complete audit of every attempt. exercise_id
-- is NULL for an ad-hoc run. learner_id is denormalized so RLS stays a flat
-- non-recursive comparison (the same shape as ai_tutor_sessions /
-- podcast_progress).
CREATE TABLE code_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    exercise_id UUID REFERENCES code_exercises(id) ON DELETE SET NULL,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    language TEXT NOT NULL,
    source TEXT NOT NULL,
    stdin TEXT NOT NULL DEFAULT '',
    stdout TEXT NOT NULL DEFAULT '',
    stderr TEXT NOT NULL DEFAULT '',
    exit_code INTEGER NOT NULL DEFAULT 0,
    duration_millis INTEGER NOT NULL DEFAULT 0,
    memory_kb INTEGER NOT NULL DEFAULT 0,
    runner TEXT NOT NULL DEFAULT '',
    -- succeeded: ran and (for a graded submission) matched expected output.
    -- failed: ran but wrong output / non-zero exit. timeout / oom: killed by
    -- a resource limit. error: the runner itself failed. blocked_limit: the
    -- daily cap rejected the run before it reached the runner.
    status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'timeout', 'oom', 'error', 'blocked_limit')),
    passed BOOLEAN NOT NULL DEFAULT false,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX code_submissions_learner_idx ON code_submissions (learner_id, created_at DESC);
CREATE INDEX code_submissions_org_created_idx ON code_submissions (org_id, created_at DESC);
CREATE INDEX code_submissions_exercise_idx ON code_submissions (exercise_id, created_at DESC);

ALTER TABLE code_submissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE code_submissions FORCE ROW LEVEL SECURITY;

-- A learner sees only their own submissions; owners/teachers can read the
-- whole org's submissions for oversight/grading but never own them. Any org
-- member may insert (a learner records their own run). Submissions are
-- immutable — no UPDATE/DELETE policy.
CREATE POLICY code_submissions_select ON code_submissions FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR is_org_teacher(code_submissions.org_id)
    OR app_is_platform_owner()
  );
CREATE POLICY code_submissions_insert ON code_submissions FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(code_submissions.org_id));

-- === code_exec_usage_counters =============================================
-- One row per (org, calendar day) counting executions, so the daily cap
-- check is a single indexed lookup rather than a scan of code_submissions.
-- Incremented in the same request transaction as the run it accounts for.
-- Mirrors ai_usage_counters, keyed by day instead of month.
CREATE TABLE code_exec_usage_counters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    period DATE NOT NULL,
    execution_count BIGINT NOT NULL DEFAULT 0,
    cpu_millis BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, period)
);

ALTER TABLE code_exec_usage_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE code_exec_usage_counters FORCE ROW LEVEL SECURITY;

-- Members may read/insert/update the counter (the cap check and the post-run
-- increment both happen inside a member's request transaction); the dashboard
-- read is owner/teacher-only via the handler route, but the policy stays
-- member-wide so a learner's own run can bump the shared org counter.
CREATE POLICY code_exec_usage_counters_select ON code_exec_usage_counters FOR SELECT
  USING (is_org_member(code_exec_usage_counters.org_id) OR app_is_platform_owner());
CREATE POLICY code_exec_usage_counters_insert ON code_exec_usage_counters FOR INSERT
  WITH CHECK (is_org_member(code_exec_usage_counters.org_id));
CREATE POLICY code_exec_usage_counters_update ON code_exec_usage_counters FOR UPDATE
  USING (is_org_member(code_exec_usage_counters.org_id));
