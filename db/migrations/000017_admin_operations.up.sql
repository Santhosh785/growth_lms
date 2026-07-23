-- Task 10 (admin console — operational administration). The read-only admin
-- dashboards (000009+/admin_ui.go) and the plans/flags/alerts surface
-- (000016) let a platform owner *see* the platform and manage limits, but they
-- cannot yet *act* on abuse: suspend a user, deactivate an organization, or
-- take a course down across org boundaries. This migration adds the state and
-- the privileged SECURITY DEFINER entry points behind those actions, plus the
-- indexes the audit-log viewer needs.
--
-- Why SECURITY DEFINER functions rather than new RLS UPDATE policies: a
-- platform owner is deliberately NOT an org member (see admin_ui.go), so the
-- existing profiles_update (self-only) and organizations_update (org-owner
-- only) policies correctly exclude them. Rather than widen those policies —
-- which would grant a platform owner blanket UPDATE on every column of every
-- profile/org — each action is a narrow, auditable function that flips exactly
-- one state column and refuses to run for a non-platform-owner. This mirrors
-- the create_organization()/accept_invitation() precedent in 000002.

-- === suspension / deactivation state ======================================

-- A suspended user keeps their row (and their content) but is blocked from
-- logging in (auth handler) and from acting on any organization (ResolveOrg).
ALTER TABLE profiles
  ADD COLUMN suspended_at     TIMESTAMPTZ,
  ADD COLUMN suspended_reason TEXT;

-- A deactivated org is frozen: its members (owners included) are refused at
-- ResolveOrg, so no course authoring, commerce, or member management can
-- proceed. A platform owner can still resolve it (for the support drill-down)
-- and reactivate it. Public catalog pages are out of scope for this column —
-- deactivation is an operational freeze, not a publishing state.
ALTER TABLE organizations
  ADD COLUMN deactivated_at     TIMESTAMPTZ,
  ADD COLUMN deactivated_reason TEXT;

-- === platform-owner administrative actions ================================

-- admin_set_user_suspended flips profiles.suspended_at for any user. Returns
-- false (no-op) if the target doesn't exist; raises if the caller is not a
-- platform owner, so the function is safe to expose to a request transaction
-- whose RLS context is a platform owner and no one else.
CREATE FUNCTION admin_set_user_suspended(p_user_id UUID, p_suspend BOOLEAN, p_reason TEXT)
  RETURNS BOOLEAN AS $$
DECLARE
  v_found BOOLEAN;
BEGIN
  IF NOT app_is_platform_owner() THEN
    RAISE EXCEPTION 'admin_set_user_suspended: caller is not a platform owner'
      USING ERRCODE = 'insufficient_privilege';
  END IF;

  UPDATE profiles
     SET suspended_at     = CASE WHEN p_suspend THEN COALESCE(suspended_at, now()) ELSE NULL END,
         suspended_reason = CASE WHEN p_suspend THEN p_reason ELSE NULL END,
         updated_at       = now()
   WHERE id = p_user_id
     -- A platform owner may not suspend themselves or another platform owner:
     -- that is a footgun that could lock every operator out of the console.
     AND is_platform_owner = false;
  GET DIAGNOSTICS v_found = ROW_COUNT;
  RETURN v_found;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public;

-- admin_set_org_active flips organizations.deactivated_at for any org.
CREATE FUNCTION admin_set_org_active(p_org_id UUID, p_active BOOLEAN, p_reason TEXT)
  RETURNS BOOLEAN AS $$
DECLARE
  v_found BOOLEAN;
BEGIN
  IF NOT app_is_platform_owner() THEN
    RAISE EXCEPTION 'admin_set_org_active: caller is not a platform owner'
      USING ERRCODE = 'insufficient_privilege';
  END IF;

  UPDATE organizations
     SET deactivated_at     = CASE WHEN p_active THEN NULL ELSE COALESCE(deactivated_at, now()) END,
         deactivated_reason = CASE WHEN p_active THEN NULL ELSE p_reason END,
         updated_at         = now()
   WHERE id = p_org_id;
  GET DIAGNOSTICS v_found = ROW_COUNT;
  RETURN v_found;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public;

-- admin_set_course_status lets a platform owner force a course's publish
-- status regardless of org membership (courses have no platform-owner RLS
-- bypass — see admin_ui.go). Used for takedown ('archived') and restore
-- ('draft'/'published'). The status CHECK on the courses table still applies.
CREATE FUNCTION admin_set_course_status(p_course_id UUID, p_status TEXT)
  RETURNS BOOLEAN AS $$
DECLARE
  v_found BOOLEAN;
BEGIN
  IF NOT app_is_platform_owner() THEN
    RAISE EXCEPTION 'admin_set_course_status: caller is not a platform owner'
      USING ERRCODE = 'insufficient_privilege';
  END IF;

  UPDATE courses
     SET status     = p_status,
         updated_at = now()
   WHERE id = p_course_id;
  GET DIAGNOSTICS v_found = ROW_COUNT;
  RETURN v_found;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public;

-- admin_list_users returns the platform-wide user directory for the admin
-- console (email search + suspended filter, keyset by created_at). SECURITY
-- DEFINER because profiles_select only exposes the caller's own row to a
-- non-platform-owner; this function gates on app_is_platform_owner() itself,
-- so it can only ever return rows to an operator.
CREATE FUNCTION admin_list_users(p_search TEXT, p_suspended_only BOOLEAN, p_limit INTEGER, p_offset INTEGER)
  RETURNS TABLE (
    id                UUID,
    email             TEXT,
    full_name         TEXT,
    is_platform_owner BOOLEAN,
    suspended_at      TIMESTAMPTZ,
    suspended_reason  TEXT,
    created_at        TIMESTAMPTZ,
    org_count         BIGINT
  ) AS $$
BEGIN
  IF NOT app_is_platform_owner() THEN
    RAISE EXCEPTION 'admin_list_users: caller is not a platform owner'
      USING ERRCODE = 'insufficient_privilege';
  END IF;

  RETURN QUERY
    SELECT p.id, p.email, p.full_name, p.is_platform_owner,
           p.suspended_at, p.suspended_reason, p.created_at,
           (SELECT count(*) FROM memberships m WHERE m.user_id = p.id) AS org_count
      FROM profiles p
     WHERE (p_search IS NULL OR p_search = '' OR p.email ILIKE '%' || p_search || '%')
       AND (NOT p_suspended_only OR p.suspended_at IS NOT NULL)
     ORDER BY p.created_at DESC
     LIMIT GREATEST(p_limit, 0) OFFSET GREATEST(p_offset, 0);
END;
$$ LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public;

-- app_is_user_suspended reports whether a user is currently suspended,
-- bypassing RLS (profiles_select only exposes the caller's own row). The login
-- handler calls this on the raw pool — before any RLS session context exists —
-- to refuse issuing a session to a suspended account.
CREATE FUNCTION app_is_user_suspended(p_user_id UUID) RETURNS BOOLEAN AS $$
  SELECT COALESCE(
    (SELECT suspended_at IS NOT NULL FROM profiles WHERE id = p_user_id),
    false
  )
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

-- === audit-log viewer indexes ============================================
-- 000002 indexed audit_events by (org_id, created_at); the platform-wide
-- viewer scans across all orgs by time and filters by action, so it needs a
-- global time index and an action index.
CREATE INDEX audit_events_created_idx ON audit_events (created_at DESC);
CREATE INDEX audit_events_action_idx  ON audit_events (action, created_at DESC);
