---
task: 3
name: model-repositories
parallel_group: 2
depends_on: [2]
issue: none
---

# Task 3: Community & notification model repositories

## What to build

A typed repository per new table, following the existing `models` pattern
(struct + `XxxRepo` + column const + `scan` helpers, every method taking a
`Querier` so the same code runs under a request transaction, the worker pool,
or a test). Register each repo on `AuthDeps` and construct it at server
startup.

Repos and their notable methods:

- Discussion threads — create, get, list-by-org, list-by-course, set
  pinned/locked, update status, touch (bump activity), delete.
- Discussion posts — create (optional parent), list-by-thread, edit body,
  update status (hide/soft-delete).
- Post reactions — add (idempotent), remove own, list-by-thread for
  aggregation.
- Post mentions — `ParseMentionTokens` / `StripMentionTokens` for `@[uuid]`
  tokens, add-many, list-by-post.
- Content reports — create, list-open-by-org, resolve, dismiss.
- Notifications — create, list-by-recipient, count-unread, mark-read,
  mark-all-read.
- Notification preferences — upsert, list, `IsEmailEnabled` (missing row =
  opted-in default).
- Unsubscribe tokens — generate random token, create, resolve (via the
  `resolve_unsubscribe` function; idempotent).
- Collab boards — create, get, list-by-course, save-snapshot, delete.

## Acceptance criteria

- [ ] One repo per table; all registered on `AuthDeps` and constructed at
      startup.
- [ ] Mention token parsing dedupes, preserves order, and ignores non-uuid
      `@name` text.
- [ ] `IsEmailEnabled` returns true when no preference row exists.
- [ ] Package builds and existing model tests still pass.

## Commit convention

Feature commit on the `task-7-communities` branch (disk-only plan).
