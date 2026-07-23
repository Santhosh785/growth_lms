-- Reverse of 000016_admin_plans_flags_alerts.up.sql. Drop the alert-writer
-- function and children before parents; policies drop with their tables. The
-- organizations.plan_id column and its FK go last (after plans still exists is
-- fine — DROP COLUMN removes the dependent FK).
DROP FUNCTION IF EXISTS record_system_alert(UUID, TEXT, TEXT, TEXT, TEXT, JSONB);
DROP TABLE IF EXISTS system_alerts;
DROP TABLE IF EXISTS org_feature_flags;
DROP TABLE IF EXISTS feature_flags;

ALTER TABLE organizations
  DROP COLUMN IF EXISTS plan_id;

DROP TABLE IF EXISTS plans;
