---
task: 3
name: permissions-matrix
parallel_group: 1
depends_on: []
issue: TBD
---

# Task 3: permissions-matrix

## What to build

Extend `internal/auth/permissions.go` with the new org-scoped action strings
Task 6 (commerce/payments) needs, following the existing conventions in that
file exactly:

- Action strings use the `resource.verb` naming convention already in use
  (e.g. `"course.create"`, `"member.role.change"`).
- Grants are expressed by adding action strings to the role's slice in
  `permissionMatrix` (keyed by `RoleOwner`, `RoleTeacher`, `RoleModerator`,
  `RoleLearner`). Follow the existing pattern of collecting a role's actions
  into a named `[]string` var (like `courseDomainActions` /
  `ownerOnlyCourseDomainActions`) and appending it into the relevant role
  entries in `permissionMatrix`, rather than inlining long literal slices.
- `Can(role, action string) bool` itself does not change — it already just
  scans `permissionMatrix[role]`.
- **Important correction to how this matrix is actually used**: `Can()`/
  `permissionMatrix` is documentation-only in this codebase, per the doc
  comment already on `permissionMatrix` in `internal/auth/permissions.go`
  ("It is enforced today via explicit `RequireRole(...)` calls on each
  route... not by consulting this map at request time"). Runtime
  enforcement is `middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher,
  ...)` called directly with literal role constants at each route
  registration in `server.go` — it does NOT call `Can()` or look at
  `permissionMatrix` at all. This task's job is still valuable: it keeps
  the matrix (and its test) as the single living reference for "which role
  can do what" that later Task 6 subtasks (commerce-handlers,
  admin-dashboard, routes-wiring) read off when deciding which
  `RequireRole(...)` arguments to write at each new route. Whoever wires
  routes in task-10 must translate each action below into the matching
  `RequireRole(...)` call by hand — adding an action here does not by
  itself protect anything.
- This file only documents the **org-scoped** role matrix. It has nothing
  to do with platform-owner-only actions (see "Explicitly out of scope"
  below), which use `RequirePlatformOwner` and have no `RequireRole` call
  or matrix entry at all.

Add a new commerce action group, e.g. `commerceDomainActions`, granted to
both `RoleOwner` and `RoleTeacher` (same shape as `courseDomainActions`):

- `"offer.create"`, `"offer.update"`, `"offer.archive"` — offer management
  (create/update/archive an offer on a course).
- `"discount.create"`, `"discount.update"`, `"discount.archive"` — discount
  code management.
- `"invitetoken.create"` — invite-token generation for invitation-only
  offers.
- `"entitlement.grant"` — manual entitlement grant (admin-grants a learner
  free access; reason is required at the handler/model layer, not enforced
  by this permission check).

Add a new owner-only commerce action group, e.g.
`ownerOnlyCommerceDomainActions`, granted to `RoleOwner` only (same shape as
`ownerOnlyCourseDomainActions`):

- `"refund.initiate"` — refund initiation (in-app "Refund" button that calls
  Razorpay's Refund API; the resulting entitlement/revenue change still only
  takes effect once the refund webhook is verified — that webhook path does
  not go through `Can()` at all, see below).
- `"dashboard.org.view"` — org-scoped admin dashboard viewing (own org's
  members/courses/enrollment/revenue).
- `"report.revenue.view"` — revenue reporting (per-course/per-offer reports).

Wire both new slices into `permissionMatrix`:
- `RoleOwner`: append `commerceDomainActions` and
  `ownerOnlyCommerceDomainActions` (same append pattern already used for
  `courseDomainActions` / `ownerOnlyCourseDomainActions`).
- `RoleTeacher`: append `commerceDomainActions` only.
- `RoleModerator`, `RoleLearner`: unchanged (no commerce actions).

### Explicitly out of scope for `permissionMatrix` / `Can()`

Two Task 6 authorization checks are **not** org-scoped roles and must NOT be
added as action strings to `permissionMatrix`:

1. **Platform commission rate configuration** (viewing/updating the
   `platform_settings` row, e.g. commission percentage). There is no org
   context for this at all — it is platform-wide configuration. Routes for
   this must be protected with
   `internal/httpserver/middleware.RequirePlatformOwner(profiles)`
   (already implemented, see `internal/httpserver/middleware/org.go`), the
   same way any other platform-wide admin route is protected. Do not invent
   an action string like `"platform_settings.update"` in this file, and do
   not route it through `RequireRole`/`Can()`.
2. **Platform-owner cross-org dashboard viewing** (the dashboard that spans
   all organizations, as opposed to `"dashboard.org.view"` above which is
   one org owner viewing their own org). This also has no single org context
   and must be protected with `RequirePlatformOwner(profiles)`, not
   `RequireRole`/`Can()`.

Whoever wires the commerce/dashboard routes in later Task 6 subtasks
(commerce-handlers, admin-dashboard, routes-wiring) should read this
distinction directly off this file: org-scoped actions are documented here
and enforced at their route with a literal `RequireRole(auth.RoleOwner, ...)`
call (never a runtime `Can()` lookup); the two platform-wide checks above go
through `RequirePlatformOwner(...)` only, with no corresponding matrix
entry and no `RequireRole` call at all.

## Acceptance criteria

- [ ] `internal/auth/permissions.go` defines a `commerceDomainActions`
      `[]string` containing exactly: `"offer.create"`, `"offer.update"`,
      `"offer.archive"`, `"discount.create"`, `"discount.update"`,
      `"discount.archive"`, `"invitetoken.create"`, `"entitlement.grant"`.
- [ ] `internal/auth/permissions.go` defines an
      `ownerOnlyCommerceDomainActions` `[]string` containing exactly:
      `"refund.initiate"`, `"dashboard.org.view"`, `"report.revenue.view"`.
- [ ] `permissionMatrix[RoleOwner]` includes every action in both
      `commerceDomainActions` and `ownerOnlyCommerceDomainActions`.
- [ ] `permissionMatrix[RoleTeacher]` includes every action in
      `commerceDomainActions` and none of `ownerOnlyCommerceDomainActions`.
- [ ] `permissionMatrix[RoleModerator]` and `permissionMatrix[RoleLearner]`
      are unchanged (no new commerce actions granted).
- [ ] `Can(role, action)` is unmodified (no new special-casing added to the
      function itself — the matrix data change is sufficient).
- [ ] No action string for platform commission configuration or
      platform-owner cross-org dashboard viewing is added to
      `permissionMatrix` anywhere in this file; a comment near the new
      commerce slices states these two are enforced exclusively via
      `middleware.RequirePlatformOwner`, not this matrix.
- [ ] `internal/auth/permissions_test.go` is extended with a
      `TestCan_CommerceDomainActions` test following the exact structure of
      the existing `TestCan_CourseDomainActions`: asserts every
      `commerceDomainActions` entry is granted to `RoleOwner` and
      `RoleTeacher` and denied to `RoleModerator` and `RoleLearner`; asserts
      every `ownerOnlyCommerceDomainActions` entry is granted to `RoleOwner`
      only and denied to `RoleTeacher`, `RoleModerator`, and `RoleLearner`.
- [ ] `go build ./...` and `go test ./internal/auth/...` pass.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
