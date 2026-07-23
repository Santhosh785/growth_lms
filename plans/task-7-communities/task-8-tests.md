---
task: 8
name: tests
parallel_group: 5
depends_on: [3, 4, 5, 6]
issue: none
---

# Task 8: Test suite (RLS isolation, worker, hub, unit)

## What to build

Automated tests proving the security-critical behavior, mirroring the existing
test patterns (real migrated Postgres via the test harness; in-memory email
fake; race detector for concurrency).

- **RLS isolation** — cross-org threads/posts are invisible and unmutable; a
  learner cannot edit/delete another member's post but a moderator of the same
  org can and the author can (directly asserting `is_org_moderator`);
  notifications and preferences are private to their owner; a plain member sees
  only their own report while a moderator sees the queue.
- **Worker dispatch** — the in-app row is always written; email is suppressed
  by the master opt-out and by a disabled category; broadcast fans out one row
  per member; an unsubscribe token flips the preference and is idempotent.
- **Realtime hub** — presence broadcast, op relayed to peers but not the
  sender, and the `OnMessage` callback fires — under the race detector.
- **Unit** — mention token parse/strip; email rendering and HTML escaping.

## Acceptance criteria

- [ ] Tenant isolation and the moderator-power branch are both asserted at the
      database level.
- [ ] Notification gating (opt-out + per-category) and fan-out are asserted.
- [ ] Hub relay/presence/callback pass with `-race`.
- [ ] All tests pass against a migrated Postgres; `go vet` is clean.

## Boundary

Tests the behavior delivered by tasks 2–6; adds no production code.

## Commit convention

Feature commit on the `task-7-communities` branch (disk-only plan).
