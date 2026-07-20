---
task: 11
name: htmx-course-editor
parallel_group: 4
depends_on: [4, 8]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 11: Lightweight HTMX course editor UI

## What to build

Per the session's scoping decision (main-plan.md), this is intentionally a
LIGHTWEIGHT layer, not the full spec-verbatim editor — no drag-and-drop JS
library, no polish pass. New package `internal/httpserver/templates/`
(no prior template infra exists in this codebase — `html/template` +
`template.ParseFS` over embedded files, or Gin's built-in
`LoadHTMLGlob`/`HTMLRender`, whichever is less friction given nothing
exists yet), plus a handler `internal/httpserver/handlers/
course_editor_ui.go` (`GET /courses/:id/edit`).

Render:
- Course metadata form (title, description, category select, tags,
  cover-image upload input).
- Chapter list: add form, each chapter shows an up/down move button pair
  (posts to the reorder endpoint with the swapped pair) instead of
  drag-drop, edit/delete controls; expandable to show its lessons the same
  way.
- Block editor per type: `text` → textarea, autosave via `hx-trigger=
  "change delay:1s"` against the autosave endpoint; `image`/`video`/`file`
  → file input + upload progress + preview/link on success; `quiz` → a
  simple repeated-question form (add/remove question, mcq/true_false/
  short_answer type select).
- Draft/review/publish/unpublish buttons (publish behind a confirm
  dialog), archive/duplicate buttons.
- Preview button opening `/api/courses/:id/preview` in a new tab/modal.
- Version-history sidebar: list with restore buttons (confirm before
  restoring).
- Every form that mutates state includes the CSRF token (Task 4) as a
  hidden field or `hx-headers` value, read from the `lms_csrf` cookie.
- Basic error handling: a toast/alert region updated via an HTMX
  out-of-band swap on failed requests.

## Acceptance criteria

- [ ] `GET /courses/:id/edit` renders for a teacher/owner and 403s (or
      redirects appropriately) for a learner/moderator.
- [ ] A teacher can, purely by clicking through the rendered page: create
      a chapter, create a lesson, add a text block, see it autosave, and
      publish the course — without any direct API/database access.
- [ ] Reordering works via the up/down buttons (no drag-drop JS needed to
      satisfy "ordering survives reload").
- [ ] Every mutating form on this page is rejected without a valid CSRF
      token (Task 4's middleware applied via Task 10's routing).
- [ ] No new JS dependency beyond htmx itself is introduced.

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
