---
task: 1
name: db-migration
parallel_group: 1
depends_on: []
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 1: Course-domain database migration

## What to build

A new golang-migrate migration pair, `db/migrations/000003_course_domain.up.sql`
and `.down.sql`, following the exact structure and conventions of
`db/migrations/000002_auth_tenancy.up.sql` (RLS enabled + forced on every
table, policies built on the existing `is_org_member(org_id)` /
`is_org_owner(org_id)` SECURITY DEFINER helper functions — do not invent new
session-variable-comparison policies; reuse the helpers so SELECT visibility
works before `app.current_org_id` is known, exactly like the `organizations`
table's own policy already does).

Add:
- `ALTER TABLE organizations ADD COLUMN bunny_library_id TEXT;` (nullable,
  lazily provisioned per Task 4's media upload flow — not part of this
  migration's own logic, just the column).
- `categories` (`id`, `org_id`, `name`, `slug`), `tags` (`id`, `org_id`,
  `name`, `slug`), `course_tags` junction (`course_id`, `tag_id`, `org_id`).
- `courses` (`id`, `org_id`, `title`, `description`, `cover_image_url`,
  `category_id`, `status` TEXT CHECK IN
  (`draft`,`review`,`scheduled`,`published`,`unpublished`,`archived`),
  `created_by`, `updated_at`, `published_at`, `publish_date`, `archived_at`).
- `chapters` (`id`, `course_id`, `org_id`, `title`,
  `sort_order NUMERIC(20,10)`, `created_by`, `updated_at`).
- `lessons` (`id`, `chapter_id`, `course_id`, `org_id`, `title`,
  `sort_order NUMERIC(20,10)`, `created_by`, `updated_at`).
- `blocks` (`id`, `lesson_id`, `course_id`, `org_id`,
  `type` TEXT CHECK IN (`text`,`image`,`video`,`file`,`quiz`),
  `content JSONB`, `sort_order NUMERIC(20,10)`, `created_by`, `updated_at`).
- `assets` (`id`, `org_id`, `course_id`, `type` TEXT CHECK IN
  (`image`,`video`,`file`), `filename`, `size_bytes`, `mime_type`,
  `storage_provider` TEXT CHECK IN (`bunny`,`supabase`), `storage_key`,
  `signed_url`, `signed_url_expires_at`, `processing_status` TEXT CHECK IN
  (`pending`,`processing`,`ready`,`failed`) DEFAULT `ready`, `created_by`,
  `updated_at`).
- `course_versions` (`id`, `course_id`, `org_id`, `version_number`,
  `snapshot JSONB`, `created_by`, `created_at`).
- `course_prerequisites` (`course_id`, `prerequisite_course_id`, `org_id`).
- `course_completion_rules` (`id`, `course_id`, `org_id`, `rule_type` TEXT
  CHECK IN (`all_lessons`,`percent_lessons`,`all_quizzes`,`percent_quizzes`),
  `threshold INT`, `created_by`, `updated_at`).
- `collections` (`id`, `org_id`, `name`, `description`, `created_by`,
  `updated_at`).
- `collection_courses` junction (`collection_id`, `course_id`, `org_id`,
  `sort_order NUMERIC(20,10)`).

Every table: `org_id NOT NULL`, FK to `organizations(id) ON DELETE CASCADE`,
RLS enabled + forced, `_idx` indexes on every FK column (matching Task 3's
naming), and a `<table>_select`/`_insert`/`_update`/`_delete` policy set
built on `is_org_member`/`is_org_owner` (owner-only for
insert/update/delete on `categories`; member-level for everything else,
matching the spec's category-vs-tag ownership split). `chapters`/`lessons`
also carry redundant `course_id`/`org_id` (denormalized down from their
parent) exactly as the spec specifies, to keep RLS policies and queries
simple without joins.

## Acceptance criteria

- [ ] `db/migrations/000003_course_domain.up.sql` and `.down.sql` exist,
      both apply/roll back cleanly against a fresh Task-1-3 database.
- [ ] Every new table has `org_id`, RLS enabled + forced, and policies that
      compile and pass `go vet`/migrate syntax check.
- [ ] `organizations.bunny_library_id` column added, nullable.
- [ ] `sort_order` is `NUMERIC(20,10)` on `chapters`, `lessons`, `blocks`,
      `collection_courses` — never a float/int type.
- [ ] Down migration drops everything in correct dependency order (children
      before parents), mirroring `000002_auth_tenancy.down.sql`'s style.
- [ ] No new SECURITY DEFINER functions are introduced unless a specific
      table's access pattern cannot be expressed with the existing
      `is_org_member`/`is_org_owner` helpers (categories' owner-only
      mutation policies can reuse `is_org_owner` directly).

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
