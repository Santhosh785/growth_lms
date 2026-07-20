---
task: 12
name: tests
parallel_group: 5
depends_on: [8, 9, 10]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 12: Course-domain test suite

## What to build

Per the session's scoping decision (main-plan.md): one representative,
solid test per required category — not one per table/endpoint.

- Extend `internal/models/rls_isolation_test.go` with
  `TestRLS_CourseDomainIsolation`: seed org A and org B each with a course
  → chapter → lesson → block → asset (following the file's existing
  `seedUser`/`seedOrgWithOwner` helper pattern), then prove — as `app_test`
  role, scoped to org A's user — that org A cannot SELECT, UPDATE, or
  DELETE any of org B's course/chapter/lesson/block/asset rows, mirroring
  `TestRLS_OrganizationIsolation`'s structure (zero-rows-affected
  assertions, not error assertions — RLS hides rows, it doesn't reject
  queries).
- Extend `internal/httpserver/rbac_test.go` (check its existing structure
  first) with a parametrized test: for each of {learner, moderator} ×
  {create course, update course, publish course, create chapter, create
  block, upload media}, assert 403.
- New `internal/models/course_test.go`: table-driven status-transition
  test (valid: draft→review, review→published, review→scheduled,
  review→draft, published→unpublished, *→archived; invalid: e.g.
  draft→published directly, published→scheduled — reject), a duplication
  test (new IDs, `draft` status, same `asset_id` references preserved), a
  version-restore test (restore creates a NEW version row, doesn't
  overwrite the one being restored from).
- New `internal/models/sortorder_test.go`: pure unit test (no DB) for the
  renormalization helper — fractional midpoint insert, and the
  renormalize-to-whole-numbers path triggering when precision is
  exhausted.
- New `internal/httpserver/handlers/media_test.go`: using Task 6's fake
  `BunnyClient`/`StorageClient`, test signed-URL expiry policy — draft-
  course asset gets a short TTL (<5 min), published-course asset can get
  up to 1 hour, and refreshing a published asset's URL after its course
  is unpublished either fails or returns a short-lived URL (not the long-
  lived published-tier one) — no live network calls involved.

## Acceptance criteria

- [ ] `go test ./...` passes; RLS/integration tests skip gracefully
      (not fail) when `LMS_TEST_DATABASE_URL` is unset, matching
      `testutil`'s existing behavior — but must actually be run against a
      real Postgres at least once before this task is considered done.
- [ ] `TestRLS_CourseDomainIsolation` proves cross-org isolation across
      all five table levels (course/chapter/lesson/block/asset) in one
      test, not five separate ones.
- [ ] Permission-matrix test covers both learner and moderator against a
      representative spread of authoring endpoints (not exhaustively all
      ~30).
- [ ] Status-transition test explicitly includes at least one invalid
      transition that must be rejected, not just the valid path.
- [ ] Sort-order test needs no database connection to run.

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
