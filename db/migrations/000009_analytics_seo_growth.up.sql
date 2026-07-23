-- Task 8: analytics events + aggregation, cross-entity search, SEO
-- surfaces, org branding/theme, a landing-page builder, and custom
-- domains.

-- === is_org_teacher() ====================================================
-- Analytics (creator dashboards) are visible to owners and teachers, not
-- plain learners — mirrors is_org_moderator's shape from migration
-- 000008 rather than inlining the membership check (same infinite-
-- recursion hazard: memberships RLS policies would re-trigger themselves).
CREATE FUNCTION is_org_teacher(p_org_id UUID) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM memberships
    WHERE org_id = p_org_id AND user_id = app_current_user_id()
      AND role IN ('owner', 'teacher')
  )
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

-- === analytics_events =====================================================
-- One row per tracked event (course view, enrollment, lesson start/
-- completion, search, purchase, refund, certificate issued). Append-only;
-- analytics_daily_rollups is the aggregated read path so dashboards never
-- scan this table directly at query time.
CREATE TABLE analytics_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'course_view', 'enrollment', 'lesson_start', 'lesson_completion',
        'search', 'purchase', 'refund', 'certificate_issued'
    )),
    actor_user_id UUID REFERENCES profiles(id) ON DELETE SET NULL,
    course_id UUID REFERENCES courses(id) ON DELETE CASCADE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX analytics_events_org_created_idx ON analytics_events (org_id, created_at DESC);
CREATE INDEX analytics_events_org_course_type_idx ON analytics_events (org_id, course_id, event_type);

ALTER TABLE analytics_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_events FORCE ROW LEVEL SECURITY;

-- Any org member can emit an event (a learner's own course view/completion
-- inserts a row); only owners/teachers can read the raw event stream.
CREATE POLICY analytics_events_select ON analytics_events FOR SELECT
  USING (is_org_teacher(analytics_events.org_id) OR app_is_platform_owner());
CREATE POLICY analytics_events_insert ON analytics_events FOR INSERT
  WITH CHECK (is_org_member(analytics_events.org_id));

-- === analytics_daily_rollups ==============================================
-- Written only by the background worker (admin pool, no RLS session
-- vars), which aggregates analytics_events once per day per
-- (org, day, course, metric). course_id NULL = org-wide metric.
CREATE TABLE analytics_daily_rollups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    course_id UUID REFERENCES courses(id) ON DELETE CASCADE,
    metric TEXT NOT NULL,
    value BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, day, course_id, metric)
);
CREATE INDEX analytics_daily_rollups_org_day_idx ON analytics_daily_rollups (org_id, day DESC);

ALTER TABLE analytics_daily_rollups ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_daily_rollups FORCE ROW LEVEL SECURITY;

CREATE POLICY analytics_daily_rollups_select ON analytics_daily_rollups FOR SELECT
  USING (is_org_teacher(analytics_daily_rollups.org_id) OR app_is_platform_owner());

-- === organizations: branding, theme, SEO, custom domain ==================
ALTER TABLE organizations
  ADD COLUMN logo_url TEXT,
  ADD COLUMN favicon_url TEXT,
  ADD COLUMN theme_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN meta_description TEXT NOT NULL DEFAULT '',
  ADD COLUMN og_image_url TEXT,
  ADD COLUMN custom_domain TEXT UNIQUE,
  ADD COLUMN domain_verification_token TEXT,
  ADD COLUMN domain_verified_at TIMESTAMPTZ;

-- === org_pages =============================================================
-- The landing-page builder's storage: one row per published/draft public
-- page (org's landing page uses slug 'home' by convention; other slugs
-- are additional configurable public pages).
CREATE TABLE org_pages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    content_html TEXT NOT NULL DEFAULT '',
    is_published BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX org_pages_org_idx ON org_pages (org_id);

ALTER TABLE org_pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_pages FORCE ROW LEVEL SECURITY;

-- Public pages are readable by anyone once published — org_pages is the
-- one table in this migration meant to be queried outside any
-- authenticated/tenant-scoped transaction (the public site renderer runs
-- with no session vars set at all, so is_org_member() would evaluate
-- false and hide even published pages; the is_published branch covers
-- that anonymous path explicitly).
CREATE POLICY org_pages_select ON org_pages FOR SELECT
  USING (is_published OR is_org_teacher(org_pages.org_id) OR app_is_platform_owner());
CREATE POLICY org_pages_insert ON org_pages FOR INSERT
  WITH CHECK (is_org_teacher(org_pages.org_id));
CREATE POLICY org_pages_update ON org_pages FOR UPDATE
  USING (is_org_teacher(org_pages.org_id));
CREATE POLICY org_pages_delete ON org_pages FOR DELETE
  USING (is_org_teacher(org_pages.org_id));

-- === search: tsvector columns + GIN indexes ===============================
ALTER TABLE courses ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(description, '')), 'B')
  ) STORED;
CREATE INDEX courses_search_idx ON courses USING GIN (search_vector);

ALTER TABLE lessons ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (setweight(to_tsvector('english', coalesce(title, '')), 'A')) STORED;
CREATE INDEX lessons_search_idx ON lessons USING GIN (search_vector);

ALTER TABLE discussion_threads ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (setweight(to_tsvector('english', coalesce(title, '')), 'A')) STORED;
CREATE INDEX discussion_threads_search_idx ON discussion_threads USING GIN (search_vector);

-- === search_org_members() =================================================
-- "Search across ... users" (plan.md Task 8) means searching an org's own
-- member directory, not a global user directory. profiles_select (000002)
-- only allows a caller to see their own row, so a plain JOIN would silently
-- filter every other member out of results. This SECURITY DEFINER function
-- deliberately bypasses that restriction, but only for members of the
-- same org the caller is calling is_org_member() against — no broader
-- than what memberships_select already lets a member see about their
-- org's roster.
CREATE FUNCTION search_org_members(p_org_id UUID, p_query TEXT) RETURNS TABLE (
    user_id UUID, full_name TEXT, email TEXT
) AS $$
  SELECT p.id, p.full_name, p.email
  FROM profiles p
  JOIN memberships m ON m.user_id = p.id
  WHERE m.org_id = p_org_id
    AND is_org_member(p_org_id)
    AND (p.full_name ILIKE '%' || p_query || '%' OR p.email ILIKE '%' || p_query || '%')
  ORDER BY p.full_name
  LIMIT 20
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

-- === resolve_org_by_domain() ==============================================
-- Custom-domain public site resolution runs with no app.current_user_id/
-- app.current_org_id set at all (an anonymous visitor hitting the org's
-- own domain) — organizations_select's RLS policy would filter out every
-- row for that caller, so this bypasses it the same way
-- resolve_unsubscribe()/find_api_token_by_prefix do for their own
-- unauthenticated lookups. Only ever returns a domain that has passed
-- verification (domain_verified_at IS NOT NULL).
CREATE FUNCTION resolve_org_by_domain(p_domain TEXT) RETURNS TABLE (
    id UUID, slug TEXT, name TEXT
) AS $$
  SELECT o.id, o.slug, o.name
  FROM organizations o
  WHERE o.custom_domain = p_domain AND o.domain_verified_at IS NOT NULL
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

-- === list_published_courses() =============================================
-- Sitemap/robots/embeddable-catalog/public-landing-page rendering all run
-- as anonymous visitors with no membership — courses_select's RLS policy
-- (is_org_member only) would return zero rows for them. This function is
-- the narrow, explicit bypass: only status = 'published' rows, ever,
-- regardless of caller.
CREATE FUNCTION list_published_courses(p_org_id UUID) RETURNS TABLE (
    id UUID, title TEXT, description TEXT, cover_image_url TEXT, published_at TIMESTAMPTZ
) AS $$
  SELECT c.id, c.title, c.description, c.cover_image_url, c.published_at
  FROM courses c
  WHERE c.org_id = p_org_id AND c.status = 'published'
  ORDER BY c.published_at DESC
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;
