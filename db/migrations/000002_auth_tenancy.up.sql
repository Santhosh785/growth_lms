-- Task 3: authentication, organizations, tenancy, and permissions.
--
-- Identity is owned by Supabase Auth (auth.users). Every table below is
-- protected by Postgres Row-Level Security so that tenant isolation is
-- enforced at the database layer, not only in application code. RLS
-- policies read three session-scoped settings that the Go backend sets on
-- every authenticated request via set_config(..., true) inside a
-- transaction: app.current_user_id, app.current_org_id, app.current_role.
--
-- NOTE: SECURITY DEFINER functions in this file execute with the
-- privileges of the role that runs this migration. Bootstrapping org
-- creation and invitation acceptance depend on that role being able to
-- bypass RLS (either because it is a superuser, or because it has been
-- granted BYPASSRLS). If migrations run under a restricted, non-superuser
-- role in some environment, that role must be granted BYPASSRLS for these
-- functions to work correctly.

-- === Helper functions for reading the current request's session vars ===

CREATE FUNCTION app_current_user_id() RETURNS UUID AS $$
  SELECT NULLIF(current_setting('app.current_user_id', true), '')::UUID
$$ LANGUAGE sql STABLE;

CREATE FUNCTION app_current_org_id() RETURNS UUID AS $$
  SELECT NULLIF(current_setting('app.current_org_id', true), '')::UUID
$$ LANGUAGE sql STABLE;

CREATE FUNCTION app_current_role() RETURNS TEXT AS $$
  SELECT NULLIF(current_setting('app.current_role', true), '')
$$ LANGUAGE sql STABLE;

-- === profiles ============================================================

CREATE TABLE profiles (
    id UUID PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    full_name TEXT,
    avatar_url TEXT,
    is_platform_owner BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX profiles_email_idx ON profiles (email);

CREATE FUNCTION app_is_platform_owner() RETURNS BOOLEAN AS $$
  SELECT COALESCE(
    (SELECT is_platform_owner FROM profiles WHERE id = app_current_user_id()),
    false
  )
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

ALTER TABLE profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE profiles FORCE ROW LEVEL SECURITY;

CREATE POLICY profiles_select ON profiles FOR SELECT
  USING (id = app_current_user_id() OR app_is_platform_owner());

CREATE POLICY profiles_update ON profiles FOR UPDATE
  USING (id = app_current_user_id())
  WITH CHECK (id = app_current_user_id());

-- Row creation happens exclusively via the handle_new_user trigger below;
-- no direct INSERT policy is granted to application requests.

-- Auto-provision a profiles row the instant Supabase Auth creates a user,
-- so it always exists before any FK from memberships/organizations needs
-- it (covers users created via the dashboard/Admin API too, not only
-- through our own /api/auth/register handler).
CREATE FUNCTION handle_new_user() RETURNS TRIGGER AS $$
BEGIN
  INSERT INTO public.profiles (id, email)
  VALUES (NEW.id, NEW.email)
  ON CONFLICT (id) DO NOTHING;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public;

CREATE TRIGGER on_auth_user_created
  AFTER INSERT ON auth.users
  FOR EACH ROW EXECUTE FUNCTION handle_new_user();

-- === organizations, memberships (bare tables) ===========================
--
-- Both tables are created before either one's RLS policies: organizations'
-- policies need to reference memberships (to check "is the caller a
-- member/owner of this org"), so memberships must already exist as a
-- relation by the time those policies are defined, even though
-- memberships itself has an FK back to organizations.

CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_by_user_id UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'teacher', 'learner', 'moderator')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, org_id)
);
CREATE INDEX memberships_org_idx ON memberships (org_id);
CREATE INDEX memberships_user_idx ON memberships (user_id);

-- Membership-check helpers used by every RLS policy below that needs to
-- ask "is the caller a member/owner of org X". These must be SECURITY
-- DEFINER (and thus bypass RLS entirely, since they run as this
-- migration's superuser) rather than inlining the equivalent
-- `EXISTS (SELECT 1 FROM memberships ...)` directly in each policy: a
-- policy that queries memberships from within a policy ON memberships
-- itself re-triggers that same policy for the subquery's rows, which
-- Postgres rejects as infinite recursion ("infinite recursion detected
-- in policy for relation memberships"). Routing the membership check
-- through a SECURITY DEFINER function's own query short-circuits that,
-- since the function owner (a superuser) is exempt from RLS regardless
-- of FORCE ROW LEVEL SECURITY.
CREATE FUNCTION is_org_member(p_org_id UUID) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM memberships WHERE org_id = p_org_id AND user_id = app_current_user_id()
  )
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

CREATE FUNCTION is_org_owner(p_org_id UUID) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM memberships WHERE org_id = p_org_id AND user_id = app_current_user_id() AND role = 'owner'
  )
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

-- === organizations RLS ===================================================

ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE organizations FORCE ROW LEVEL SECURITY;

CREATE POLICY organizations_select ON organizations FOR SELECT
  USING (is_org_member(organizations.id) OR app_is_platform_owner());

CREATE POLICY organizations_update ON organizations FOR UPDATE
  USING (is_org_owner(organizations.id));

CREATE POLICY organizations_delete ON organizations FOR DELETE
  USING (is_org_owner(organizations.id));

-- No direct INSERT policy: organization creation always goes through
-- create_organization() below, which atomically creates the org and its
-- first owner membership (a plain INSERT policy can't express "and also
-- create the membership row that makes this org visible to its creator").

-- === memberships RLS =====================================================

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;

CREATE POLICY memberships_select ON memberships FOR SELECT
  USING (is_org_member(memberships.org_id) OR app_is_platform_owner());

CREATE POLICY memberships_insert ON memberships FOR INSERT
  WITH CHECK (is_org_owner(memberships.org_id));

CREATE POLICY memberships_update ON memberships FOR UPDATE
  USING (is_org_owner(memberships.org_id));

CREATE POLICY memberships_delete ON memberships FOR DELETE
  USING (is_org_owner(memberships.org_id));

-- === invitations =========================================================

CREATE TABLE invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('teacher', 'learner', 'moderator')),
    invited_by_user_id UUID NOT NULL REFERENCES profiles(id),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined')),
    token TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX invitations_org_idx ON invitations (org_id);
CREATE INDEX invitations_email_idx ON invitations (email);

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;

CREATE POLICY invitations_select ON invitations FOR SELECT
  USING (is_org_member(invitations.org_id));

CREATE POLICY invitations_insert ON invitations FOR INSERT
  WITH CHECK (is_org_owner(invitations.org_id));

CREATE POLICY invitations_delete ON invitations FOR DELETE
  USING (is_org_owner(invitations.org_id));

-- No SELECT/UPDATE policy grants access by token to an unauthenticated
-- holder: accept/decline of an invitation always goes through
-- accept_invitation() / decline_invitation() below, since the acceptor
-- has no app.current_user_id session var set in the RLS sense until they
-- are themselves a member.

-- === audit_events ========================================================

CREATE TABLE audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    user_id UUID REFERENCES profiles(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT,
    resource_id UUID,
    details JSONB,
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_org_idx ON audit_events (org_id, created_at DESC);

ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_events_select ON audit_events FOR SELECT
  USING (
    (org_id IS NOT NULL AND is_org_member(org_id))
    OR app_is_platform_owner()
  );

CREATE POLICY audit_events_insert ON audit_events FOR INSERT
  WITH CHECK (org_id IS NULL OR is_org_member(org_id));

-- === api_tokens ==========================================================

CREATE TABLE api_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    token_prefix TEXT NOT NULL,
    created_by_user_id UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);
CREATE INDEX api_tokens_org_idx ON api_tokens (org_id);
CREATE INDEX api_tokens_prefix_idx ON api_tokens (token_prefix);

ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY api_tokens_select ON api_tokens FOR SELECT
  USING (is_org_owner(api_tokens.org_id));

CREATE POLICY api_tokens_insert ON api_tokens FOR INSERT
  WITH CHECK (is_org_owner(api_tokens.org_id));

CREATE POLICY api_tokens_update ON api_tokens FOR UPDATE
  USING (is_org_owner(api_tokens.org_id));

CREATE POLICY api_tokens_delete ON api_tokens FOR DELETE
  USING (is_org_owner(api_tokens.org_id));

-- Looks up an unrevoked api_tokens row by its prefix, bypassing RLS. This
-- is the one legitimate case for reading api_tokens with no session
-- variables set at all: API-token authentication resolves the caller's
-- identity (org/role) FROM the token itself, so by definition no
-- app.current_user_id exists yet when this lookup runs. The Go caller
-- still bcrypt-compares the secret against token_hash before trusting the
-- result — this function only narrows the search, it doesn't authenticate.
CREATE FUNCTION find_api_token_by_prefix(p_prefix TEXT) RETURNS api_tokens
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT * FROM api_tokens WHERE token_prefix = p_prefix AND revoked_at IS NULL
$$;

-- === Bootstrap functions (bypass the chicken-and-egg RLS gaps) ==========

-- Atomically creates an organization and its first owner membership. Any
-- authenticated user may call this (self-service multi-tenancy); the org
-- would otherwise be invisible to its own creator under organizations_select
-- until a membership row exists, and a plain INSERT policy on organizations
-- cannot also create that membership row in the same statement.
CREATE FUNCTION create_organization(p_name TEXT, p_slug TEXT) RETURNS organizations
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE
  v_user_id UUID := app_current_user_id();
  v_org organizations;
BEGIN
  IF v_user_id IS NULL THEN
    RAISE EXCEPTION 'create_organization requires an authenticated user';
  END IF;

  INSERT INTO organizations (slug, name, created_by_user_id)
  VALUES (p_slug, p_name, v_user_id)
  RETURNING * INTO v_org;

  INSERT INTO memberships (user_id, org_id, role)
  VALUES (v_user_id, v_org.id, 'owner');

  RETURN v_org;
END;
$$;

-- Validates an invitation token against the currently authenticated user's
-- email, creates the membership, and marks the invitation accepted. Runs
-- as SECURITY DEFINER because the acceptor is not yet a member of the org
-- (memberships_insert's ordinary RLS check would otherwise reject them).
CREATE FUNCTION accept_invitation(p_token TEXT) RETURNS memberships
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE
  v_user_id UUID := app_current_user_id();
  v_user_email TEXT;
  v_invitation invitations;
  v_membership memberships;
BEGIN
  IF v_user_id IS NULL THEN
    RAISE EXCEPTION 'accept_invitation requires an authenticated user';
  END IF;

  SELECT email INTO v_user_email FROM profiles WHERE id = v_user_id;

  SELECT * INTO v_invitation FROM invitations
  WHERE token = p_token AND status = 'pending' AND expires_at > now()
  FOR UPDATE;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'invitation not found, expired, or already resolved';
  END IF;

  IF lower(v_invitation.email) <> lower(v_user_email) THEN
    RAISE EXCEPTION 'invitation email does not match the authenticated user';
  END IF;

  INSERT INTO memberships (user_id, org_id, role)
  VALUES (v_user_id, v_invitation.org_id, v_invitation.role)
  ON CONFLICT (user_id, org_id) DO UPDATE SET role = EXCLUDED.role
  RETURNING * INTO v_membership;

  UPDATE invitations SET status = 'accepted' WHERE id = v_invitation.id;

  RETURN v_membership;
END;
$$;

-- Declines an invitation on behalf of the currently authenticated user,
-- matched by email rather than membership (the decliner never becomes a
-- member, so ordinary RLS on invitations would hide the row from them).
CREATE FUNCTION decline_invitation(p_token TEXT) RETURNS invitations
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE
  v_user_id UUID := app_current_user_id();
  v_user_email TEXT;
  v_invitation invitations;
BEGIN
  IF v_user_id IS NULL THEN
    RAISE EXCEPTION 'decline_invitation requires an authenticated user';
  END IF;

  SELECT email INTO v_user_email FROM profiles WHERE id = v_user_id;

  SELECT * INTO v_invitation FROM invitations
  WHERE token = p_token AND status = 'pending' AND expires_at > now()
  FOR UPDATE;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'invitation not found, expired, or already resolved';
  END IF;

  IF lower(v_invitation.email) <> lower(v_user_email) THEN
    RAISE EXCEPTION 'invitation email does not match the authenticated user';
  END IF;

  UPDATE invitations SET status = 'declined' WHERE id = v_invitation.id
  RETURNING * INTO v_invitation;

  RETURN v_invitation;
END;
$$;
