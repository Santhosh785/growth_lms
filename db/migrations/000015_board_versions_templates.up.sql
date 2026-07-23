-- Task 9 (advanced learning features), "improved collaborative boards": two
-- additive enhancements over the Task 7 collab_boards whiteboard.
--
--   1. collab_board_versions — a named snapshot history for a board. The live
--      board state (collab_boards.snapshot) is last-write-wins and mutates
--      continuously; a version is an immutable point-in-time copy a member saves
--      (a checkpoint) and can later restore the board to. This makes the board
--      recoverable from a bad edit and gives a lightweight revision trail.
--
--   2. collab_board_templates — an org-level reusable starting snapshot a new
--      board can be seeded from (e.g. a retro grid, a SWOT canvas). Templates
--      are org-scoped (not tied to a course) and teacher-authored.
--
-- Both tables are tenant-scoped (org_id + RLS) and reuse the same
-- membership/role helpers as collab_boards. This is a pure extension of an
-- existing Task 7 feature, not a new flag-gated module, so there is no
-- feature-flag column and no platform kill-switch — collaborative boards are
-- already a shipped, always-on capability. org_id is denormalized onto the
-- versions table (from the parent board) so RLS stays a flat non-recursive
-- comparison.

-- === collab_board_versions ================================================
-- One saved checkpoint of a board's state. snapshot is a verbatim copy of
-- collab_boards.snapshot at save time. label is an optional human name for the
-- checkpoint. Immutable once written (no UPDATE policy); superseded checkpoints
-- are simply deleted.
CREATE TABLE collab_board_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    board_id UUID NOT NULL REFERENCES collab_boards(id) ON DELETE CASCADE,
    label TEXT NOT NULL DEFAULT '',
    snapshot JSONB NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX collab_board_versions_board_idx ON collab_board_versions (board_id, created_at DESC);
CREATE INDEX collab_board_versions_org_idx ON collab_board_versions (org_id);

ALTER TABLE collab_board_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE collab_board_versions FORCE ROW LEVEL SECURITY;

-- Any org member reads a board's version history (same visibility as the board
-- itself); any member may save a checkpoint of a board they can see.
CREATE POLICY collab_board_versions_select ON collab_board_versions FOR SELECT
  USING (is_org_member(collab_board_versions.org_id) OR app_is_platform_owner());
CREATE POLICY collab_board_versions_insert ON collab_board_versions FOR INSERT
  WITH CHECK (is_org_member(collab_board_versions.org_id) AND created_by = app_current_user_id());
-- A checkpoint can be pruned by whoever saved it or a moderator/owner (mirrors
-- the collab_boards delete policy). No UPDATE: versions are immutable.
CREATE POLICY collab_board_versions_delete ON collab_board_versions FOR DELETE
  USING (created_by = app_current_user_id() OR is_org_moderator(collab_board_versions.org_id));

-- === collab_board_templates ===============================================
-- An org-level reusable board starting point. title is unique per org so a
-- template has a stable identity. snapshot is the seed state a new board copies.
CREATE TABLE collab_board_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    snapshot JSONB NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, title)
);
CREATE INDEX collab_board_templates_org_idx ON collab_board_templates (org_id, created_at DESC);

ALTER TABLE collab_board_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE collab_board_templates FORCE ROW LEVEL SECURITY;

-- Any org member reads the org's templates (so anyone creating a board can seed
-- from one); only teachers/owners author them.
CREATE POLICY collab_board_templates_select ON collab_board_templates FOR SELECT
  USING (is_org_member(collab_board_templates.org_id) OR app_is_platform_owner());
CREATE POLICY collab_board_templates_insert ON collab_board_templates FOR INSERT
  WITH CHECK (is_org_teacher(collab_board_templates.org_id));
CREATE POLICY collab_board_templates_update ON collab_board_templates FOR UPDATE
  USING (is_org_teacher(collab_board_templates.org_id));
CREATE POLICY collab_board_templates_delete ON collab_board_templates FOR DELETE
  USING (is_org_teacher(collab_board_templates.org_id));
