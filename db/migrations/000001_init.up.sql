-- Sanity migration proving the golang-migrate pipeline works end to end.
-- Real domain tables (organizations, users, courses, ...) are added by
-- Task 3 onward.
CREATE TABLE IF NOT EXISTS schema_version_check (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    initialized_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT schema_version_check_singleton CHECK (id = 1)
);

INSERT INTO schema_version_check (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;
