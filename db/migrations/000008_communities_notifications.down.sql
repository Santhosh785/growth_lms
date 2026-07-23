DROP FUNCTION IF EXISTS resolve_unsubscribe(TEXT);

DROP TABLE IF EXISTS collab_boards;
DROP TABLE IF EXISTS unsubscribe_tokens;
DROP TABLE IF EXISTS notification_preferences;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS content_reports;
DROP TABLE IF EXISTS post_mentions;
DROP TABLE IF EXISTS post_reactions;
DROP TABLE IF EXISTS discussion_posts;
DROP TABLE IF EXISTS discussion_threads;

DROP FUNCTION IF EXISTS is_org_moderator(UUID);
