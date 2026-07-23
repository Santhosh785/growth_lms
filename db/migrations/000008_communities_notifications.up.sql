-- Task 7: Communities, notifications, and real-time collaboration.
--
-- Every table here is org-scoped and follows the Task 2/3 RLS recipe:
-- org_id NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, RLS
-- ENABLE + FORCE, policies built on the SECURITY DEFINER helpers
-- is_org_member() / is_org_owner() / app_current_user_id() / app_current_role().
-- Child tables denormalize org_id (and course_id where useful) so a policy
-- never needs a join. The one new access tier this task introduces is the
-- moderator: is_org_moderator() below, mirroring is_org_owner().

-- === is_org_moderator() ==================================================
-- True when the current user is an owner OR a moderator of the org. This is
-- what makes moderator power real in-DB (pin/lock threads, hide/delete
-- anyone's post, resolve reports) rather than only enforced in middleware.
-- SECURITY DEFINER + STABLE + fixed search_path, exactly like is_org_owner.
CREATE FUNCTION is_org_moderator(p_org_id UUID) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM memberships
    WHERE org_id = p_org_id AND user_id = app_current_user_id()
      AND role IN ('owner', 'moderator')
  )
$$ LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public;

-- === discussion_threads ==================================================
-- course_id NULL = org-wide community thread; course_id set = course
-- discussion. One subsystem, two scopes. Pin/lock/hide are moderator
-- actions gated by the is_org_moderator branch of the UPDATE/DELETE policy.
CREATE TABLE discussion_threads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID REFERENCES courses(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    is_locked BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'hidden', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX discussion_threads_org_course_idx
  ON discussion_threads (org_id, course_id, is_pinned DESC, updated_at DESC);

ALTER TABLE discussion_threads ENABLE ROW LEVEL SECURITY;
ALTER TABLE discussion_threads FORCE ROW LEVEL SECURITY;

CREATE POLICY discussion_threads_select ON discussion_threads FOR SELECT
  USING (is_org_member(discussion_threads.org_id) OR app_is_platform_owner());

CREATE POLICY discussion_threads_insert ON discussion_threads FOR INSERT
  WITH CHECK (is_org_member(discussion_threads.org_id) AND created_by = app_current_user_id());

CREATE POLICY discussion_threads_update ON discussion_threads FOR UPDATE
  USING (created_by = app_current_user_id() OR is_org_moderator(discussion_threads.org_id));

CREATE POLICY discussion_threads_delete ON discussion_threads FOR DELETE
  USING (created_by = app_current_user_id() OR is_org_moderator(discussion_threads.org_id));

-- === discussion_posts ====================================================
-- The first-class message. A root post has parent_post_id NULL; a reply
-- points at a root post. Only one level of nesting is allowed — enforced in
-- the handler (a reply's parent must itself be a root post).
CREATE TABLE discussion_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    thread_id UUID NOT NULL REFERENCES discussion_threads(id) ON DELETE CASCADE,
    parent_post_id UUID REFERENCES discussion_posts(id) ON DELETE CASCADE,
    author_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'visible' CHECK (status IN ('visible', 'hidden', 'deleted')),
    edited_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX discussion_posts_thread_idx ON discussion_posts (thread_id, created_at);
CREATE INDEX discussion_posts_org_idx ON discussion_posts (org_id);

ALTER TABLE discussion_posts ENABLE ROW LEVEL SECURITY;
ALTER TABLE discussion_posts FORCE ROW LEVEL SECURITY;

CREATE POLICY discussion_posts_select ON discussion_posts FOR SELECT
  USING (is_org_member(discussion_posts.org_id) OR app_is_platform_owner());

CREATE POLICY discussion_posts_insert ON discussion_posts FOR INSERT
  WITH CHECK (is_org_member(discussion_posts.org_id) AND author_id = app_current_user_id());

CREATE POLICY discussion_posts_update ON discussion_posts FOR UPDATE
  USING (author_id = app_current_user_id() OR is_org_moderator(discussion_posts.org_id));

CREATE POLICY discussion_posts_delete ON discussion_posts FOR DELETE
  USING (author_id = app_current_user_id() OR is_org_moderator(discussion_posts.org_id));

-- === post_reactions ======================================================
CREATE TABLE post_reactions (
    post_id UUID NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    emoji TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, user_id, emoji)
);
CREATE INDEX post_reactions_org_idx ON post_reactions (org_id);

ALTER TABLE post_reactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE post_reactions FORCE ROW LEVEL SECURITY;

CREATE POLICY post_reactions_select ON post_reactions FOR SELECT
  USING (is_org_member(post_reactions.org_id) OR app_is_platform_owner());

CREATE POLICY post_reactions_insert ON post_reactions FOR INSERT
  WITH CHECK (is_org_member(post_reactions.org_id) AND user_id = app_current_user_id());

-- You may only remove your own reaction.
CREATE POLICY post_reactions_delete ON post_reactions FOR DELETE
  USING (user_id = app_current_user_id());

-- === post_mentions =======================================================
-- Populated when a post body contains an @[uuid] member token. Drives
-- mention notifications. A mentioned user must be an org member.
CREATE TABLE post_mentions (
    post_id UUID NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    mentioned_user_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, mentioned_user_id)
);
CREATE INDEX post_mentions_org_idx ON post_mentions (org_id);
CREATE INDEX post_mentions_user_idx ON post_mentions (mentioned_user_id);

ALTER TABLE post_mentions ENABLE ROW LEVEL SECURITY;
ALTER TABLE post_mentions FORCE ROW LEVEL SECURITY;

CREATE POLICY post_mentions_select ON post_mentions FOR SELECT
  USING (is_org_member(post_mentions.org_id) OR app_is_platform_owner());

CREATE POLICY post_mentions_insert ON post_mentions FOR INSERT
  WITH CHECK (is_org_member(post_mentions.org_id));

-- === content_reports =====================================================
-- Any member may report a post; only moderators see the full open queue and
-- resolve/dismiss. A reporter can always see their own reports.
CREATE TABLE content_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    post_id UUID NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    reporter_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolved_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX content_reports_org_status_idx ON content_reports (org_id, status, created_at DESC);
CREATE INDEX content_reports_post_idx ON content_reports (post_id);

ALTER TABLE content_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE content_reports FORCE ROW LEVEL SECURITY;

CREATE POLICY content_reports_select ON content_reports FOR SELECT
  USING (
    reporter_id = app_current_user_id()
    OR is_org_moderator(content_reports.org_id)
    OR app_is_platform_owner()
  );

CREATE POLICY content_reports_insert ON content_reports FOR INSERT
  WITH CHECK (is_org_member(content_reports.org_id) AND reporter_id = app_current_user_id());

CREATE POLICY content_reports_update ON content_reports FOR UPDATE
  USING (is_org_moderator(content_reports.org_id));

-- === notifications (in-app) ==============================================
-- One row per recipient. Direct notifications (mention, reply, report_filed)
-- and broadcast fan-out (one row per member) both land here. Recipients see
-- and mark-read only their own rows.
CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    recipient_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    link_url TEXT NOT NULL DEFAULT '',
    actor_id UUID REFERENCES profiles(id) ON DELETE SET NULL,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notifications_recipient_idx
  ON notifications (recipient_id, read_at, created_at DESC);

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;

CREATE POLICY notifications_select ON notifications FOR SELECT
  USING (recipient_id = app_current_user_id());

CREATE POLICY notifications_update ON notifications FOR UPDATE
  USING (recipient_id = app_current_user_id());

-- A request-path insert (e.g. a mention row written inline) must be by a
-- member of the org; the worker's broadcast fan-out inserts run at pool
-- admin privilege, the same trust boundary as every other background job.
CREATE POLICY notifications_insert ON notifications FOR INSERT
  WITH CHECK (is_org_member(notifications.org_id));

-- === notification_preferences ============================================
-- Per-user, per-org, per-category opt-in. Finer-grained than the global
-- profiles.notification_opt_out kill-switch, which still applies on top.
CREATE TABLE notification_preferences (
    user_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    category TEXT NOT NULL CHECK (category IN ('mentions', 'replies', 'broadcasts', 'digest')),
    email_enabled BOOLEAN NOT NULL DEFAULT true,
    inapp_enabled BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, org_id, category)
);

ALTER TABLE notification_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_preferences FORCE ROW LEVEL SECURITY;

CREATE POLICY notification_preferences_select ON notification_preferences FOR SELECT
  USING (user_id = app_current_user_id());

CREATE POLICY notification_preferences_insert ON notification_preferences FOR INSERT
  WITH CHECK (user_id = app_current_user_id() AND is_org_member(notification_preferences.org_id));

CREATE POLICY notification_preferences_update ON notification_preferences FOR UPDATE
  USING (user_id = app_current_user_id());

-- === unsubscribe_tokens ==================================================
-- One-click email unsubscribe. Resolution happens on a PUBLIC unauthenticated
-- endpoint, so it goes through the SECURITY DEFINER resolve_unsubscribe()
-- function below (the caller has no app.current_user_id) — the same
-- bootstrap-function pattern as accept_invitation / find_api_token_by_prefix.
CREATE TABLE unsubscribe_tokens (
    token TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    category TEXT CHECK (category IN ('mentions', 'replies', 'broadcasts', 'digest')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at TIMESTAMPTZ
);
CREATE INDEX unsubscribe_tokens_user_idx ON unsubscribe_tokens (user_id);

ALTER TABLE unsubscribe_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE unsubscribe_tokens FORCE ROW LEVEL SECURITY;

-- No token-holder SELECT policy: token resolution is public and goes through
-- resolve_unsubscribe(). A signed-in user may read their own tokens if ever
-- needed (there is no such UI yet).
CREATE POLICY unsubscribe_tokens_select ON unsubscribe_tokens FOR SELECT
  USING (user_id = app_current_user_id());

-- Resolves an unused unsubscribe token, flips the matching preference to
-- email_enabled=false (all categories when category IS NULL), marks the
-- token used, and returns the affected user_id. Idempotent: a used or
-- unknown token returns NULL and changes nothing. Runs with definer rights
-- because the public caller has no RLS session context.
CREATE FUNCTION resolve_unsubscribe(p_token TEXT) RETURNS UUID
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE
  v_user_id UUID;
  v_org_id UUID;
  v_category TEXT;
BEGIN
  SELECT user_id, org_id, category INTO v_user_id, v_org_id, v_category
  FROM unsubscribe_tokens
  WHERE token = p_token AND used_at IS NULL;

  IF v_user_id IS NULL THEN
    RETURN NULL;
  END IF;

  IF v_org_id IS NULL THEN
    -- Global unsubscribe: master kill-switch on the profile.
    UPDATE profiles SET notification_opt_out = true WHERE id = v_user_id;
  ELSIF v_category IS NULL THEN
    UPDATE notification_preferences SET email_enabled = false, updated_at = now()
    WHERE user_id = v_user_id AND org_id = v_org_id;
  ELSE
    INSERT INTO notification_preferences (user_id, org_id, category, email_enabled)
    VALUES (v_user_id, v_org_id, v_category, false)
    ON CONFLICT (user_id, org_id, category)
    DO UPDATE SET email_enabled = false, updated_at = now();
  END IF;

  UPDATE unsubscribe_tokens SET used_at = now() WHERE token = p_token;
  RETURN v_user_id;
END;
$$;

-- === collab_boards =======================================================
-- Course-scoped collaborative whiteboards. State is a JSON snapshot
-- (element_id -> element), debounce-persisted by the in-process realtime hub
-- via last-write-wins per element. Any org member may open and edit a board.
CREATE TABLE collab_boards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    snapshot JSONB NOT NULL DEFAULT '{}',
    created_by UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    updated_by UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX collab_boards_course_idx ON collab_boards (course_id, updated_at DESC);
CREATE INDEX collab_boards_org_idx ON collab_boards (org_id);

ALTER TABLE collab_boards ENABLE ROW LEVEL SECURITY;
ALTER TABLE collab_boards FORCE ROW LEVEL SECURITY;

CREATE POLICY collab_boards_select ON collab_boards FOR SELECT
  USING (is_org_member(collab_boards.org_id) OR app_is_platform_owner());

CREATE POLICY collab_boards_insert ON collab_boards FOR INSERT
  WITH CHECK (is_org_member(collab_boards.org_id) AND created_by = app_current_user_id());

CREATE POLICY collab_boards_update ON collab_boards FOR UPDATE
  USING (is_org_member(collab_boards.org_id));

CREATE POLICY collab_boards_delete ON collab_boards FOR DELETE
  USING (created_by = app_current_user_id() OR is_org_moderator(collab_boards.org_id));
