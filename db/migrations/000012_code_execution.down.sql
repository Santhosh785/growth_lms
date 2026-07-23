-- Reverse of 000012_code_execution.up.sql. Children before parents; policies
-- drop with their tables. No SECURITY DEFINER functions to drop (this module
-- has no anonymous surface).
DROP TABLE IF EXISTS code_exec_usage_counters;
DROP TABLE IF EXISTS code_submissions;
DROP TABLE IF EXISTS code_exercises;

ALTER TABLE organizations
  DROP COLUMN IF EXISTS code_exec_daily_limit,
  DROP COLUMN IF EXISTS code_exec_enabled;
