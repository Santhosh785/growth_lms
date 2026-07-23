---
task: 2
name: schema-rls
parallel_group: 1
depends_on: []
issue: none
---

# Task 2: Communities & notifications schema + RLS (migration 000008)

## What to build

One forward + down migration adding the Task 7 data layer. Every org-owned
table follows the established RLS recipe (`org_id NOT NULL` FK to
organizations, `ENABLE` + `FORCE ROW LEVEL SECURITY`, policies built on the
existing `is_org_member` / `is_org_owner` / `app_current_user_id` helpers).
Child tables denormalize `org_id` so policies never join.

New objects:

- **`is_org_moderator(org_id)`** — SECURITY-DEFINER helper, true for role
  `owner` OR `moderator`. This is what makes moderator power real in-DB.
- **`discussion_threads`** — `course_id NULL` = org-wide, set = course thread;
  `is_pinned`, `is_locked`, `status`. UPDATE/DELETE gated by author OR
  `is_org_moderator`.
- **`discussion_posts`** — root post + one-level reply (`parent_post_id`),
  `status` for soft-delete; author-or-moderator UPDATE/DELETE.
- **`post_reactions`** — `(post_id, user_id, emoji)` PK; delete only own.
- **`post_mentions`** — `(post_id, mentioned_user_id)`; drives mention notices.
- **`content_reports`** — reporter sees own, moderator sees queue, moderator
  resolves/dismisses.
- **`notifications`** — in-app; recipient-scoped SELECT/UPDATE.
- **`notification_preferences`** — `(user_id, org_id, category)`; self-scoped.
- **`unsubscribe_tokens`** + **`resolve_unsubscribe(token)`** SECURITY-DEFINER
  function for the public one-click unsubscribe flow.
- **`collab_boards`** — course-scoped; JSONB `snapshot`.

## Acceptance criteria

- [ ] All nine tables + both functions created with FORCE RLS and policies.
- [ ] `is_org_moderator` used in the threads/posts UPDATE-DELETE and reports
      UPDATE policies.
- [ ] Migration applies and rolls back cleanly (verified up → down → up).
- [ ] No table exposes another organization's rows to a non-member.

## Commit convention

Feature commit on the `task-7-communities` branch (disk-only plan).
