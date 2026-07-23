-- Task 10 (admin console, CLI, backups, operations). This migration adds the
-- data model behind four operator-facing surfaces that the read-only admin
-- dashboards (000009+/admin_ui.go) and the operational CLI (internal/cli) do
-- not yet cover:
--
--   1. Plans & plan limits        -> plans + organizations.plan_id
--   2. Feature flags (runtime)    -> feature_flags + org_feature_flags
--   3. Usage & quota management   -> (computed on demand from plans + live
--                                     row counts; see internal/quota — no new
--                                     counter table, the existing per-feature
--                                     usage counters + COUNT(*) are the source)
--   4. Observability & alerting   -> system_alerts
--
-- Everything here is either platform-global (plans, feature_flags: written
-- only by a platform owner, mirroring platform_settings in
-- 000006_commerce.up.sql) or tenant-scoped with RLS (org_feature_flags,
-- system_alerts). Plans are readable by any authenticated session so an org
-- owner can see the limits of the plan they're on; the writes are all
-- platform-owner-gated both by RLS and by middleware.RequirePlatformOwner at
-- the route layer.

-- === plans ================================================================
-- Platform-wide catalog of subscription plans. Limit columns are nullable;
-- NULL means "unlimited" for that dimension (internal/quota treats a NULL /
-- non-positive limit as no cap, matching how AIConfig.MonthlyTokenLimit <= 0
-- already means unlimited). price_cents/currency are informational for the
-- admin UI — billing enforcement is out of scope for this task; the plan is a
-- limits envelope, not a Razorpay subscription.
CREATE TABLE plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    max_courses INTEGER,
    max_published_courses INTEGER,
    max_members INTEGER,
    max_storage_bytes BIGINT,
    max_ai_tokens_month BIGINT,
    price_cents BIGINT NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'INR',
    is_default BOOLEAN NOT NULL DEFAULT false,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one plan may be the default (the plan new orgs and orgs with a NULL
-- plan_id fall back to). A partial unique index enforces the invariant without
-- forbidding many non-default plans.
CREATE UNIQUE INDEX plans_single_default_idx ON plans (is_default) WHERE is_default;

ALTER TABLE plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE plans FORCE ROW LEVEL SECURITY;

-- Any authenticated org member can read the catalog (to see their own plan's
-- limits on the org admin page); only a platform owner mutates it.
CREATE POLICY plans_select ON plans FOR SELECT
  USING (app_current_user_id() IS NOT NULL);
CREATE POLICY plans_insert ON plans FOR INSERT
  WITH CHECK (app_is_platform_owner());
CREATE POLICY plans_update ON plans FOR UPDATE
  USING (app_is_platform_owner());
CREATE POLICY plans_delete ON plans FOR DELETE
  USING (app_is_platform_owner());

-- === organizations.plan_id ===============================================
-- An org's assigned plan. NULL is legal and resolves to the default plan at
-- read time (internal/quota / PlanRepo.ResolveForOrg), so a newly created org
-- needs no plan-assignment step to have working limits. ON DELETE SET NULL so
-- retiring a plan doesn't cascade-delete orgs.
ALTER TABLE organizations
  ADD COLUMN plan_id UUID REFERENCES plans(id) ON DELETE SET NULL;

-- === feature_flags ========================================================
-- Runtime, operator-managed feature flags. This is the dynamic complement to
-- the compile/env-time platform flags in config (LMS_AI_ENABLED etc.): those
-- gate whole subsystems and require a redeploy to change; these are cheap
-- named booleans a platform owner flips live from the admin console. A flag's
-- effective value for an org is: the org's override in org_feature_flags if
-- one exists, else this default_enabled. Keyed by a stable string `key`.
CREATE TABLE feature_flags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    default_enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE feature_flags ENABLE ROW LEVEL SECURITY;
ALTER TABLE feature_flags FORCE ROW LEVEL SECURITY;

CREATE POLICY feature_flags_select ON feature_flags FOR SELECT
  USING (app_current_user_id() IS NOT NULL);
CREATE POLICY feature_flags_insert ON feature_flags FOR INSERT
  WITH CHECK (app_is_platform_owner());
CREATE POLICY feature_flags_update ON feature_flags FOR UPDATE
  USING (app_is_platform_owner());
CREATE POLICY feature_flags_delete ON feature_flags FOR DELETE
  USING (app_is_platform_owner());

-- === org_feature_flags ====================================================
-- Per-org override of a feature flag. Presence of a row means "this org's
-- value is `enabled`, ignore the flag default". An org owner may set overrides
-- for their own org; a platform owner may set them for any org. One row per
-- (org, flag).
CREATE TABLE org_feature_flags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    flag_key TEXT NOT NULL REFERENCES feature_flags(key) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL,
    updated_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, flag_key)
);
CREATE INDEX org_feature_flags_org_idx ON org_feature_flags (org_id);

ALTER TABLE org_feature_flags ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_feature_flags FORCE ROW LEVEL SECURITY;

CREATE POLICY org_feature_flags_select ON org_feature_flags FOR SELECT
  USING (is_org_member(org_feature_flags.org_id) OR app_is_platform_owner());
CREATE POLICY org_feature_flags_insert ON org_feature_flags FOR INSERT
  WITH CHECK (is_org_owner(org_feature_flags.org_id) OR app_is_platform_owner());
CREATE POLICY org_feature_flags_update ON org_feature_flags FOR UPDATE
  USING (is_org_owner(org_feature_flags.org_id) OR app_is_platform_owner());
CREATE POLICY org_feature_flags_delete ON org_feature_flags FOR DELETE
  USING (is_org_owner(org_feature_flags.org_id) OR app_is_platform_owner());

-- === system_alerts ========================================================
-- Operational alert stream (plan.md Task 10: "alerts for failed jobs, payment
-- webhooks, storage, database, and authentication errors"). Each row is one
-- surfaced operational event an operator may need to act on. org_id is
-- nullable — a failed background job or a DB-level alert may not belong to any
-- one org, while a payment-webhook or auth alert usually does. severity is one
-- of info/warning/critical; category names the subsystem. resolved_at marks an
-- operator having acknowledged/closed it.
--
-- Writes come from the application's own privileged paths (the worker's asynq
-- error handler, the webhook handler, the auth middleware) — these run either
-- as the pool's admin role (worker, no per-request RLS) or from a request path
-- that sets a service-style context, so there is deliberately NO general
-- application INSERT policy; inserts happen through the SECURITY DEFINER
-- record_system_alert() function below, which any authenticated session may
-- call but which always stamps the row itself. Reads are platform-owner-wide
-- or org-scoped.
CREATE TABLE system_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    severity TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    category TEXT NOT NULL CHECK (category IN ('job', 'webhook', 'storage', 'database', 'auth', 'other')),
    source TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    resolved_at TIMESTAMPTZ,
    resolved_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX system_alerts_open_idx ON system_alerts (created_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX system_alerts_org_idx ON system_alerts (org_id, created_at DESC);
CREATE INDEX system_alerts_category_idx ON system_alerts (category, created_at DESC);

ALTER TABLE system_alerts ENABLE ROW LEVEL SECURITY;
ALTER TABLE system_alerts FORCE ROW LEVEL SECURITY;

-- Platform owner sees every alert; an org owner sees their org's alerts only.
CREATE POLICY system_alerts_select ON system_alerts FOR SELECT
  USING (
    app_is_platform_owner()
    OR (system_alerts.org_id IS NOT NULL AND is_org_owner(system_alerts.org_id))
  );
-- Only a platform owner (or an org owner for their own org) may resolve.
CREATE POLICY system_alerts_update ON system_alerts FOR UPDATE
  USING (
    app_is_platform_owner()
    OR (system_alerts.org_id IS NOT NULL AND is_org_owner(system_alerts.org_id))
  );

-- record_system_alert inserts one alert row regardless of the caller's org
-- membership — it is the single sanctioned write path (there is no INSERT
-- policy on system_alerts). SECURITY DEFINER so the worker/webhook/auth code
-- can record an alert without being an org member; the function validates its
-- own inputs against the same CHECK domains the table enforces.
CREATE FUNCTION record_system_alert(
    p_org_id UUID,
    p_severity TEXT,
    p_category TEXT,
    p_source TEXT,
    p_message TEXT,
    p_details JSONB
) RETURNS system_alerts
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
    v_row system_alerts;
BEGIN
    INSERT INTO system_alerts (org_id, severity, category, source, message, details)
    VALUES (p_org_id, p_severity, p_category, p_source, p_message, COALESCE(p_details, '{}'::jsonb))
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$$;

-- === seed default plan ====================================================
-- One default 'free' plan so every org (existing and new) has resolvable
-- limits immediately. Generous-but-finite caps make the quota machinery
-- observable in a fresh install without blocking normal MVP usage; a platform
-- owner tunes these or adds paid tiers from the admin console.
INSERT INTO plans (code, name, description, max_courses, max_published_courses,
                   max_members, max_storage_bytes, max_ai_tokens_month,
                   price_cents, currency, is_default, is_active)
VALUES ('free', 'Free', 'Default plan for new organizations.',
        25, 10, 50, 5368709120, 2000000, 0, 'INR', true, true);

-- Backfill existing orgs onto the default plan (new orgs may leave plan_id
-- NULL and still resolve to it, but stamping existing rows makes the admin
-- listing show a concrete assignment).
UPDATE organizations
SET plan_id = (SELECT id FROM plans WHERE is_default LIMIT 1)
WHERE plan_id IS NULL;

-- === seed baseline feature flags =========================================
-- A couple of runtime flags operators commonly want on day one. These are
-- illustrative and independent of the env-time subsystem flags; more are added
-- from the console.
INSERT INTO feature_flags (key, description, default_enabled) VALUES
  ('maintenance_banner', 'Show a platform-wide maintenance banner to all users.', false),
  ('new_learner_onboarding', 'Enable the redesigned learner onboarding flow.', false);
