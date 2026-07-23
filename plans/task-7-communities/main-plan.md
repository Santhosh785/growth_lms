# Plan: task-7-communities

> Retrospective plan. Task 7 was implemented and committed on the
> `task-7-communities` branch (8 commits, `1109c99` → `550b6e5`) before this
> record was drafted. It documents what was built and why, for a future
> reader. GitHub publishing was unavailable (no git remote), so this is a
> disk-only draft.

## Goal

Deliver roadmap **Task 7 (Phase C — Engagement)** for Growth LMS: give
organizations and courses a community layer (threaded discussions), an in-app
+ email notification system with preferences and unsubscribe, and real-time
collaboration (presence + collaborative boards) — all enforcing the same
tenant isolation and server-side authorization the MVP established.

## Approach

Build the full Task 7 on top of the existing conventions (golang-migrate SQL
migrations with SECURITY-DEFINER RLS helpers, the `models` repo pattern, Gin
handlers on `*AuthDeps`, the Asynq worker + `notify.EmailClient` stack, and
HTMX templates with a shared `nav.html`). Real-time is a **Go-native,
in-process WebSocket hub** — no second service, no Node/Yjs. Every new
org-owned table enables Postgres RLS; a new `is_org_moderator()` helper gives
the moderator role real in-database power.

## Decisions & Rejected Alternatives

<!-- why is it built this way? see grilling-record.md for the full session -->

- **Full Task 7 including real-time boards** — the user wanted the complete
  engagement layer, not a slice. Rejected: discussions-only, and
  discussions+notifications-only (would have deferred the collaboration the
  user explicitly asked for).
- **Go-native in-process WebSocket hub** — keeps one language and one binary,
  reuses the existing cookie/JWT auth and membership checks, no internal
  service-auth secret. Rejected: a separate Node `y-websocket`/Yjs service
  (most faithful to the plan's literal "Yjs" wording but doubles the infra and
  adds a second runtime); a separate `cmd/collab` Go binary (independently
  scalable but needs an internal service handshake). Consequence: boards use
  **last-write-wins**, not a CRDT — acceptable for the first cut, with a
  documented Redis-pub/sub seam for multi-instance scale-out.
- **One discussion subsystem, two scopes** — a thread with `course_id NULL` is
  an org-wide forum thread; with `course_id` set it is a course discussion.
  One level of replies (root post + flat replies). Rejected: course-only
  discussions (drops the org community); arbitrary-depth nested threads
  (heavier to query, render, and moderate).
- **Report queue + soft-delete, moderator power in RLS** — members report
  posts; moderators/owners work a per-org queue and hide/soft-delete (rows
  kept + auditable). Enforced in-DB by a new `is_org_moderator()` helper, not
  only in middleware. Rejected: hard-delete with no queue (no audit trail, no
  review); author+owner-only (wastes the moderator role, no reporting UX).
- **In-app notifications table + email, per-category preferences** — a
  `notifications` table drives a nav bell/inbox; email is sent only when the
  global `profiles.notification_opt_out` master switch AND the per-category
  `notification_preferences` both allow it, with one-click unsubscribe tokens.
  Rejected: in-app-only (drops the email deliverable); email-only (no inbox).
- **@-mention via member-picker `@[uuid]` tokens** — profiles have no username
  field, so the composer inserts hidden `@[uuid]` tokens from an autocomplete;
  the server validates each against org membership. Rejected: adding a
  username column (migration + uniqueness + settings UI); mention-by-email
  (leaks emails, clunky).
- **Boards: presence + course-scoped whiteboards, JSON snapshot (LWW)** — the
  hub carries live presence and board element ops, debounce-persisted as a
  JSON snapshot. Rejected: presence + live-discussion-push only (no boards);
  append-only op-log persistence (heavier storage/compaction).
- **Secure the pending admin-register endpoint first, as its own commit** —
  the tree already had an unauthenticated `POST /api/auth/admin-register`;
  gate it behind platform-owner auth + rate limit before starting Task 7.
  Rejected: leave as-is (an open user-creation endpoint would linger and mix
  into the feature branch); revert entirely (the endpoint is genuinely
  useful, just needed gating).

## Tasks

| # | Task | Phase | Depends on | Status |
|---|------|-------|------------|--------|
| 1 | Secure the admin-register endpoint | 1 | — | done |
| 2 | Communities & notifications schema + RLS (migration 000008) | 1 | — | done |
| 3 | Community & notification model repositories | 2 | 2 | done |
| 4 | Notification worker dispatch + email templates | 3 | 3 | done |
| 5 | Community/moderation/notification HTTP API + middleware | 3 | 3 | done |
| 6 | In-process realtime hub + WebSocket endpoints | 4 | 3, 5 | done |
| 7 | Server-rendered community & notification UI | 5 | 5, 6 | done |
| 8 | Test suite (RLS isolation, worker, hub, unit) | 5 | 3, 4, 5, 6 | done |

## Execution phases

- **Phase 1 (parallel):** task-1, task-2
- **Phase 2:** task-3
- **Phase 3 (parallel):** task-4, task-5
- **Phase 4:** task-6
- **Phase 5 (parallel):** task-7, task-8

## Dependency graph

```
task-1 ─┐
task-2 ─┴─▶ task-3 ─┬─▶ task-4
                    ├─▶ task-5 ─┬─▶ task-6 ─┬─▶ task-7
                    │           │           │
                    └───────────┴───────────┴─▶ task-8
```
