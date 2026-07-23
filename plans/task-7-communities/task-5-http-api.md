---
task: 5
name: http-api
parallel_group: 3
depends_on: [3]
issue: none
---

# Task 5: Community/moderation/notification HTTP API + middleware

## What to build

The JSON API for discussions, moderation, notifications, preferences, and
board CRUD, plus the middleware that resolves org context for routes keyed only
by a row id.

- **Resolve-from-row middleware** — `ResolveThreadOrg`, `ResolvePostOrg`,
  `ResolveBoardOrg`: load the row (visible only to members under RLS), resolve
  the caller's role in the row's org, stamp org context — mirroring the
  existing `ResolveCourseOrg`.
- **Discussions** — create/list org threads and course threads; get a thread
  with posts + aggregated reactions; create a post or one-level reply
  (rejecting a reply whose parent is not a root post in the same thread, and
  blocking posts to a locked thread); react/unreact; report a post; list org
  members for the @-mention picker. On post-create, validate `@[uuid]` tokens
  against membership, persist mentions, and enqueue mention/reply notifications.
- **Moderation** — pin/lock, hide (moderator/owner), edit/delete (author OR
  moderator), list report queue, resolve/dismiss.
- **Notifications** — recipient-scoped list, unread-count HTML badge fragment,
  mark-read, mark-all-read; owner/teacher broadcast that enqueues fan-out.
- **Preferences** — get/update per-category; public confirm-then-POST
  unsubscribe resolved via `resolve_unsubscribe` (no auth, no request tx).
- **Boards** — course-scoped create/list; get (with snapshot); delete
  (creator or moderator).

Moderator/owner routes are `RequireRole`-gated on top of the `is_org_moderator`
RLS. Notification routes carry no org segment (RLS by `recipient_id`). Route
paths must avoid gin param/static segment conflicts.

## Acceptance criteria

- [ ] All routes register without conflict.
- [ ] Cross-org access is impossible (enforced by RLS, verified in task 8).
- [ ] One-level reply nesting and locked-thread rules enforced.
- [ ] Mentions notify only real, non-self org members.
- [ ] Public unsubscribe works unauthenticated and is idempotent.

## Boundary

Does NOT include WebSocket transport (task 6) or HTML pages (task 7). Enqueues
the worker tasks defined in task 4.

## Commit convention

Feature commit on the `task-7-communities` branch (disk-only plan).
