---
task: 7
name: course-org-middleware
parallel_group: 2
depends_on: [1]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 7: ResolveCourseOrg middleware

## What to build

New file `internal/httpserver/middleware/course.go`, modeled directly on
`internal/httpserver/middleware/org.go`'s `ResolveOrg`.

`ResolveCourseOrg(courses *models.CourseRepo, memberships
*models.MembershipRepo, profiles *models.ProfileRepo) gin.HandlerFunc`:
- Must run after `Authenticate` + `WithRequestTx`.
- Reads `:courseId` from the path.
- Loads the course via the request's `RequestTxFromGin` transaction — this
  works even before org context is set, because `courses`' RLS SELECT
  policy (Task 1) is built on `is_org_member(courses.org_id)`, which only
  needs `app_current_user_id()` (already set by `dbctx.Begin` at request
  start), not `app.current_org_id`. This is the same bootstrap pattern
  `organizations`' own RLS policy already relies on.
- 404s (via `models.ErrNotFound`) if the course doesn't exist or isn't
  RLS-visible — same "doesn't exist vs not visible are indistinguishable"
  principle `ResolveOrg` already follows.
- Resolves the caller's role via `memberships.GetRole(ctx, tx, userID,
  course.OrgID)`; falls back to platform-owner check exactly like
  `ResolveOrg` does if no membership row exists.
- Calls `dbctx.SetOrgContext(ctx, tx, course.OrgID, role)`.
- Stores the resolved context using the SAME `middleware.OrgContext`
  struct and accessor (`OrgContextFromGin`) `ResolveOrg` already defines —
  do not invent a parallel type, since downstream code (`RequireRole`,
  audit logging) should work unmodified regardless of whether org context
  came from a `:org_slug` or a `:courseId`.
- Also stores the loaded `*models.Course` in gin context (new small
  accessor, e.g. `CourseFromGin(c)`), so handlers don't need to re-fetch
  it.

Nested routes (`/api/courses/:courseId/chapters/...`,
`/api/courses/:courseId/chapters/:chapterId/lessons/...`, etc.) all mount
under one Gin route group using this same middleware once, keyed off the
shared `:courseId` segment.

## Acceptance criteria

- [ ] A request for a course in the caller's own org succeeds and
      `dbctx`'s `app.current_org_id`/`app.current_role` end up set
      correctly for the rest of the request.
- [ ] A request for a course in a DIFFERENT org (or a nonexistent course
      ID) gets 404 — not 403, not a raw 500 — matching `ResolveOrg`'s
      existing not-found-vs-forbidden convention.
- [ ] A platform owner with no membership row can still resolve any
      course's org context (role empty, `IsPlatformOwner: true`), same as
      `ResolveOrg`.
- [ ] `middleware.RequireRole(...)` works unmodified downstream of this
      middleware (reuses the same `OrgContext` type/key).

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
