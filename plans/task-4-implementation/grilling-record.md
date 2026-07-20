## Grilling Record

> Reference only — not part of the spec. Kept for a future agent,
> developer, or the author revisiting why this plan is shaped this way.
> This session grilled the IMPLEMENTATION approach for Task 4, after the
> requirements spec itself (`plans/lms-mvp/task-4-course-domain.md`) had
> already been grilled in an earlier session (see
> `plans/lms-mvp/grilling-record.md`, Q62-Q73).

<details>
<summary>Complete decision history of the implementation-planning session</summary>

### Q1: Bunny Stream / Supabase Storage media clients
**Options presented:**
1. Interface + fake, like `auth.Client` — matches existing pattern, testable without live credentials
2. Inline HTTP calls in handlers — simpler file layout, untestable without live credentials

**Recommended:** Option 1 — matches `internal/auth/supabase_client.go`'s established pattern
**Chosen:** Option 1, as recommended

### Q2: Scheduled publish mechanism
**Options presented:**
1. Periodic sweep task (every minute, `WHERE status='scheduled' AND publish_date <= now()`) — self-healing, no task-ID bookkeeping
2. Per-course delayed one-off asynq task (`ProcessAt`) — requires storing/canceling asynq task IDs on status reversion

**Recommended:** Option 1 — simpler, self-healing, matches the sort_order "self-healing" philosophy already used elsewhere in the spec
**Chosen:** Option 1, as recommended

### Q3: HTMX UI scope
**Options presented:**
1. JSON API + tests first (production quality); lightweight HTMX after (no drag-drop JS, up/down buttons instead)
2. Full HTMX UI per spec verbatim, including SortableJS drag-drop
3. JSON API + tests only, defer all HTML/HTMX entirely

**Recommended:** Option 1 — spec's own acceptance criteria say "HTMX UI or JSON API," and the backend is the larger correctness surface
**Chosen:** Option 1, as recommended

### Q4: Bunny webhook secret config
**Options presented:**
1. Required config value, like other secrets — matches `config.Load()`'s fail-fast convention
2. Optional; webhook endpoint 403s if unset — one inconsistent case in an otherwise-uniform required-var list

**Recommended:** Option 1, as recommended
**Chosen:** Option 1

### Q5: CSRF for the new HTMX routes
**Options presented:**
1. Add minimal double-submit-cookie CSRF middleware now, scoped to the new HTML routes only
2. Defer CSRF, flag as a known gap (inherited from Task 3, which specified but never built it)

**Recommended:** Option 1 — Task 3's own grilling record (Q57) called CSRF "non-negotiable once cookies drive real mutations," and this task is the first to actually ship cookie-driven HTML mutations
**Chosen:** Option 1, as recommended

### Q6: Test depth
**Options presented:**
1. One representative case per required category (e.g. one combined RLS isolation test across 5 tables, one parametrized permission-matrix test, etc.)
2. Exhaustive: separate test per table/endpoint (~12 RLS tests, ~30 permission tests)

**Recommended:** Option 1 — same underlying mechanism repeated many times for marginal confidence gain
**Chosen:** Option 1, as recommended

### Q7: Route / org-context resolution
**Options presented:**
1. Keep the spec's literal flat paths (`/api/courses/:courseId/...`), add a new `ResolveCourseOrg` middleware that loads the course via `is_org_member(courses.org_id)` (bootstrap-safe, same pattern `organizations`' own RLS policy uses) to resolve org context before `app.current_org_id` is known
2. Diverge from the spec's paths, nest everything under `/api/orgs/:org_slug/courses/...` to reuse the existing `ResolveOrg` middleware verbatim

**Recommended:** Option 1 — matches the spec's documented API surface exactly
**Chosen:** Option 1, as recommended (with a noted exception: `/api/categories` and `/api/collections` have no course to derive org context from, so those two nest under `/api/orgs/:org_slug/...` and reuse `ResolveOrg` as-is — this exception was identified while writing the final plan, not asked as a separate question, since it followed directly from Q7's chosen approach)

</details>
