DROP FUNCTION IF EXISTS decline_invitation(TEXT);
DROP FUNCTION IF EXISTS accept_invitation(TEXT);
DROP FUNCTION IF EXISTS create_organization(TEXT, TEXT);
DROP FUNCTION IF EXISTS find_api_token_by_prefix(TEXT);

DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS invitations;
-- CASCADE: organizations' RLS policies reference memberships, and
-- memberships has an FK back to organizations, so neither table can be
-- dropped strictly before the other without cascading away the
-- dependent policy/constraint.
DROP TABLE IF EXISTS memberships CASCADE;
DROP TABLE IF EXISTS organizations CASCADE;
DROP FUNCTION IF EXISTS is_org_owner(UUID);
DROP FUNCTION IF EXISTS is_org_member(UUID);

DROP TRIGGER IF EXISTS on_auth_user_created ON auth.users;
DROP FUNCTION IF EXISTS handle_new_user();

-- CASCADE: profiles_select policy on profiles calls
-- app_is_platform_owner(), so the table (and its policies) must go before
-- the function can be dropped.
DROP TABLE IF EXISTS profiles CASCADE;
DROP FUNCTION IF EXISTS app_is_platform_owner();

DROP FUNCTION IF EXISTS app_current_role();
DROP FUNCTION IF EXISTS app_current_org_id();
DROP FUNCTION IF EXISTS app_current_user_id();
