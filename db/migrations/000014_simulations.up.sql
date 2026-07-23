-- Task 9 (advanced learning features), Interactive simulations & diagrams:
-- teacher-authored interactive content — a parameterized simulation (input
-- controls + derived outputs the client renders live) or a diagram (a
-- mermaid/dot/excalidraw source the client renders) — plus a per-learner
-- progress record tracking interaction and completion.
--
-- Like every Task 9 advanced module (see 000010_ai_authoring / 000011_podcasts
-- / 000012_code_execution / 000013_scorm) this is feature-flagged
-- (organizations.simulations_enabled AND the platform-level
-- LMS_SIMULATIONS_ENABLED, checked in the handler layer), tenant-scoped (every
-- table carries org_id + RLS), and independently testable. There is no external
-- per-run cost to meter (rendering is entirely client-side), so — like podcasts
-- and SCORM — there is no usage-counter table; observability is the audit-event
-- trail the handlers write on authoring actions plus the progress record
-- itself. The spec/config JSON is parsed and validated in internal/simulations
-- (DB-free); this schema records the validated spec and each learner's tracked
-- interaction state. There is no anonymous surface (a learner always interacts
-- inside an authenticated session), so no SECURITY DEFINER bypass is needed.

-- === organizations: simulations feature flag ==============================
-- Org-owner-controlled toggle; the platform-level LMS_SIMULATIONS_ENABLED flag
-- (checked in the handler layer) is the operator kill-switch on top of it,
-- mirroring the AI/podcasts/code-exec/scorm two-flag gate.
ALTER TABLE organizations
  ADD COLUMN simulations_enabled BOOLEAN NOT NULL DEFAULT false;

-- === simulations ==========================================================
-- One teacher-authored interactive artifact belonging to one org, optionally
-- tied to a course/lesson. slug is a stable per-org identifier, unique per org.
-- kind is 'simulation' or 'diagram' (CHECK mirrors internal/simulations.Kind;
-- keep the two in lockstep). spec holds the validated definition (a
-- internal/simulations.Spec) the client renders; config holds the optional
-- completion/grading policy (a internal/simulations.Config).
CREATE TABLE simulations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID REFERENCES courses(id) ON DELETE SET NULL,
    lesson_id UUID REFERENCES lessons(id) ON DELETE SET NULL,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('simulation', 'diagram')),
    spec JSONB NOT NULL DEFAULT '{}'::jsonb,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_published BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX simulations_org_idx ON simulations (org_id, created_at DESC);
CREATE INDEX simulations_course_idx ON simulations (course_id);

ALTER TABLE simulations ENABLE ROW LEVEL SECURITY;
ALTER TABLE simulations FORCE ROW LEVEL SECURITY;

-- Any org member reads the org's simulations (published-or-not) for the in-app
-- catalog; only teachers/owners author them.
CREATE POLICY simulations_select ON simulations FOR SELECT
  USING (is_org_member(simulations.org_id) OR app_is_platform_owner());
CREATE POLICY simulations_insert ON simulations FOR INSERT
  WITH CHECK (is_org_teacher(simulations.org_id));
CREATE POLICY simulations_update ON simulations FOR UPDATE
  USING (is_org_teacher(simulations.org_id));
CREATE POLICY simulations_delete ON simulations FOR DELETE
  USING (is_org_teacher(simulations.org_id));

-- === simulation_progress ==================================================
-- One learner's tracked interaction state for one simulation. state is opaque
-- client state for resume (current parameter values / diagram viewport);
-- interaction_count and last_score drive reporting/completion. A single running
-- record per (simulation, learner) — retakes overwrite via upsert — so
-- (simulation_id, learner_id) is unique. Learner-owned via flat RLS on
-- learner_id (the same shape as podcast_progress / scorm_attempts): a learner
-- sees and writes only their own progress; owners/teachers read for reporting
-- but never own it. org_id is denormalized from the parent simulation so RLS
-- stays a flat non-recursive comparison.
CREATE TABLE simulation_progress (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    simulation_id UUID NOT NULL REFERENCES simulations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    state JSONB NOT NULL DEFAULT '{}'::jsonb,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    last_score DOUBLE PRECISION,
    is_complete BOOLEAN NOT NULL DEFAULT false,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (simulation_id, learner_id)
);
CREATE INDEX simulation_progress_learner_idx ON simulation_progress (learner_id, updated_at DESC);
CREATE INDEX simulation_progress_sim_idx ON simulation_progress (simulation_id, updated_at DESC);
CREATE INDEX simulation_progress_org_idx ON simulation_progress (org_id, updated_at DESC);

ALTER TABLE simulation_progress ENABLE ROW LEVEL SECURITY;
ALTER TABLE simulation_progress FORCE ROW LEVEL SECURITY;

CREATE POLICY simulation_progress_select ON simulation_progress FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR is_org_teacher(simulation_progress.org_id)
    OR app_is_platform_owner()
  );
CREATE POLICY simulation_progress_insert ON simulation_progress FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(simulation_progress.org_id));
CREATE POLICY simulation_progress_update ON simulation_progress FOR UPDATE
  USING (learner_id = app_current_user_id());
