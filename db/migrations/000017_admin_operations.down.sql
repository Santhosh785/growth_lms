-- Reverse of 000017_admin_operations.up.sql.
DROP INDEX IF EXISTS audit_events_action_idx;
DROP INDEX IF EXISTS audit_events_created_idx;

DROP FUNCTION IF EXISTS app_is_user_suspended(UUID);
DROP FUNCTION IF EXISTS admin_list_users(TEXT, BOOLEAN, INTEGER, INTEGER);
DROP FUNCTION IF EXISTS admin_set_course_status(UUID, TEXT);
DROP FUNCTION IF EXISTS admin_set_org_active(UUID, BOOLEAN, TEXT);
DROP FUNCTION IF EXISTS admin_set_user_suspended(UUID, BOOLEAN, TEXT);

ALTER TABLE organizations
  DROP COLUMN IF EXISTS deactivated_reason,
  DROP COLUMN IF EXISTS deactivated_at;

ALTER TABLE profiles
  DROP COLUMN IF EXISTS suspended_reason,
  DROP COLUMN IF EXISTS suspended_at;
