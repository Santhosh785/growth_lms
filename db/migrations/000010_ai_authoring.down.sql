-- Reverse of 000010_ai_authoring.up.sql. Drop children before parents;
-- policies drop with their tables.
DROP TABLE IF EXISTS ai_tutor_messages;
DROP TABLE IF EXISTS ai_tutor_sessions;
DROP TABLE IF EXISTS ai_usage_counters;
DROP TABLE IF EXISTS ai_generations;

ALTER TABLE organizations
  DROP COLUMN IF EXISTS ai_monthly_token_limit,
  DROP COLUMN IF EXISTS ai_enabled;
