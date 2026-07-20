# Plan: task-4-implementation

## Goal

Implement Task 4 (`plans/lms-mvp/task-4-course-domain.md`): the complete course
authoring and content-management system — org-scoped schema with RLS, course
lifecycle (draft/review/scheduled/published/unpublished/archived), a
5-block-type content editor, signed-URL media uploads to Bunny Stream
(video) and Supabase Storage (images/files), duplication, version history,
and collections. Learner-facing playback/progress/scoring stays out of
scope (Task 5's job).

## Approach

Follow Task 3's established conventions exactly rather than introducing new
patterns: `models.Querier`-based repos, `dbctx`/RLS session-variable
pattern, `RequireRole` defense-in-depth, the `auth.Client`-style interface
for external HTTP integrations. Prioritize a fully correct, tested backend
(schema, RLS, permissions, media flow, worker job) over UI polish; the HTMX
course editor is a lightweight but functional layer added after the API is
solid, per the decision below.

## Decisions & Rejected Alternatives

- **Media clients (Bunny Stream, Supabase Storage) as interfaces with real
  HTTP implementations**, mirroring `internal/auth/supabase_client.go`'s
  `Client` interface. Rejected: inline HTTP calls in handlers (untestable
  without live credentials, breaks from the codebase's established
  pattern). Real implementations are best-effort against documented APIs;
  not live-verified in this session (no Bunny/Supabase credentials
  available).
- **Scheduled publish via a periodic asynq sweep** (every minute, `WHERE
  status='scheduled' AND publish_date <= now()`), not a per-course delayed
  one-off task. Rejected: per-course `ProcessAt` task (requires storing
  and canceling asynq task IDs when a teacher reverts `scheduled` →
  `review`; more moving parts, more failure modes). The sweep is
  self-healing — a reverted course just stops matching the WHERE clause.
- **JSON API + DB/RLS + tests fully complete first; HTMX UI added after,
  kept lightweight** (metadata form, up/down move buttons instead of
  drag-drop JS, per-type block editors, autosave, publish/preview/version
  buttons). Rejected: full spec-verbatim HTMX UI with SortableJS drag-drop
  (would significantly expand scope); skipping HTML entirely (rejected
  because the spec explicitly calls for an HTMX UI, even though its
  acceptance criteria technically permit JSON-API-only via "HTMX UI or
  JSON API").
- **`LMS_BUNNY_WEBHOOK_SECRET` is a required config value**, matching
  `config.Load()`'s fail-fast convention for every other secret. Rejected:
  optional-with-fail-closed-403 (would be the one inconsistent case in an
  otherwise uniform required-var list).
- **Add minimal double-submit-cookie CSRF middleware**, scoped only to the
  new cookie-authenticated HTML/HTMX course-editor routes (JSON API stays
  exempt). Closes a gap Task 3's own grilling record flagged (Q57) but
  never implemented, now that this task is the first to ship real
  cookie-driven HTML mutations. Rejected: deferring CSRF as a known gap
  (unacceptable once real cookie-session mutations exist).
- **Test suite: one representative case per required category**, not one
  per table/endpoint. E.g. a single RLS isolation test spanning
  courses+chapters+lessons+blocks+assets, a parametrized permission-matrix
  test over learner/moderator × authoring endpoints, etc. Rejected:
  exhaustive per-table/per-endpoint tests (same underlying mechanism
  repeated ~12x/~30x for marginal confidence gain).
- **Course-domain routes stay flat (`/api/courses/:courseId/...`, no
  `:org_slug` segment), resolved by a new `ResolveCourseOrg` middleware**
  that loads the course (RLS-visible via `is_org_member(courses.org_id)` —
  the same bootstrap-safe pattern `organizations`' own RLS policy already
  uses, since `is_org_member` only needs `app_current_user_id()`, already
  set by `dbctx.Begin`), extracts `org_id`, resolves role, and calls
  `dbctx.SetOrgContext`. Rejected: nesting under `/api/orgs/:org_slug/...`
  (would diverge from the spec's literal documented paths).
  Exception: `/api/categories` and `/api/collections` have no course to
  derive org context from, so they nest under
  `/api/orgs/:org_slug/categories` and `/api/orgs/:org_slug/collections`,
  reusing the existing `ResolveOrg` middleware as-is.
- **No GitHub remote configured in this repo** → this plan is a disk-only
  draft (not published as GitHub issues); `/run-plan` is not used.
  Implementation proceeds directly in this session immediately after this
  draft, by the same agent, in the same worktree — so exact phase/subagent
  parallelism below is descriptive of the dependency graph, not literally
  executed via separate subagents.
- Full grilling record (all 7 questions, in order, with recommendations and
  choices) is preserved in `grilling-record.md` in this directory, per
  draft-plan convention — reference only, not part of the spec.

## Tasks

| # | Task | Phase | Depends on | Status |
|---|------|-------|------------|--------|
| 1 | db-migration | 1 | — | pending |
| 2 | config-webhook-secret | 1 | — | pending |
| 3 | permissions-matrix | 1 | — | pending |
| 4 | csrf-middleware | 1 | — | pending |
| 5 | models-repositories | 2 | 1 | pending |
| 6 | media-clients | 2 | 2 | pending |
| 7 | course-org-middleware | 2 | 1 | pending |
| 8 | handlers-json-api | 3 | 3, 5, 6, 7 | pending |
| 9 | worker-jobs | 3 | 5, 6 | pending |
| 10 | routes-wiring | 4 | 4, 8, 9 | pending |
| 11 | htmx-course-editor | 4 | 4, 8 | pending |
| 12 | tests | 5 | 8, 9, 10 | pending |

## Execution phases

- **Phase 1 (parallel):** task-1 (db-migration), task-2 (config-webhook-secret), task-3 (permissions-matrix), task-4 (csrf-middleware)
- **Phase 2 (parallel):** task-5 (models-repositories), task-6 (media-clients), task-7 (course-org-middleware)
- **Phase 3 (parallel):** task-8 (handlers-json-api), task-9 (worker-jobs)
- **Phase 4 (parallel):** task-10 (routes-wiring), task-11 (htmx-course-editor)
- **Phase 5:** task-12 (tests)
