-- Task 9 (advanced learning features), Podcasts & RSS module:
-- org-scoped podcast shows, their episodes (with transcripts + listen
-- duration), curated playlists, and per-learner listen progress — plus the
-- public, anonymous RSS 2.0 feed surface every podcast app subscribes to.
--
-- Like every Task 9 advanced module (see 000010_ai_authoring), this is
-- feature-flagged (organizations.podcasts_enabled AND the platform-level
-- LMS_PODCASTS_ENABLED), tenant-scoped (every table carries org_id + RLS),
-- and independently testable. Observability here is the audit-event trail
-- the handlers already write on every publish action plus the
-- podcast_progress consumption record — there is no external per-call cost
-- to meter the way the AI module's token ledger does, so no ledger table.

-- === organizations: Podcasts feature flag =================================
-- Org-owner-controlled toggle; the platform-level LMS_PODCASTS_ENABLED flag
-- (checked in the handler layer) is the operator kill-switch on top of it,
-- mirroring the AI module's two-flag gate.
ALTER TABLE organizations
  ADD COLUMN podcasts_enabled BOOLEAN NOT NULL DEFAULT false;

-- === podcast_shows ========================================================
-- A podcast series belonging to one org, optionally tied to a course. slug
-- is the stable public identifier the RSS feed URL is built from
-- (/o/:org_slug/podcasts/:show_slug/rss.xml), unique per org.
CREATE TABLE podcast_shows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID REFERENCES courses(id) ON DELETE SET NULL,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    image_url TEXT,
    language TEXT NOT NULL DEFAULT 'en',
    category TEXT NOT NULL DEFAULT '',
    is_published BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX podcast_shows_org_idx ON podcast_shows (org_id, created_at DESC);

ALTER TABLE podcast_shows ENABLE ROW LEVEL SECURITY;
ALTER TABLE podcast_shows FORCE ROW LEVEL SECURITY;

-- Any org member reads the org's shows (published-or-not) for the in-app
-- catalog; only teachers/owners author them. The public RSS feed does NOT
-- go through these policies — it reads via the SECURITY DEFINER functions
-- below, which hard-limit output to published rows regardless of caller.
CREATE POLICY podcast_shows_select ON podcast_shows FOR SELECT
  USING (is_org_member(podcast_shows.org_id) OR app_is_platform_owner());
CREATE POLICY podcast_shows_insert ON podcast_shows FOR INSERT
  WITH CHECK (is_org_teacher(podcast_shows.org_id));
CREATE POLICY podcast_shows_update ON podcast_shows FOR UPDATE
  USING (is_org_teacher(podcast_shows.org_id));
CREATE POLICY podcast_shows_delete ON podcast_shows FOR DELETE
  USING (is_org_teacher(podcast_shows.org_id));

-- === podcast_episodes =====================================================
-- One episode within a show. org_id is denormalized from the parent show so
-- the RLS policy stays a flat non-recursive comparison. audio_url is the
-- enclosure URL an RSS reader downloads; duration_seconds/audio_bytes feed
-- the <itunes:duration>/enclosure length attributes.
CREATE TABLE podcast_episodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    show_id UUID NOT NULL REFERENCES podcast_shows(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    audio_url TEXT NOT NULL,
    audio_bytes BIGINT NOT NULL DEFAULT 0,
    audio_mime_type TEXT NOT NULL DEFAULT 'audio/mpeg',
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    transcript TEXT NOT NULL DEFAULT '',
    episode_number INTEGER,
    season_number INTEGER,
    is_published BOOLEAN NOT NULL DEFAULT false,
    published_at TIMESTAMPTZ,
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX podcast_episodes_show_idx ON podcast_episodes (show_id, published_at DESC NULLS LAST);

ALTER TABLE podcast_episodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE podcast_episodes FORCE ROW LEVEL SECURITY;

CREATE POLICY podcast_episodes_select ON podcast_episodes FOR SELECT
  USING (is_org_member(podcast_episodes.org_id) OR app_is_platform_owner());
CREATE POLICY podcast_episodes_insert ON podcast_episodes FOR INSERT
  WITH CHECK (is_org_teacher(podcast_episodes.org_id));
CREATE POLICY podcast_episodes_update ON podcast_episodes FOR UPDATE
  USING (is_org_teacher(podcast_episodes.org_id));
CREATE POLICY podcast_episodes_delete ON podcast_episodes FOR DELETE
  USING (is_org_teacher(podcast_episodes.org_id));

-- === podcast_playlists ====================================================
-- A curated, ordered collection of episodes (e.g. "Onboarding series").
CREATE TABLE podcast_playlists (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_published BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX podcast_playlists_org_idx ON podcast_playlists (org_id, created_at DESC);

ALTER TABLE podcast_playlists ENABLE ROW LEVEL SECURITY;
ALTER TABLE podcast_playlists FORCE ROW LEVEL SECURITY;

CREATE POLICY podcast_playlists_select ON podcast_playlists FOR SELECT
  USING (is_org_member(podcast_playlists.org_id) OR app_is_platform_owner());
CREATE POLICY podcast_playlists_insert ON podcast_playlists FOR INSERT
  WITH CHECK (is_org_teacher(podcast_playlists.org_id));
CREATE POLICY podcast_playlists_update ON podcast_playlists FOR UPDATE
  USING (is_org_teacher(podcast_playlists.org_id));
CREATE POLICY podcast_playlists_delete ON podcast_playlists FOR DELETE
  USING (is_org_teacher(podcast_playlists.org_id));

-- === podcast_playlist_items ===============================================
-- Episode membership in a playlist with an explicit sort order. org_id is
-- denormalized for flat RLS.
CREATE TABLE podcast_playlist_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    playlist_id UUID NOT NULL REFERENCES podcast_playlists(id) ON DELETE CASCADE,
    episode_id UUID NOT NULL REFERENCES podcast_episodes(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE (playlist_id, episode_id)
);
CREATE INDEX podcast_playlist_items_playlist_idx ON podcast_playlist_items (playlist_id, sort_order);

ALTER TABLE podcast_playlist_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE podcast_playlist_items FORCE ROW LEVEL SECURITY;

CREATE POLICY podcast_playlist_items_select ON podcast_playlist_items FOR SELECT
  USING (is_org_member(podcast_playlist_items.org_id) OR app_is_platform_owner());
CREATE POLICY podcast_playlist_items_insert ON podcast_playlist_items FOR INSERT
  WITH CHECK (is_org_teacher(podcast_playlist_items.org_id));
CREATE POLICY podcast_playlist_items_update ON podcast_playlist_items FOR UPDATE
  USING (is_org_teacher(podcast_playlist_items.org_id));
CREATE POLICY podcast_playlist_items_delete ON podcast_playlist_items FOR DELETE
  USING (is_org_teacher(podcast_playlist_items.org_id));

-- === podcast_progress =====================================================
-- Per-learner listen position for one episode. Owned by the learner (flat
-- RLS on learner_id, the same shape as ai_tutor_sessions): a learner sees
-- and writes only their own progress; owners/teachers can read it for
-- oversight but never own it. org_id is denormalized for flat RLS.
CREATE TABLE podcast_progress (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    episode_id UUID NOT NULL REFERENCES podcast_episodes(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    position_seconds INTEGER NOT NULL DEFAULT 0,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    completed BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (episode_id, learner_id)
);
CREATE INDEX podcast_progress_learner_idx ON podcast_progress (learner_id, updated_at DESC);

ALTER TABLE podcast_progress ENABLE ROW LEVEL SECURITY;
ALTER TABLE podcast_progress FORCE ROW LEVEL SECURITY;

CREATE POLICY podcast_progress_select ON podcast_progress FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR is_org_teacher(podcast_progress.org_id)
    OR app_is_platform_owner()
  );
CREATE POLICY podcast_progress_insert ON podcast_progress FOR INSERT
  WITH CHECK (learner_id = app_current_user_id() AND is_org_member(podcast_progress.org_id));
CREATE POLICY podcast_progress_update ON podcast_progress FOR UPDATE
  USING (learner_id = app_current_user_id());

-- === public RSS SECURITY DEFINER functions ================================
-- The RSS feed is served to anonymous podcast apps with no membership/
-- session, so ordinary RLS (is_org_member) would return zero rows. These
-- two functions are the narrow, explicit bypass — published shows/episodes
-- only, ever, regardless of caller — exactly mirroring Task 8's
-- list_published_courses precedent.

-- The org's podcasts_enabled toggle is enforced here (not just the show's
-- is_published flag), so turning the module off for an org hides its public
-- feeds too — an anonymous RSS request has no session to read that column
-- through RLS, so the gate must live inside the SECURITY DEFINER boundary.
CREATE FUNCTION get_published_podcast_show(p_org_id UUID, p_slug TEXT) RETURNS TABLE (
    id UUID, slug TEXT, title TEXT, description TEXT, author TEXT,
    image_url TEXT, language TEXT, category TEXT, updated_at TIMESTAMPTZ
) AS $$
  SELECT s.id, s.slug, s.title, s.description, s.author,
         s.image_url, s.language, s.category, s.updated_at
  FROM podcast_shows s
  JOIN organizations o ON o.id = s.org_id
  WHERE s.org_id = p_org_id AND s.slug = p_slug
    AND s.is_published = true AND o.podcasts_enabled = true
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

CREATE FUNCTION list_published_podcast_episodes(p_show_id UUID) RETURNS TABLE (
    id UUID, title TEXT, description TEXT, audio_url TEXT, audio_bytes BIGINT,
    audio_mime_type TEXT, duration_seconds INTEGER, episode_number INTEGER,
    season_number INTEGER, published_at TIMESTAMPTZ
) AS $$
  SELECT e.id, e.title, e.description, e.audio_url, e.audio_bytes,
         e.audio_mime_type, e.duration_seconds, e.episode_number,
         e.season_number, e.published_at
  FROM podcast_episodes e
  JOIN podcast_shows s ON s.id = e.show_id
  JOIN organizations o ON o.id = s.org_id
  WHERE e.show_id = p_show_id
    AND e.is_published = true
    AND s.is_published = true
    AND o.podcasts_enabled = true
    AND e.published_at IS NOT NULL
  ORDER BY e.published_at DESC
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;
