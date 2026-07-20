---
task: 10
name: routes-wiring
parallel_group: 4
depends_on: [4, 8, 9]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 10: Wire course-domain routes into the Gin engine

## What to build

Extend `internal/httpserver/server.go`: add `registerCourseRoutes(engine
*gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool)`, analogous to
`registerOrgRoutes`, called from `New(...)` alongside the existing
`registerAuthRoutes`/`registerOrgRoutes` calls.

- `/api/courses` (create) and `/api/courses/:courseId/...` group using
  `Authenticate` + `WithRequestTx` + `middleware.ResolveCourseOrg(...)`
  (Task 7) + `middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)` on
  mutating routes: chapters, chapters/:id/lessons, lessons/:id/blocks,
  reorder endpoints, autosave, publish/unpublish/duplicate/archive,
  preview (teacher/owner only, but not a mutation — still requires the
  role check per spec), versions (list/get/restore).
- `/api/media/upload/video`, `/api/media/upload`,
  `/api/media/upload/:pendingId/complete`, `/api/assets/:id/refresh-url` —
  same middleware stack, keyed off a `:courseId` in their request body/
  path as needed (media upload requests must carry which course they
  belong to).
- `/api/webhooks/bunny` — NO auth/RLS middleware (external caller), just
  the handler that verifies the HMAC signature itself before doing
  anything.
- `/api/orgs/:org_slug/categories`, `/api/orgs/:org_slug/collections` —
  mounted under the EXISTING `org := authed.Group("/orgs/:org_slug")` +
  `ResolveOrg` group already in `registerOrgRoutes` (or a sibling function
  reusing that same `org` group), per the main-plan's noted exception.
- New HTML group: `/courses/:id/edit` (GET, renders Task 11's editor page)
  — cookie-authenticated (`Authenticate` already supports the session
  cookie), `ResolveCourseOrg`, PLUS `middleware.RequireCSRF()` (Task 4) on
  every mutating HTMX route this group serves.

## Acceptance criteria

- [ ] `go build ./...` succeeds with all new routes wired.
- [ ] Every route from Task 8's handler set is reachable at the exact path
      documented in the spec (`/api/courses/:courseId/chapters`, etc.).
- [ ] The Bunny webhook route has NO `Authenticate`/`WithRequestTx`/role
      middleware — only signature verification inside the handler.
- [ ] The HTML course-editor route group has `RequireCSRF()` applied to
      its mutating routes and NOT applied to any `/api/*` JSON route.
- [ ] Manual smoke test (curl or the existing `/test-console` pattern):
      create a course, add a chapter/lesson/block, publish it, confirm a
      learner-role token gets 403 on the create-course call.

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
