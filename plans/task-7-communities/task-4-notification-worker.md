---
task: 4
name: notification-worker
parallel_group: 3
depends_on: [3]
issue: none
---

# Task 4: Notification worker dispatch + email templates

## What to build

Background dispatch for the four community notification kinds — mention,
reply, report-filed, broadcast — enqueued from the HTTP handlers and processed
by the Asynq worker (never sent synchronously in the request path).

Each handler ALWAYS writes the in-app `notifications` row, then sends email
only when BOTH the global `profiles.notification_opt_out` master switch AND the
per-category `notification_preferences.email_enabled` allow it. Report-filed
and broadcast fan out across the org's moderators/members respectively. Each
email carries a per-recipient one-click unsubscribe link in its footer, minted
as an `unsubscribe_tokens` row at send time.

Also add typed email render helpers (mention / reply / broadcast) that return
`(subject, htmlBody)` with a shared layout and the unsubscribe footer, leaving
the `EmailClient` interface unchanged.

## Acceptance criteria

- [ ] Four task types + payloads + enqueue functions, registered on the worker
      mux.
- [ ] In-app row written on every dispatch, regardless of email gating.
- [ ] Email suppressed by the master opt-out and by a disabled category.
- [ ] Broadcast writes one in-app row (and one gated email) per member.
- [ ] Email bodies HTML-escape user content and include the unsubscribe link.

## Boundary

Does NOT define the HTTP routes that enqueue these tasks — that is task 5.

## Commit convention

Feature commit on the `task-7-communities` branch (disk-only plan).
