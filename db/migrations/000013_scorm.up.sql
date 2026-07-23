-- Task 9 (advanced learning features), SCORM 1.2 / 2004 module:
-- teacher-authored SCORM packages (a validated imsmanifest.xml + the launch
-- href of its default organization) and a per-learner runtime record — the
-- CMI data model each SCO reads/writes through the JS API adapter — that
-- doubles as this module's completion/score reporting surface.
--
-- Like every Task 9 advanced module (see 000010_ai_authoring / 000011_podcasts
-- / 000012_code_execution) this is feature-flagged (organizations.scorm_enabled
-- AND the platform-level LMS_SCORM_ENABLED), tenant-scoped (every table carries
-- org_id + RLS), and independently testable. Observability here is the
-- audit-event trail the handlers write on every authoring action plus the
-- scorm_attempts runtime record itself — a SCORM launch has no external per-run
-- cost to meter the way the AI token ledger or the code-exec daily cap do, so
-- (like podcasts) there is no usage-counter table. Package parsing/validation
-- and CMI element normalization live in internal/scorm; this schema records the
-- validated manifest metadata and each learner's tracked runtime state. There
-- is no anonymous surface (a SCO always runs inside an authenticated learner's
-- session), so no SECURITY DEFINER bypass functions are needed.

-- === organizations: SCORM feature flag ====================================
-- Org-owner-controlled toggle; the platform-level LMS_SCORM_ENABLED flag
-- (checked in the handler layer) is the operator kill-switch on top of it,
-- mirroring the AI/podcasts/code-exec two-flag gate.
ALTER TABLE organizations
  ADD COLUMN scorm_enabled BOOLEAN NOT NULL DEFAULT false;

-- === scorm_packages =======================================================
-- One imported SCORM package belonging to one org, optionally tied to a
-- course/lesson. slug is a stable per-org identifier, unique per org. version
-- is the CMI runtime the package targets ('1.2' or '2004'), detected from the
-- imsmanifest.xml at import time. launch_href is the resource href the API
-- adapter loads (resolved from the manifest's default organization). manifest
-- holds the parsed structure (organization title + item tree) for rendering a
-- table of contents. storage_path is where the extracted package assets live
-- (a Bunny/Supabase object prefix); the schema records it but the actual asset
-- storage/serving is a media-layer concern, not this migration's.
CREATE TABLE scorm_packages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID REFERENCES courses(id) ON DELETE SET NULL,
    lesson_id UUID REFERENCES lessons(id) ON DELETE SET NULL,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    -- CHECK mirrors internal/scorm.Version; keep the two in lockstep.
    version TEXT NOT NULL CHECK (version IN ('1.2', '2004')),
    identifier TEXT NOT NULL DEFAULT '',
    launch_href TEXT NOT NULL,
    storage_path TEXT NOT NULL DEFAULT '',
    -- The mastery/passing score a SCO is graded against when it reports a raw
    -- score but no explicit pass/fail (SCORM 1.2 cmi.student_data.mastery_score
    -- / a 2004 minimum). NULL = the package/SCO decides its own status.
    mastery_score DOUBLE PRECISION,
    manifest JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_published BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX scorm_packages_org_idx ON scorm_packages (org_id, created_at DESC);
CREATE INDEX scorm_packages_course_idx ON scorm_packages (course_id);

ALTER TABLE scorm_packages ENABLE ROW LEVEL SECURITY;
ALTER TABLE scorm_packages FORCE ROW LEVEL SECURITY;

-- Any org member reads the org's packages (published-or-not) for the in-app
-- catalog; only teachers/owners author them.
CREATE POLICY scorm_packages_select ON scorm_packages FOR SELECT
  USING (is_org_member(scorm_packages.org_id) OR app_is_platform_owner());
CREATE POLICY scorm_packages_insert ON scorm_packages FOR INSERT
  WITH CHECK (is_org_teacher(scorm_packages.org_id));
CREATE POLICY scorm_packages_update ON scorm_packages FOR UPDATE
  USING (is_org_teacher(scorm_packages.org_id));
CREATE POLICY scorm_packages_delete ON scorm_packages FOR DELETE
  USING (is_org_teacher(scorm_packages.org_id));

-- === scorm_attempts =======================================================
-- One learner's tracked runtime state for one package attempt. The API
-- adapter's SetValue/Commit calls are folded into this row: the denormalized
-- summary columns (lesson/completion/success status, score, time) drive
-- reporting without parsing the raw CMI blob, and cmi_data holds the full
-- element map so a suspended SCO can be resumed exactly. attempt_number lets a
-- learner retake a package; (package_id, learner_id, attempt_number) is unique.
-- Learner-owned via flat RLS on learner_id (the same shape as podcast_progress
-- / ai_tutor_sessions): a learner sees and writes only their own attempts;
-- owners/teachers read them for reporting but never own them. org_id is
-- denormalized from the parent package so RLS stays a flat non-recursive
-- comparison.
CREATE TABLE scorm_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    package_id UUID NOT NULL REFERENCES scorm_packages(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL DEFAULT 1,
    -- SCORM 1.2 cmi.core.lesson_status: passed/completed/failed/incomplete/
    -- browsed/not attempted. For a 2004 package this is derived from
    -- completion_status + success_status by internal/scorm for a uniform view.
    lesson_status TEXT NOT NULL DEFAULT 'not attempted',
    -- SCORM 2004 cmi.completion_status (completed/incomplete/not attempted/
    -- unknown) and cmi.success_status (passed/failed/unknown). Left at their
    -- defaults for a 1.2 package.
    completion_status TEXT NOT NULL DEFAULT 'unknown',
    success_status TEXT NOT NULL DEFAULT 'unknown',
    score_raw DOUBLE PRECISION,
    score_min DOUBLE PRECISION,
    score_max DOUBLE PRECISION,
    -- 2004 cmi.score.scaled, normalized to [-1, 1]; NULL for 1.2 / unset.
    score_scaled DOUBLE PRECISION,
    -- Accumulated tracked time and the last committed session time, in seconds.
    total_time_seconds INTEGER NOT NULL DEFAULT 0,
    session_time_seconds INTEGER NOT NULL DEFAULT 0,
    -- cmi.core.lesson_location / cmi.location: the resume bookmark.
    location TEXT NOT NULL DEFAULT '',
    -- cmi.suspend_data: opaque SCO state for resume (capped by the handler).
    suspend_data TEXT NOT NULL DEFAULT '',
    -- The full CMI element map (including interactions/objectives) as committed,
    -- for exact resume and detailed 2004 interaction reporting.
    cmi_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_complete BOOLEAN NOT NULL DEFAULT false,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (package_id, learner_id, attempt_number)
);
CREATE INDEX scorm_attempts_learner_idx ON scorm_attempts (learner_id, updated_at DESC);
CREATE INDEX scorm_attempts_package_idx ON scorm_attempts (package_id, updated_at DESC);
CREATE INDEX scorm_attempts_org_idx ON scorm_attempts (org_id, updated_at DESC);

ALTER TABLE scorm_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE scorm_attempts FORCE ROW LEVEL SECURITY;

CREATE POLICY scorm_attempts_select ON scorm_attempts FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR is_org_teacher(scorm_attempts.org_id)
    OR app_is_platform_owner()
  );
CREATE POLICY scorm_attempts_insert ON scorm_attempts FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(scorm_attempts.org_id));
CREATE POLICY scorm_attempts_update ON scorm_attempts FOR UPDATE
  USING (learner_id = app_current_user_id());
