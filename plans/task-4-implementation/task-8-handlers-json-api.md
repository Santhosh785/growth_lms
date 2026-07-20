---
task: 8
name: handlers-json-api
parallel_group: 3
depends_on: [3, 5, 6, 7]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 8: Course-domain JSON API handlers

## What to build

New files under `internal/httpserver/handlers/`, following `orgs.go`'s
pattern (constructor functions closing over `*AuthDeps`, `tx, _ :=
middleware.RequestTxFromGin(c)`, `oc, _ := middleware.OrgContextFromGin(c)`,
`errors.Is(err, models.ErrNotFound)` → 404, audit-log state-changing
actions via `d.Audit.Record`).

Extend `AuthDeps` (`internal/httpserver/handlers/deps.go`) with the new
repos from Task 5 and the `media.BunnyClient`/`media.StorageClient` from
Task 6.

- `courses.go`: create/get/list/update/delete, status transitions
  (`draft→review`, `review→published`, `review→scheduled`,
  `review→draft`, `published→unpublished`, any-non-archived→`archived`,
  reject anything else with 400 — this state machine lives here, not in
  the model layer), archive/unarchive, `POST /publish` (snapshot + set
  `published_at`, reject with a clear error listing offending blocks if
  any video block's asset isn't `processing_status='ready'`), `POST
  /unpublish`, `POST /duplicate`.
- `chapters.go`, `lessons.go`: create/update/delete (409 + child count on
  delete-with-children), reorder.
- `blocks.go`: create/update/delete/reorder per the 5 JSONB shapes;
  autosave endpoint (content only, never touches course status);
  text-block HTML sanitized via a new `internal/sanitize/html.go` wrapping
  `bluemonday` with EXACTLY the allowlist from the spec: `p, strong, em, u,
  ul, ol, li, br, a (href only, no target/event attrs), h1-h3` — nothing
  else, no regex-based sanitization.
- `media.go`: `POST /api/media/upload/video` (lazily provisions
  `bunny_library_id` on first video upload for an org, creates the
  `assets` row with `processing_status='pending'` immediately, returns
  the signed/TUS upload URL), `POST /api/media/upload` (Supabase signed
  upload URL, path `org/{org_id}/courses/{course_id}/{asset_id}/
  {filename}`), `POST /api/media/upload/:pendingId/complete` (does a
  server-side `HeadObject` call before creating/finalizing the `assets`
  row — never trusts the client's completion call alone).
- `assets.go`: `PATCH /api/assets/:id/refresh-url` — regenerates and
  caches a new signed URL; TTL depends on the asset's course status
  (< 5 min for draft-course assets, up to 1 hour for published) — see
  Task 12 for the exact test this must satisfy.
- `versions.go`: list/get/restore.
- `collections.go`, `categories.go`, `tags.go`: CRUD per spec (categories
  owner-only mutation; tags freeform get-or-create available to
  teacher/owner). These two mount under `/api/orgs/:org_slug/...` using
  the EXISTING `ResolveOrg` middleware (not `ResolveCourseOrg` — no course
  in their path), per the main-plan's noted routing exception.
- `preview.go`: `GET /api/courses/:courseId/preview` — teacher/owner-only,
  renders a Task-4-owned minimal read-only view (chapters/lessons/blocks
  in order; text as sanitized HTML; image/file as `<img>`/download link;
  video as a `<video>` tag using a short-lived signed URL; quiz as a
  read-only question list, no answering UI). Available regardless of
  course status (draft/review/scheduled all previewable). This can render
  either JSON (for the API) or delegate to Task 11's HTML template — keep
  the data-assembly logic in this handler, template rendering optional
  here (Task 11 owns the actual HTML template file).

All authoring routes must be gated by `middleware.RequireRole(auth.RoleOwner,
auth.RoleTeacher)` (category mutation additionally requires
`auth.RoleOwner` only) — RLS remains the real boundary; this is defense in
depth per the spec.

## Acceptance criteria

- [ ] Every endpoint listed in the spec's "Summary of Deliverables" /
      per-section bullet points exists and returns sensible JSON.
- [ ] Learner/moderator roles get 403 on every authoring endpoint (RLS
      would also block them at the DB layer, but the Go-side check must
      fire first).
- [ ] Status transitions not in the documented flow are rejected with 400,
      not silently applied.
- [ ] Publish is rejected (with the offending block list in the error
      body) if any video block's asset isn't `ready`.
- [ ] Text block content is sanitized on every create/update through the
      exact bluemonday allowlist — no script/style/event-handler content
      survives.
- [ ] Upload-confirmation handler performs a real server-side existence
      check before creating an `assets` row — a forged `/complete` call
      for a file that was never uploaded does not create a usable asset.
- [ ] Course duplication produces new IDs, starts `draft`, shares
      `asset_id` references (no storage copy).

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
