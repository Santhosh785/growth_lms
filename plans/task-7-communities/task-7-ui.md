---
task: 7
name: ui
parallel_group: 5
depends_on: [5, 6]
issue: none
---

# Task 7: Server-rendered community & notification UI

## What to build

HTMX/JS pages that drive the Task 7 JSON API over same-origin fetch (same
pattern as the existing learner UI — no CSRF token on those calls,
SameSite=Lax cookies), plus nav integration.

- **Community page** — org thread list + new-thread composer.
- **Thread page** — posts and one-level replies, reactions, an `@`-mention
  picker that inserts `@[uuid]` tokens from a member autocomplete, report, and
  author/moderator delete + moderator hide/pin/lock actions.
- **Notifications page** — the in-app inbox with mark-read and mark-all-read.
- **Board page** — a collaborative sticky-note whiteboard over the board
  WebSocket, showing live presence and restoring the snapshot on load.
- **Moderation page** — the moderator report queue with resolve/dismiss.
- **Nav** — a notification bell with an HTMX unread badge, and per-org
  Community/Reports links.

## Acceptance criteria

- [ ] Every page renders through the shared nav and template system.
- [ ] Thread page supports posting, replying, reacting, mentioning, reporting,
      and (for moderators) hiding/deleting.
- [ ] The nav bell shows an unread count that refreshes periodically.
- [ ] The board page reflects other users' changes live.
- [ ] All templates parse and their routes register without conflict.

## Boundary

Consumes the API (task 5) and the WebSocket hub (task 6); adds no new backend
behavior.

## Commit convention

Feature commit on the `task-7-communities` branch (disk-only plan).
