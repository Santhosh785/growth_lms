-- Reverse of 000014_simulations.up.sql. Children before parents; policies drop
-- with their tables. No SECURITY DEFINER functions to drop (this module has no
-- anonymous surface).
DROP TABLE IF EXISTS simulation_progress;
DROP TABLE IF EXISTS simulations;

ALTER TABLE organizations
  DROP COLUMN IF EXISTS simulations_enabled;
