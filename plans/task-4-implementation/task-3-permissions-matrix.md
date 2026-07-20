---
task: 3
name: permissions-matrix
parallel_group: 1
depends_on: []
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 3: Extend the permission matrix for course-domain actions

## What to build

Extend `internal/auth/permissions.go`'s `permissionMatrix` (documentation-only
map, per its existing comment — real enforcement stays via
`middleware.RequireRole(...)` on each route, this task does not change that
enforcement mechanism) with course-domain actions:

- Add to both `RoleOwner` and `RoleTeacher`: `course.create`,
  `course.update`, `course.delete`, `course.publish`, `course.unpublish`,
  `course.archive`, `course.duplicate`, `chapter.create`, `chapter.update`,
  `chapter.delete`, `lesson.create`, `lesson.update`, `lesson.delete`,
  `block.create`, `block.update`, `block.delete`, `media.upload`,
  `collection.manage`, `tag.manage`.
- Add to `RoleOwner` only: `category.create`, `category.update`,
  `category.delete` (spec: categories are an owner-managed curated
  taxonomy; tags are freeform get-or-create available to teacher/owner).
- `RoleModerator` and `RoleLearner` get none of the above (spec: moderator
  remains learner-equivalent for authoring, per Task 3's Q54 decision;
  learners never author).

Do not touch `RequireRole` or any route registration — that's Task 10's
job. This task only updates the documentation map and its test.

## Acceptance criteria

- [ ] `permissionMatrix[RoleOwner]` and `permissionMatrix[RoleTeacher]`
      include all course-domain actions listed above (teacher list is a
      strict subset of owner's, minus the three `category.*` actions).
- [ ] `permissionMatrix[RoleModerator]` and `permissionMatrix[RoleLearner]`
      remain unchanged (empty/invite-only, as today).
- [ ] `Can(role, action)` returns correct true/false for each new action
      across all four roles — add table-driven test cases to whatever test
      file covers `permissions.go` today (create one if none exists).

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
