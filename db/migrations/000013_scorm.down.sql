-- Reverse of 000013_scorm.up.sql. Children before parents; policies drop with
-- their tables. No SECURITY DEFINER functions to drop (this module has no
-- anonymous surface).
DROP TABLE IF EXISTS scorm_attempts;
DROP TABLE IF EXISTS scorm_packages;

ALTER TABLE organizations
  DROP COLUMN IF EXISTS scorm_enabled;
