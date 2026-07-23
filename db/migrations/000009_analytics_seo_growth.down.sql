DROP FUNCTION IF EXISTS list_published_courses(UUID);
DROP FUNCTION IF EXISTS resolve_org_by_domain(TEXT);
DROP FUNCTION IF EXISTS search_org_members(UUID, TEXT);

DROP INDEX IF EXISTS discussion_threads_search_idx;
ALTER TABLE discussion_threads DROP COLUMN IF EXISTS search_vector;

DROP INDEX IF EXISTS lessons_search_idx;
ALTER TABLE lessons DROP COLUMN IF EXISTS search_vector;

DROP INDEX IF EXISTS courses_search_idx;
ALTER TABLE courses DROP COLUMN IF EXISTS search_vector;

DROP TABLE IF EXISTS org_pages;

ALTER TABLE organizations
  DROP COLUMN IF EXISTS domain_verified_at,
  DROP COLUMN IF EXISTS domain_verification_token,
  DROP COLUMN IF EXISTS custom_domain,
  DROP COLUMN IF EXISTS og_image_url,
  DROP COLUMN IF EXISTS meta_description,
  DROP COLUMN IF EXISTS theme_json,
  DROP COLUMN IF EXISTS favicon_url,
  DROP COLUMN IF EXISTS logo_url;

DROP TABLE IF EXISTS analytics_daily_rollups;
DROP TABLE IF EXISTS analytics_events;

DROP FUNCTION IF EXISTS is_org_teacher(UUID);
