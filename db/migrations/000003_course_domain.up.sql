-- Task 4: course domain, media, authoring, and publishing.
--
-- Every table here follows Task 3's RLS convention: org_id NOT NULL,
-- RLS enabled + forced, and policies built on is_org_member(org_id) /
-- is_org_owner(org_id) (see 000002_auth_tenancy.up.sql) rather than a
-- direct comparison against app.current_org_id. Those helpers only need
-- app_current_user_id() (already set by dbctx.Begin at request start), so
-- SELECT visibility works even before a request has resolved which
-- org/role it's operating as — the same bootstrap-safe pattern
-- organizations' own RLS policy already relies on. This is what lets
-- ResolveCourseOrg look up a course by ID alone, before org context is
-- known, exactly like ResolveOrg does for organizations by slug.

ALTER TABLE organizations ADD COLUMN bunny_library_id TEXT;

-- === categories ==========================================================
-- Curated, owner-managed taxonomy: only owners create/update/delete.

CREATE TABLE categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX categories_org_idx ON categories (org_id);

ALTER TABLE categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE categories FORCE ROW LEVEL SECURITY;

CREATE POLICY categories_select ON categories FOR SELECT
  USING (is_org_member(categories.org_id));
CREATE POLICY categories_insert ON categories FOR INSERT
  WITH CHECK (is_org_owner(categories.org_id));
CREATE POLICY categories_update ON categories FOR UPDATE
  USING (is_org_owner(categories.org_id));
CREATE POLICY categories_delete ON categories FOR DELETE
  USING (is_org_owner(categories.org_id));

-- === tags =================================================================
-- Freeform get-or-create: any teacher/owner can create a tag by using it.

CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX tags_org_idx ON tags (org_id);

ALTER TABLE tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE tags FORCE ROW LEVEL SECURITY;

CREATE POLICY tags_select ON tags FOR SELECT
  USING (is_org_member(tags.org_id));
CREATE POLICY tags_insert ON tags FOR INSERT
  WITH CHECK (is_org_member(tags.org_id));

-- === courses ==============================================================

CREATE TABLE courses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT,
    cover_image_url TEXT,
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'draft'
      CHECK (status IN ('draft', 'review', 'scheduled', 'published', 'unpublished', 'archived')),
    publish_date TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    archived_at TIMESTAMPTZ
);
CREATE INDEX courses_org_idx ON courses (org_id);
CREATE INDEX courses_category_idx ON courses (category_id);
CREATE INDEX courses_status_publish_date_idx ON courses (status, publish_date)
  WHERE status = 'scheduled';

ALTER TABLE courses ENABLE ROW LEVEL SECURITY;
ALTER TABLE courses FORCE ROW LEVEL SECURITY;

CREATE POLICY courses_select ON courses FOR SELECT
  USING (is_org_member(courses.org_id));
CREATE POLICY courses_insert ON courses FOR INSERT
  WITH CHECK (is_org_member(courses.org_id));
CREATE POLICY courses_update ON courses FOR UPDATE
  USING (is_org_member(courses.org_id));
CREATE POLICY courses_delete ON courses FOR DELETE
  USING (is_org_member(courses.org_id));

-- === chapters =============================================================
-- course_id/org_id are denormalized down from the parent course so RLS
-- policies and queries never need a join to enforce/scope isolation.

CREATE TABLE chapters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    sort_order NUMERIC(20,10) NOT NULL,
    created_by UUID NOT NULL REFERENCES profiles(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX chapters_course_idx ON chapters (course_id, sort_order);
CREATE INDEX chapters_org_idx ON chapters (org_id);

ALTER TABLE chapters ENABLE ROW LEVEL SECURITY;
ALTER TABLE chapters FORCE ROW LEVEL SECURITY;

CREATE POLICY chapters_select ON chapters FOR SELECT
  USING (is_org_member(chapters.org_id));
CREATE POLICY chapters_insert ON chapters FOR INSERT
  WITH CHECK (is_org_member(chapters.org_id));
CREATE POLICY chapters_update ON chapters FOR UPDATE
  USING (is_org_member(chapters.org_id));
CREATE POLICY chapters_delete ON chapters FOR DELETE
  USING (is_org_member(chapters.org_id));

-- === lessons ===============================================================

CREATE TABLE lessons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chapter_id UUID NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    sort_order NUMERIC(20,10) NOT NULL,
    created_by UUID NOT NULL REFERENCES profiles(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX lessons_chapter_idx ON lessons (chapter_id, sort_order);
CREATE INDEX lessons_course_idx ON lessons (course_id);
CREATE INDEX lessons_org_idx ON lessons (org_id);

ALTER TABLE lessons ENABLE ROW LEVEL SECURITY;
ALTER TABLE lessons FORCE ROW LEVEL SECURITY;

CREATE POLICY lessons_select ON lessons FOR SELECT
  USING (is_org_member(lessons.org_id));
CREATE POLICY lessons_insert ON lessons FOR INSERT
  WITH CHECK (is_org_member(lessons.org_id));
CREATE POLICY lessons_update ON lessons FOR UPDATE
  USING (is_org_member(lessons.org_id));
CREATE POLICY lessons_delete ON lessons FOR DELETE
  USING (is_org_member(lessons.org_id));

-- === assets ================================================================
-- Created before blocks: blocks.content JSONB references assets.id, and
-- while that reference isn't a hard FK (it lives inside JSONB), assets
-- must exist first for readability of the migration's dependency order.

CREATE TABLE assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('image', 'video', 'file')),
    filename TEXT NOT NULL,
    size_bytes BIGINT,
    mime_type TEXT,
    storage_provider TEXT NOT NULL CHECK (storage_provider IN ('bunny', 'supabase')),
    storage_key TEXT NOT NULL,
    signed_url TEXT,
    signed_url_expires_at TIMESTAMPTZ,
    processing_status TEXT NOT NULL DEFAULT 'ready'
      CHECK (processing_status IN ('pending', 'processing', 'ready', 'failed')),
    duration_seconds INT,
    thumbnail_url TEXT,
    created_by UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX assets_org_idx ON assets (org_id);
CREATE INDEX assets_course_idx ON assets (course_id);

ALTER TABLE assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE assets FORCE ROW LEVEL SECURITY;

CREATE POLICY assets_select ON assets FOR SELECT
  USING (is_org_member(assets.org_id));
CREATE POLICY assets_insert ON assets FOR INSERT
  WITH CHECK (is_org_member(assets.org_id));
CREATE POLICY assets_update ON assets FOR UPDATE
  USING (is_org_member(assets.org_id));
CREATE POLICY assets_delete ON assets FOR DELETE
  USING (is_org_member(assets.org_id));

-- === blocks ================================================================

CREATE TABLE blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id UUID NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('text', 'image', 'video', 'file', 'quiz')),
    content JSONB NOT NULL DEFAULT '{}'::jsonb,
    sort_order NUMERIC(20,10) NOT NULL,
    created_by UUID NOT NULL REFERENCES profiles(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX blocks_lesson_idx ON blocks (lesson_id, sort_order);
CREATE INDEX blocks_course_idx ON blocks (course_id);
CREATE INDEX blocks_org_idx ON blocks (org_id);

ALTER TABLE blocks ENABLE ROW LEVEL SECURITY;
ALTER TABLE blocks FORCE ROW LEVEL SECURITY;

CREATE POLICY blocks_select ON blocks FOR SELECT
  USING (is_org_member(blocks.org_id));
CREATE POLICY blocks_insert ON blocks FOR INSERT
  WITH CHECK (is_org_member(blocks.org_id));
CREATE POLICY blocks_update ON blocks FOR UPDATE
  USING (is_org_member(blocks.org_id));
CREATE POLICY blocks_delete ON blocks FOR DELETE
  USING (is_org_member(blocks.org_id));

-- === course_tags junction ==================================================

CREATE TABLE course_tags (
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    PRIMARY KEY (course_id, tag_id)
);
CREATE INDEX course_tags_org_idx ON course_tags (org_id);
CREATE INDEX course_tags_tag_idx ON course_tags (tag_id);

ALTER TABLE course_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_tags FORCE ROW LEVEL SECURITY;

CREATE POLICY course_tags_select ON course_tags FOR SELECT
  USING (is_org_member(course_tags.org_id));
CREATE POLICY course_tags_insert ON course_tags FOR INSERT
  WITH CHECK (is_org_member(course_tags.org_id));
CREATE POLICY course_tags_delete ON course_tags FOR DELETE
  USING (is_org_member(course_tags.org_id));

-- === course_versions ========================================================

CREATE TABLE course_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version_number INT NOT NULL,
    snapshot JSONB NOT NULL,
    created_by UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (course_id, version_number)
);
CREATE INDEX course_versions_course_idx ON course_versions (course_id, version_number DESC);
CREATE INDEX course_versions_org_idx ON course_versions (org_id);

ALTER TABLE course_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_versions FORCE ROW LEVEL SECURITY;

CREATE POLICY course_versions_select ON course_versions FOR SELECT
  USING (is_org_member(course_versions.org_id));
CREATE POLICY course_versions_insert ON course_versions FOR INSERT
  WITH CHECK (is_org_member(course_versions.org_id));

-- === course_prerequisites ===================================================

CREATE TABLE course_prerequisites (
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    prerequisite_course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    PRIMARY KEY (course_id, prerequisite_course_id)
);
CREATE INDEX course_prerequisites_org_idx ON course_prerequisites (org_id);

ALTER TABLE course_prerequisites ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_prerequisites FORCE ROW LEVEL SECURITY;

CREATE POLICY course_prerequisites_select ON course_prerequisites FOR SELECT
  USING (is_org_member(course_prerequisites.org_id));
CREATE POLICY course_prerequisites_insert ON course_prerequisites FOR INSERT
  WITH CHECK (is_org_member(course_prerequisites.org_id));
CREATE POLICY course_prerequisites_delete ON course_prerequisites FOR DELETE
  USING (is_org_member(course_prerequisites.org_id));

-- === course_completion_rules ================================================

CREATE TABLE course_completion_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    rule_type TEXT NOT NULL CHECK (rule_type IN ('all_lessons', 'percent_lessons', 'all_quizzes', 'percent_quizzes')),
    threshold INT NOT NULL,
    created_by UUID NOT NULL REFERENCES profiles(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX course_completion_rules_course_idx ON course_completion_rules (course_id);
CREATE INDEX course_completion_rules_org_idx ON course_completion_rules (org_id);

ALTER TABLE course_completion_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_completion_rules FORCE ROW LEVEL SECURITY;

CREATE POLICY course_completion_rules_select ON course_completion_rules FOR SELECT
  USING (is_org_member(course_completion_rules.org_id));
CREATE POLICY course_completion_rules_insert ON course_completion_rules FOR INSERT
  WITH CHECK (is_org_member(course_completion_rules.org_id));
CREATE POLICY course_completion_rules_update ON course_completion_rules FOR UPDATE
  USING (is_org_member(course_completion_rules.org_id));
CREATE POLICY course_completion_rules_delete ON course_completion_rules FOR DELETE
  USING (is_org_member(course_completion_rules.org_id));

-- === collections ============================================================

CREATE TABLE collections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    created_by UUID NOT NULL REFERENCES profiles(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX collections_org_idx ON collections (org_id);

ALTER TABLE collections ENABLE ROW LEVEL SECURITY;
ALTER TABLE collections FORCE ROW LEVEL SECURITY;

CREATE POLICY collections_select ON collections FOR SELECT
  USING (is_org_member(collections.org_id));
CREATE POLICY collections_insert ON collections FOR INSERT
  WITH CHECK (is_org_member(collections.org_id));
CREATE POLICY collections_update ON collections FOR UPDATE
  USING (is_org_member(collections.org_id));
CREATE POLICY collections_delete ON collections FOR DELETE
  USING (is_org_member(collections.org_id));

-- === collection_courses junction ============================================

CREATE TABLE collection_courses (
    collection_id UUID NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sort_order NUMERIC(20,10) NOT NULL,
    PRIMARY KEY (collection_id, course_id)
);
CREATE INDEX collection_courses_collection_idx ON collection_courses (collection_id, sort_order);
CREATE INDEX collection_courses_org_idx ON collection_courses (org_id);

ALTER TABLE collection_courses ENABLE ROW LEVEL SECURITY;
ALTER TABLE collection_courses FORCE ROW LEVEL SECURITY;

CREATE POLICY collection_courses_select ON collection_courses FOR SELECT
  USING (is_org_member(collection_courses.org_id));
CREATE POLICY collection_courses_insert ON collection_courses FOR INSERT
  WITH CHECK (is_org_member(collection_courses.org_id));
CREATE POLICY collection_courses_update ON collection_courses FOR UPDATE
  USING (is_org_member(collection_courses.org_id));
CREATE POLICY collection_courses_delete ON collection_courses FOR DELETE
  USING (is_org_member(collection_courses.org_id));
