-- Reverse of 000011_podcasts.up.sql. Drop the SECURITY DEFINER functions
-- first, then children before parents; policies drop with their tables.
DROP FUNCTION IF EXISTS list_published_podcast_episodes(UUID);
DROP FUNCTION IF EXISTS get_published_podcast_show(UUID, TEXT);

DROP TABLE IF EXISTS podcast_progress;
DROP TABLE IF EXISTS podcast_playlist_items;
DROP TABLE IF EXISTS podcast_playlists;
DROP TABLE IF EXISTS podcast_episodes;
DROP TABLE IF EXISTS podcast_shows;

ALTER TABLE organizations
  DROP COLUMN IF EXISTS podcasts_enabled;
