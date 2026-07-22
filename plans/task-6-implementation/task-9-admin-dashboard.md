---
task: 9
name: admin-dashboard
parallel_group: 3
depends_on: [4]
issue: TBD
---

# Task 9: admin-dashboard

## What to build

Build the two basic, **read-only** admin dashboard pages promised by
`plans/lms-mvp/task-6-commerce.md`'s "Basic admin dashboard" section: an
org-scoped view for organization owners, and a cross-organization view for
platform owners. This task is pure reporting — no forms, buttons, or
endpoints that mutate any state. It does **not** build the refund button
(`"refund.initiate"`) or the entitlement-grant form (`"entitlement.grant"`)
— those are task-6-commerce-handlers' job. If a future engineer lands here
expecting mutation UI, they are in the wrong task file.

This task depends only on Task 4 (models-repositories) of this same plan —
specifically `OrderRepo`, `EntitlementRepo`, and `PlatformSettingsRepo`,
plus the already-merged Task 3 org/membership repos (`OrgRepo`,
`MembershipRepo`) and Task 4 (course domain, prior merged work)
`CourseRepo`. It does not depend on task-6 (commerce-handlers), task-7
(webhook-handler), or task-8 (worker-jobs) — those can be built in parallel
with this one; this task only *reads* rows those tasks will eventually
write (or, if this task ships first, rows that simply don't exist yet, in
which case the pages render empty/zero states).

### Conventions to follow (read these files before writing code)

- `internal/httpserver/middleware/org.go` — `RequireRole(roles ...string)`
  (org-scoped role gate, resolved by `ResolveOrg`, also lets a platform
  owner through) and `RequirePlatformOwner(profiles *models.ProfileRepo)`
  (platform-wide gate, no org context, requires `profiles.is_platform_owner`).
  Both are already implemented — this task only calls them, it does not
  modify `org.go`.
- `internal/httpserver/handlers/learner_ui.go` and its templates in
  `internal/httpserver/templates/` (`dashboard.html`,
  `course_learn.html`, `submissions.html`) — this is Task 5's precedent for
  "lightweight HTMX" server-rendered pages in this codebase: a plain
  `html/template` per page (no client-side framework, no CSS/JS build
  step), a package-level view struct per template (e.g.
  `dashboardCourseView`), a handler that loads data via the existing repo
  methods and calls `templates.X.Execute(c.Writer, gin.H{...})`, and
  `Content-Type: text/html; charset=utf-8` set explicitly before writing.
  Follow this exact pattern — do not introduce a new templating approach,
  a JS framework, or a build step.
- `internal/httpserver/handlers/course_editor_ui.go` (Task 4's course
  editor) set the "buttons not heavy JS" / "buttons not drag-drop"
  convention this codebase follows for authoring/admin UI generally: plain
  HTML controls, small inline `<script>` blocks only where a JSON API call
  is unavoidable, never a SPA-style reactive rewrite of the page. This
  task's pages have no mutations at all, so in practice this means: no
  inline fetch() scripts are needed here — every page is a single GET that
  renders fully server-side.
- `internal/httpserver/handlers/members.go`'s `ListMembers` already
  implements "every member of the resolved organization" via
  `d.Memberships.ListByOrg(ctx, tx, oc.OrgID)`. **Reuse this exact repo call
  from the new org-dashboard handler** — do not write a second
  member-listing query. `MembershipRepo.ListByOrg` returns
  `[]MembershipWithProfile` (see `internal/models/membership.go`), which
  already carries `UserID`, `Email`, `FullName`, `Role`, `JoinedAt` — enough
  for the dashboard's member table as-is.
- `internal/models/course.go`'s `CourseRepo.List(ctx, q, orgID)` already
  returns every course in an org (`internal/httpserver/handlers` has a
  `ListCourses` JSON handler using it). **Reuse this exact repo call** for
  the dashboard's course list — do not write a second course-listing query.
  `Course` already carries `Status` for the "publish status" column the
  spec asks for.
- `internal/httpserver/handlers/deps.go`'s `AuthDeps` struct is where every
  handler's repo dependencies are wired (e.g. `Profiles`, `Orgs`,
  `Memberships`, `Courses`). Add the three new Task-4 repos this task needs
  (`Orders *models.OrderRepo`, `Entitlements *models.EntitlementRepo`,
  `PlatformSettings *models.PlatformSettingsRepo`) to this struct under a
  `// Task 6: commerce.` comment block, matching the existing `// Task 4:
  course domain.` / `// Task 5: learner journey.` section comments. If
  task-6-commerce-handlers or task-6-webhook-handler have already added
  these fields by the time this task is implemented, do not duplicate them
  — just consume what's there.

### Dependency this task expects from Task 4 (models-repositories)

This task assumes the following repo methods exist on top of the schema
defined in task-1-db-migration.md. If Task 4 lands without one of these
exact methods, add the missing method as part of *this* task rather than
duplicating its query logic inline in a handler — repo methods belong in
`internal/models`, not in `internal/httpserver/handlers`, following every
prior task's layering.

1. **`OrgRepo.ListAll(ctx, q) ([]*Organization, error)`** — lists every
   organization on the platform, no `org_id` filter. This almost certainly
   does not exist yet (`internal/models/organization.go` currently only has
   `Create`, `GetBySlug`, `Update`, `Delete`, `GetBunnyLibraryID`,
   `SetBunnyLibraryID` — none list across orgs). Add it if missing. Needed
   for the platform-owner cross-org list. Since `organizations` rows are
   only visible to a caller under `is_org_member`-style RLS in normal
   session context, this method must run under a platform-owner-authorized
   session (the same trust boundary `RequirePlatformOwner` already
   enforces at the HTTP layer before this method is ever called) —
   consider whether the underlying RLS policy on `organizations` already
   grants platform owners a bypass (check `db/migrations/000002_auth_tenancy.up.sql`
   for an `app_is_platform_owner()` clause on `organizations_select`); if
   it does not, that is a gap for whoever lands `OrgRepo.ListAll` to flag,
   not silently work around with a service-role connection.

2. **`OrderRepo` — a per-org revenue/enrollment summary method.**
   `plans/lms-mvp/task-6-commerce.md`'s "Revenue and reporting" section
   describes a creator-facing report ("total net-of-commission revenue per
   course and per offer... filters for date ranges and offer types") that
   task-6-commerce-handlers is expected to build as its own reporting
   endpoint. This dashboard's "Enrollment overview: per-course enrollment
   counts and revenue (net of platform commission)" panel is the *same
   underlying data*, just embedded in a page instead of returned as its own
   JSON report. To avoid duplicating that aggregation query in two places,
   this task expects `OrderRepo` to expose a shared method along these
   lines:

   ```go
   // RevenueByCourse aggregates succeeded orders for the given org,
   // grouped by course, optionally filtered by a date range and/or offer
   // type. Returns per-course order count and total net-of-commission
   // revenue (total - commission_amount), segmented by currency (never
   // summed across currencies — see grilling-record.md Q1).
   func (r *OrderRepo) RevenueByCourse(ctx context.Context, q Querier, orgID string, filter RevenueFilter) ([]CourseRevenueSummary, error)
   ```

   where `RevenueFilter` carries optional `From, To *time.Time` and
   `OfferType *string`, and `CourseRevenueSummary` carries at least
   `CourseID, CourseTitle string`, `Currency string`, `OrderCount int`,
   `NetRevenue string` (or `decimal`/`NUMERIC`-compatible type matching
   this repo's existing money-column convention). If
   task-6-commerce-handlers has already landed this method (or an
   equivalently-shaped one) by the time this task is implemented, **call
   that exact method** rather than adding a second one — check
   `internal/models` for an existing `OrderRepo` method with "revenue" or
   "report" in its name before adding a new one. If neither exists yet,
   add `RevenueByCourse` as specified above; task-6-commerce-handlers'
   own revenue-report endpoint should then be updated to call it too (leave
   a comment noting this expectation if you land first).

3. **`OrderRepo` — a platform-wide commission summary method**, needed for
   the platform-owner cross-org list's "commission revenue generated"
   column, segmented by currency:

   ```go
   // CommissionByOrg aggregates succeeded orders across ALL organizations,
   // grouped by org and currency, returning each org's total commission
   // collected (commission_amount summed, per currency — never summed
   // across currencies).
   func (r *OrderRepo) CommissionByOrg(ctx context.Context, q Querier) ([]OrgCommissionSummary, error)
   ```

   This method necessarily reads across every org's `orders` rows in one
   query, so — like `OrgRepo.ListAll` above — it must run in a
   platform-owner-authorized session context; the HTTP layer's
   `RequirePlatformOwner` gate is what makes this safe to call, not
   anything inside the query itself. Flag (do not silently invent) any RLS
   gap this surfaces, the same way as item 1.

4. **`EntitlementRepo` — used indirectly.** Enrollment *counts* per course
   for the org-scoped view can come from either
   `LearnerCourseAccessRepo` (Task 5, already merged — it has
   org/course-scoped rows) or the new `EntitlementRepo`. Prefer counting
   `learner_course_access` rows with `access_status = 'active'` per course
   (via a new `LearnerCourseAccessRepo` method if one doesn't already exist
   for this, e.g. `CountActiveByOrg(ctx, q, orgID) (map[string]int, error)`
   keyed by course ID) since that table already represents "does this
   learner have access," per Task 5/6's design — `entitlements` is the
   grant record behind it, not a second access-tracking table. If
   `LearnerCourseAccessRepo` has no such counting method yet, add it as
   part of this task rather than doing a raw SQL query inline in the
   handler.

5. **`PlatformSettingsRepo`** is not directly needed by either dashboard
   page's read path (the commission *rate* isn't displayed here — only
   commission *revenue already collected*, which comes from
   `orders.commission_amount`, snapshotted per-order at purchase time per
   `task-1-db-migration.md`). Do not build a commission-rate-editing UI in
   this task — `platform_settings` UPDATE is out of scope here (no bulk
   admin actions / no feature-flag or quota management per the spec).

### Page 1: org-scoped admin dashboard

- **Route**: `GET /orgs/:org_slug/admin` (HTML page, alongside the existing
  `/orgs/:org_slug` JSON routes registered in `server.go`'s org-resolving
  route group — reuse that group's existing `ResolveOrg` middleware chain
  rather than building a new one).
- **Gate**: `middleware.RequireRole(auth.RoleOwner)` — per the commerce
  spec's literal wording, "visible to organization owners" (not teachers).
  This matches `permissions-matrix`'s `"dashboard.org.view"` action, which
  is owner-only in `permissionMatrix`. `RequireRole` already lets a
  platform owner through too (see `org.go`'s doc comment), so a platform
  owner can open any org's `/admin` page without being a member — this is
  intentional and matches the "support purposes" framing in the spec (the
  platform-owner drill-down in Page 2 links here).
- **Handler**: new `OrgAdminDashboardPage(d *AuthDeps) gin.HandlerFunc` in a
  new file `internal/httpserver/handlers/admin_ui.go` (parallel to
  `learner_ui.go`'s naming). Loads, in this order:
  1. Members via `d.Memberships.ListByOrg(ctx, tx, oc.OrgID)` (reused from
     `members.go`, see above).
  2. Courses via `d.Courses.List(ctx, tx, oc.OrgID)` (reused from the
     existing course-listing path).
  3. Per-course enrollment counts (see dependency item 4 above).
  4. Per-course revenue via `d.Orders.RevenueByCourse(ctx, tx, oc.OrgID,
     models.RevenueFilter{})` (see dependency item 2) — no filter applied
     by default; **support optional `?from=`, `?to=`, `?offer_type=` query
     params** on this same route, parsed and passed into the filter, so an
     owner can narrow the enrollment/revenue table without a second page —
     render the filter as a plain HTML `<form method="get">` with date
     inputs and a select, following `course_editor.html`'s "buttons not
     JS" convention (a normal GET form submit, no JS needed since HTMX/JS
     isn't required for a plain query-param GET).
- **Template**: new `internal/httpserver/templates/admin_dashboard.html`,
  registered in `templates.go` as `AdminDashboard` (add it to the existing
  `//go:embed` directive and `template.Must(template.ParseFS(...))` block
  — do not create a second embed.FS). Three sections, following
  `dashboard.html`'s existing `<section>`/`.card`/`.empty` CSS pattern
  (reuse the same inline `<style>` block verbatim rather than inventing new
  styling, for visual consistency across every Task 4/5/6 HTML page):
  1. **Members**: a table of email, full name, role, joined date.
  2. **Courses**: a table of title, status (draft/published/archived —
     whatever `Course.Status`'s actual values are per Task 4), created
     date.
  3. **Enrollment & revenue overview**: a table keyed by course, columns
     for active-enrollment count, order count, and net revenue **grouped by
     currency** (e.g. two revenue columns, "Net Revenue (INR)" / "Net
     Revenue (USD)", or one row per currency per course if a course has
     sales in both — never a single summed total, per the currency
     decision in `grilling-record.md` Q1). The date-range/offer-type filter
     form sits above this table.
- No row in any of these three tables is a link to an edit/mutate action.
  Course titles MAY link to the existing (Task 4) course-editor page
  (`/orgs/:org_slug/courses/:courseId/edit` or whatever that route actually
  is per `server.go` — check before assuming) since that's just navigation
  to an existing page this task doesn't own, not a new mutation.

### Page 2: platform-owner cross-org dashboard

- **Route**: `GET /admin/organizations` (platform-wide, no `:org_slug` in
  the path — register in `server.go`'s top-level `authed` group alongside
  other authentication-only routes, not inside an org-resolving group,
  since `RequirePlatformOwner` has no org context per its own doc comment).
- **Gate**: `middleware.RequirePlatformOwner(d.Profiles)` only. Per the
  commerce spec's literal wording, "visible only to profiles.is_platform_owner"
  — do not also allow org owners here, and do not route this through
  `RequireRole`/`Can()` at all (permissions-matrix's task file explicitly
  says platform-owner cross-org dashboard viewing is "not org-scoped" and
  must not get a `permissionMatrix` entry).
- **Handler**: new `PlatformAdminDashboardPage(d *AuthDeps) gin.HandlerFunc`
  in the same `admin_ui.go` file. Loads:
  1. All organizations via `d.Orgs.ListAll(ctx, tx)` (see dependency item
     1 above).
  2. For each org: member count (`len(d.Memberships.ListByOrg(...))` or a
     dedicated `CountByOrg` method if one exists — prefer adding a
     `MembershipRepo.CountByOrg(ctx, q, orgID) (int, error)` over loading
     full rows just to `len()` them, if performance matters at this scale;
     either is acceptable for MVP, but a count-only query is preferred if
     cheap to add), course count (similarly, prefer a `CourseRepo.CountByOrg`
     if adding one is cheap, else `len(List(...))`), and enrollment count
     (see dependency item 4, summed across the org's courses).
  3. Commission revenue per org via `d.Orders.CommissionByOrg(ctx, tx)`
     (see dependency item 3), segmented by currency (never summed).
- **Template**: new `internal/httpserver/templates/admin_organizations.html`,
  registered as `AdminOrganizations` in `templates.go`, same styling reuse
  as above. One table: org name/slug, member count, course count,
  enrollment count, commission revenue per currency. Each row's org
  name/slug links to a **drill-down page** (see below) — this is the only
  navigation in the whole task, and it is read-only navigation, not an
  action button.
- **Drill-down page**: `GET /admin/organizations/:org_slug` (also
  `RequirePlatformOwner`-gated, also outside any `ResolveOrg` group since a
  platform owner viewing an arbitrary org here should not require
  `ResolveOrg`'s membership-or-platform-owner resolution dance — just look
  up the org directly by slug via `d.Orgs.GetBySlug`). Handler
  `PlatformAdminOrgDetailPage(d *AuthDeps) gin.HandlerFunc`, reusing the
  exact same three data loads as Page 1's org-scoped dashboard (members,
  courses, enrollment/revenue-by-course) for that one org — this is
  explicitly a **view**, not an edit page: render with the same
  `admin_dashboard.html` template as Page 1 if the data shape matches
  exactly (simplest: have `PlatformAdminOrgDetailPage` call the same
  internal data-loading helper `OrgAdminDashboardPage` uses, factored into
  a shared unexported function, and execute `templates.AdminDashboard` the
  same way) rather than hand-rolling a near-duplicate template. No edit
  controls, no forms that POST/PATCH/DELETE anywhere on this page — the
  spec is explicit: "ability to VIEW (not edit)... explicitly no bulk
  actions, no edit capability."

### Explicitly out of scope (do not build in this task)

- The refund button / `"refund.initiate"` UI — task-6-commerce-handlers.
- The entitlement-grant form / `"entitlement.grant"` UI —
  task-6-commerce-handlers.
- Any bulk admin action (bulk role change, bulk course archive, etc.).
- Feature flags or quota management of any kind.
- Editing an organization's settings from the platform-owner drill-down
  page (view only).
- Editing `platform_settings.commission_percent` from any page here (that
  belongs to whichever task builds the platform-owner commission-rate
  config endpoint referenced in `task-3-permissions-matrix.md`'s "out of
  scope" section, not this one).
- Any JSON API endpoint mirroring these pages — this task is HTML-page-only,
  matching Task 5 Stage 8's precedent (the *data* these pages read may
  overlap with task-6-commerce-handlers' own JSON revenue-report endpoint,
  per the shared-repo-method guidance above, but this task itself adds no
  new `/api/...` routes).

## Acceptance criteria

- [ ] `GET /orgs/:org_slug/admin` renders an HTML page (not JSON) showing:
      the org's member list with roles, the org's course list with publish
      status, and a per-course enrollment-count + net-of-commission-revenue
      table segmented by currency.
- [ ] `GET /orgs/:org_slug/admin` is gated by
      `middleware.RequireRole(auth.RoleOwner)` — a `teacher`, `moderator`,
      or `learner` member of the org gets 403; a non-member, non-platform-owner
      caller gets 403 (or 404, per `ResolveOrg`'s existing behavior for a
      slug the caller can't see); an owner of the org succeeds; a platform
      owner who is not a member of the org also succeeds.
- [ ] The org-scoped page accepts optional `?from=`, `?to=`, `?offer_type=`
      query params that narrow the enrollment/revenue table via a plain
      HTML `<form method="get">` (no JS submission required).
- [ ] `GET /admin/organizations` renders an HTML page listing every
      organization on the platform with member count, course count,
      enrollment count, and commission revenue **segmented by currency
      (INR and USD never summed together)**.
- [ ] `GET /admin/organizations` is gated by
      `middleware.RequirePlatformOwner(d.Profiles)` only — any
      authenticated non-platform-owner (including an org owner of some
      other org) gets 403; no `permissionMatrix`/`Can()` check is added for
      this route.
- [ ] `GET /admin/organizations/:org_slug` renders a read-only drill-down
      of a single organization's basic details (members, courses,
      enrollment/revenue), gated the same way as `/admin/organizations`,
      reachable by clicking a row on that page.
- [ ] None of the three pages contains any HTML form or control that
      issues a POST/PATCH/PUT/DELETE request, or a link to any such
      endpoint — verified by inspecting the rendered templates: only
      `<form method="get">` (the filter form) and plain `<a href>`
      navigation links exist.
- [ ] The member list and course list on the org-scoped page are populated
      via the existing `MembershipRepo.ListByOrg` and `CourseRepo.List`
      methods (or their platform-drill-down equivalents) — no duplicate
      SQL query is written in `internal/httpserver/handlers` to reproduce
      what those repo methods already do.
- [ ] Revenue/enrollment aggregation lives in `internal/models` repo
      methods (`OrderRepo.RevenueByCourse`, `OrderRepo.CommissionByOrg`, and
      any new count-only membership/course/access methods), not as raw SQL
      or aggregation logic inline in `internal/httpserver/handlers`.
- [ ] If `OrderRepo.RevenueByCourse` (or an equivalently-purposed method)
      does not already exist when this task starts, it is added here and
      is written so that task-6-commerce-handlers' own revenue-report
      endpoint can call the same method rather than duplicating the query
      (leave a doc-comment cross-reference either way).
- [ ] New templates (`admin_dashboard.html`, `admin_organizations.html`)
      follow `dashboard.html`'s existing inline `<style>` block and
      `.card`/`.progress`/`.empty` class conventions for visual consistency
      with Task 4/5's pages, and are registered in
      `internal/httpserver/templates/templates.go`'s single `embed.FS` /
      `template.Must(template.ParseFS(...))` block (no second `embed.FS`).
  - [ ] New handlers live in `internal/httpserver/handlers/admin_ui.go`,
      following `learner_ui.go`'s file-header-comment convention describing
      the file's scope.
- [ ] `AuthDeps` (`internal/httpserver/handlers/deps.go`) gains
      `Orders *models.OrderRepo`, `Entitlements *models.EntitlementRepo`,
      and `PlatformSettings *models.PlatformSettingsRepo` fields under a new
      `// Task 6: commerce.` comment block, unless already added by a
      concurrently-landed sibling task in this same plan (task-6/7/8), in
      which case no duplicate fields are added.
  - [ ] Routes are wired in `internal/httpserver/server.go` in this task
      (even though a later task-10 "routes-wiring" step exists for the rest
      of Task 6, this task's three GET-only HTML routes have no dependency
      on commerce-handlers/webhook-handler/worker-jobs and can be wired
      immediately once this task's handlers exist — do not leave them
      unwired pending task-10).
- [ ] `go build ./...` passes and any new/changed Go files have no
      `internal/httpserver/handlers` → raw-SQL-in-handler violations (all
      DB access goes through `internal/models` repos via the existing
      `Querier` pattern).

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
