---
task: 5
name: models-repositories
parallel_group: 2
depends_on: [1]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 5: Course-domain model repositories

## What to build

New files under `internal/models/`, following `organization.go`'s exact
pattern (plain struct, `NewXRepo()` constructor, methods take
`models.Querier` so they work identically against a request-scoped
`dbctx.RequestTx` or a worker's pool connection or a test transaction):

- `category.go`, `tag.go` — CRUD + get-or-create for tags (`GetOrCreate(ctx,
  q, orgID, name) (*Tag, error)`, upsert-by-`(org_id, slug)`).
- `course.go` — CRUD, `List(ctx, q, orgID) ([]*Course, error)`,
  `UpdateStatus(ctx, q, id, newStatus string) (*Course, error)` (no
  transition-validity logic here — that's a handler-level state machine,
  this repo method just performs the UPDATE the handler decided is valid),
  `Duplicate(ctx, q, sourceCourseID string) (*Course, error)` (deep-copies
  chapters/lessons/blocks with new IDs in one transaction, shares
  `asset_id` references — never duplicates asset rows or storage, per
  spec's asset-lifecycle note), tag-attach/detach.
- `chapter.go`, `lesson.go` — CRUD, `NextSortOrder(ctx, q, parentID)
  (numeric, error)`, `Reorder(...)` delegating to the shared renormalization
  helper (task's `sortorder.go`), delete blocked (409-shaped error, not a
  raw DB error) if children exist — return a typed error the handler can
  map to 409 with a count, e.g. `ErrHasChildren{Count int}`.
- `block.go` — CRUD per the 5 JSONB content shapes (spec section "Block-
  Based Content Editor"), `Reorder(...)`, `Autosave(ctx, q, id, content)
  (updates content + updated_at only, never touches course/published_at)`.
- `asset.go` — CRUD, `RefreshSignedURL(...)` (updates cached
  `signed_url`/`signed_url_expires_at` — actual signed-URL generation via
  the media clients from Task 6, this repo method just persists the
  result), `processing_status` transitions for the webhook path.
- `course_version.go` — `Snapshot(ctx, q, courseID) (*CourseVersion,
  error)` (serializes full nested chapters→lessons→blocks state to JSONB),
  `List`, `Get`, `Restore(ctx, q, courseID, versionID) (*CourseVersion,
  error)` (creates a NEW version + overwrites current
  chapters/lessons/blocks from the snapshot — restore is "undo via new
  version", never deletes history).
- `course_prerequisite.go`, `course_completion_rule.go` — plain CRUD.
- `collection.go` — CRUD, `AddCourse`/`RemoveCourse`/`ListCourses`/
  `Reorder` on `collection_courses`.
- `internal/models/sortorder.go` — shared helper: given a parent's existing
  sibling `sort_order` values (as `NUMERIC(20,10)`/Go `decimal`-equivalent,
  check what numeric type `pgx` maps `NUMERIC` to in this codebase — likely
  `pgtype.Numeric` or a `string`/`float64` wrapper, follow whatever
  precedent exists elsewhere, or use `github.com/shopspring/decimal` if a
  clean fractional midpoint calc is needed and add it to `go.mod`) and a
  target position, returns either a fractional midpoint or triggers a full
  renormalization (whole-number respacing: 1.0, 2.0, 3.0...) of all
  siblings in the same transaction when precision would be exhausted.
  Exposed as a pure, unit-testable function separate from any DB call.

## Acceptance criteria

- [ ] Every repo method takes `models.Querier`, not a concrete pool/tx type.
- [ ] `Course.Duplicate` produces new IDs for course + all chapters/lessons/
      blocks, starts `status='draft'`, and block `content` JSONB for
      image/video/file blocks keeps the SAME `asset_id` as the source (no
      new asset rows, no storage copy).
- [ ] Chapter/lesson delete returns a distinguishable error type when
      children exist, carrying the child count, instead of a generic DB
      error or silent cascade.
- [ ] `course_version.Restore` creates a new version row; it never deletes
      or overwrites an existing `course_versions` row.
- [ ] `sortorder.go`'s renormalization function has a standalone unit test
      (no DB needed) proving: normal fractional insert between two
      siblings, and the renormalize-to-whole-numbers path firing when
      precision is exhausted.

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
