DROP TABLE IF EXISTS collection_courses;
DROP TABLE IF EXISTS collections;
DROP TABLE IF EXISTS course_completion_rules;
DROP TABLE IF EXISTS course_prerequisites;
DROP TABLE IF EXISTS course_versions;
DROP TABLE IF EXISTS course_tags;
DROP TABLE IF EXISTS blocks;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS lessons;
DROP TABLE IF EXISTS chapters;
DROP TABLE IF EXISTS courses;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS categories;

ALTER TABLE organizations DROP COLUMN IF EXISTS bunny_library_id;
