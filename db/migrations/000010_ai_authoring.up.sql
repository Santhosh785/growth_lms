-- Task 9 (advanced learning features), AI authoring & tutors module:
-- per-org feature flag + usage cap, an append-only generation ledger for
-- cost tracking and prompt/version logging, a per-month usage counter for
-- limit enforcement, and course-scoped tutor conversations.
--
-- Feature-flagged (organizations.ai_enabled AND the platform-level
-- LMS_AI_ENABLED), tenant-scoped (every table carries org_id + RLS), and
-- observable (every model call is logged to ai_generations with tokens,
-- cost, prompt version, and status) per the plan's Task 9 requirements.

-- === organizations: AI feature flag + per-org usage cap ==================
-- ai_enabled is the org-owner-controlled toggle; ai_monthly_token_limit is
-- an optional per-org override of the platform default cap (NULL = use the
-- LMS_AI_MONTHLY_TOKEN_LIMIT default).
ALTER TABLE organizations
  ADD COLUMN ai_enabled BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN ai_monthly_token_limit BIGINT;

-- === ai_generations =======================================================
-- One append-only row per model call (outline/lesson/quiz authoring or a
-- tutor reply). Carries the exact prompt template version and the token/
-- cost accounting, so cost tracking and prompt/version logging both read
-- from here. Rows are written even when a call is blocked by the usage
-- limit (status = 'blocked_limit') or errors (status = 'failed'), so the
-- ledger is a complete audit of every attempt.
CREATE TABLE ai_generations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    actor_user_id UUID REFERENCES profiles(id) ON DELETE SET NULL,
    course_id UUID REFERENCES courses(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('outline', 'lesson', 'quiz', 'tutor')),
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_micros BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'blocked_limit')),
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ai_generations_org_created_idx ON ai_generations (org_id, created_at DESC);
CREATE INDEX ai_generations_actor_idx ON ai_generations (org_id, actor_user_id);

ALTER TABLE ai_generations ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_generations FORCE ROW LEVEL SECURITY;

-- Any org member can log a generation (a learner's own tutor reply inserts
-- a row); the actor can always read their own rows, and owners/teachers can
-- read the whole org ledger for the cost dashboard.
CREATE POLICY ai_generations_select ON ai_generations FOR SELECT
  USING (
    actor_user_id = app_current_user_id()
    OR is_org_teacher(ai_generations.org_id)
    OR app_is_platform_owner()
  );
CREATE POLICY ai_generations_insert ON ai_generations FOR INSERT
  WITH CHECK (is_org_member(ai_generations.org_id));

-- === ai_usage_counters ====================================================
-- One row per (org, calendar month) accumulating tokens and cost, so the
-- monthly limit check is a single indexed lookup rather than a scan of
-- ai_generations. Incremented in the same request transaction as the
-- generation it accounts for.
CREATE TABLE ai_usage_counters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    period DATE NOT NULL,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cost_micros BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, period)
);

ALTER TABLE ai_usage_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_usage_counters FORCE ROW LEVEL SECURITY;

-- Members may read/insert/update the counter (the limit check and the
-- post-call increment both happen inside a member's request transaction);
-- the dashboard read is owner/teacher only via the handler route, but the
-- policy stays member-wide so a learner's own tutor call can bump the
-- shared org counter.
CREATE POLICY ai_usage_counters_select ON ai_usage_counters FOR SELECT
  USING (is_org_member(ai_usage_counters.org_id) OR app_is_platform_owner());
CREATE POLICY ai_usage_counters_insert ON ai_usage_counters FOR INSERT
  WITH CHECK (is_org_member(ai_usage_counters.org_id));
CREATE POLICY ai_usage_counters_update ON ai_usage_counters FOR UPDATE
  USING (is_org_member(ai_usage_counters.org_id));

-- === ai_tutor_sessions ====================================================
-- A course-scoped tutor conversation belonging to one learner. learner_id
-- is denormalized (rather than resolved through an enrollment join) so the
-- RLS policy is a flat non-recursive comparison.
CREATE TABLE ai_tutor_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ai_tutor_sessions_learner_idx ON ai_tutor_sessions (learner_id, course_id, updated_at DESC);

ALTER TABLE ai_tutor_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_tutor_sessions FORCE ROW LEVEL SECURITY;

-- A learner sees and manages only their own tutor sessions; owners/teachers
-- can read them for support/oversight but do not own them.
CREATE POLICY ai_tutor_sessions_select ON ai_tutor_sessions FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR is_org_teacher(ai_tutor_sessions.org_id)
    OR app_is_platform_owner()
  );
CREATE POLICY ai_tutor_sessions_insert ON ai_tutor_sessions FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(ai_tutor_sessions.org_id));
CREATE POLICY ai_tutor_sessions_update ON ai_tutor_sessions FOR UPDATE
  USING (learner_id = app_current_user_id());

-- === ai_tutor_messages ====================================================
-- One turn (user or assistant) in a tutor session. org_id + learner_id are
-- denormalized from the parent session for the same flat-RLS reason.
CREATE TABLE ai_tutor_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES ai_tutor_sessions(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ai_tutor_messages_session_idx ON ai_tutor_messages (session_id, created_at);

ALTER TABLE ai_tutor_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_tutor_messages FORCE ROW LEVEL SECURITY;

CREATE POLICY ai_tutor_messages_select ON ai_tutor_messages FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR is_org_teacher(ai_tutor_messages.org_id)
    OR app_is_platform_owner()
  );
CREATE POLICY ai_tutor_messages_insert ON ai_tutor_messages FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(ai_tutor_messages.org_id));
