---
task: 10
name: routes-wiring
parallel_group: 4
depends_on: [6, 7, 8, 9]
issue: TBD
---

# Task 10: routes-wiring

## What to build

Wire Task 6's already-built commerce/payments components into the Gin
engine in `internal/httpserver/server.go`: the commerce-handlers task
(task-6, `commerce-handlers`), the webhook-handler task (task-7), and the
admin-dashboard task (task-9) all produce handler functions but do not
register any routes themselves — this task is the single integration
point that mounts them, applies rate limiting, and double-checks every
route's permission middleware against `plans/task-6-implementation/task-3-permissions-matrix.md`.
It also builds the one piece of UI glue that doesn't belong to any single
handler task: the order-status "processing" page template that HTMX-polls
task-6's JSON order-status endpoint.

This file assumes task-6 (commerce-handlers), task-7 (webhook-handler),
task-8 (worker-jobs), and task-9 (admin-dashboard) are already merged.
Read their code before starting — the exact handler function names,
`AuthDeps` field names, and repo constructors below are this author's
best-effort projection from `plans/lms-mvp/task-6-commerce.md` and
`plans/task-6-implementation/main-plan.md`/`grilling-record.md`; if the
merged code names things differently, follow what actually exists and
adjust the route registration accordingly — the important thing is the
route *shape*, *middleware stack*, and *permission mapping* described
here, not the literal Go identifiers.

### Background: existing route-registration conventions

Read `internal/httpserver/server.go` in full before starting. It already
establishes every pattern this task reuses:

- `New(cfg, logger, db, redisClient) *gin.Engine` builds `handlers.AuthDeps`
  (a single struct of repos/clients shared by every handler) and calls one
  `register*Routes(engine, deps, ...)` function per domain area
  (`registerAuthRoutes`, `registerOrgRoutes`, `registerCourseRoutes`,
  `registerLearnerRoutes`, `registerLearnerUIRoutes`). This task adds
  `registerCommerceRoutes` and `registerAdminRoutes` (or equivalent split —
  see "Route grouping" below) called from `New` alongside the existing
  five.
- **Course-scoped resources are flat**: `/api/courses/:courseId/...`,
  resolved via `middleware.ResolveCourseOrg(d.Courses, d.Memberships,
  d.Profiles)` rather than an `:org_slug` path segment (see
  `registerCourseRoutes` and `registerLearnerRoutes`, both of which reuse
  this exact middleware). This is the established convention for anything
  that hangs off a specific course, and it is the convention **offers**
  must follow, since an offer is always tied to a course (per
  `task-6-commerce.md`: "Each offer is tied to a course").
- **Resources with no course/org in their path resolve context
  internally or need no org context at all** — e.g. `CreateCourse`/
  `ListCourses` resolve org by slug from the request body rather than the
  URL; `ListCertificates` needs only `Authenticate` since its query is
  learner-scoped by RLS. The same reasoning applies to a **standalone
  checkout page reached by offer ID** (see "Route grouping" below).
- **Rate limiting** is applied per-route via a small closure factory, not
  global middleware — see `registerAuthRoutes`'s `authLimit` helper:
  ```go
  authLimit := func(prefix string) gin.HandlerFunc {
  	limiter := ratelimit.New(redisClient, "ratelimit:"+prefix, 5, 15*time.Minute)
  	return middleware.RateLimit(limiter, middleware.ByClientIP)
  }
  ```
  `ratelimit.New(client, prefix, limit, window)` builds a Redis-backed
  fixed-window limiter; `middleware.RateLimit(limiter, keyFn)` turns it
  into `gin.HandlerFunc`; `middleware.ByClientIP` is the existing per-IP
  key function. `internal/ratelimit/ratelimit.go`'s `Limiter.Allow` fails
  **open** on a Redis outage (traffic is never blocked by a Redis
  failure), which is relevant when picking limits for the webhook route
  below.
- **Webhook routes skip session/CSRF entirely.** The existing precedent is
  the Bunny transcode-complete webhook in `registerCourseRoutes`:
  ```go
  engine.POST("/api/webhooks/bunny", handlers.BunnyWebhook(d))
  ```
  Registered directly on `engine` (not on any `authed`/`org`/`course`
  group), with **no** `Authenticate`, `WithRequestTx`, `ResolveOrg`,
  `ResolveCourseOrg`, `RequireRole`, or CSRF middleware — the handler
  itself verifies the HMAC signature before doing anything, matching
  `CLAUDE.md`'s "payment/enrollment access must only ever be granted after
  verified provider webhook events" rule. The Razorpay webhook route must
  follow this identical pattern.
- **Cookie-authenticated HTML routes** (course editor, learner UI) live in
  their own `engine.Group("")` with `Authenticate` (which accepts either a
  bearer header or the `lms_session` cookie) + `WithRequestTx`, and add
  `middleware.EnsureCSRFCookie(d.Config)` + `middleware.RequireCSRF()` on
  mutating routes only when the page itself performs a
  cookie-authenticated HTML form/HTMX POST directly (the course editor).
  Pages that only GET, and whose mutations are inline `fetch()` calls to
  the existing JSON API (the learner UI pages), skip CSRF entirely — see
  `registerLearnerUIRoutes`'s doc comment for why. The order-status page
  built by this task follows the learner-UI pattern: GET-only page, its
  poll requests are all read-only.
- **Permission enforcement** always doubles up two independent layers:
  `middleware.RequireRole(...)` (backed by `auth.Can(role, action)` and
  `permissionMatrix` in `internal/auth/permissions.go`) or
  `middleware.RequirePlatformOwner(d.Profiles)` at the route layer, plus
  RLS at the database layer. Never rely on RLS alone for a route that
  should also be role-gated at the HTTP layer.

### 1. Register commerce-handlers routes (task-6)

All commerce routes require `Authenticate` + `WithRequestTx`, matching
every other authenticated route group. Add a new
`registerCommerceRoutes(engine *gin.Engine, d *handlers.AuthDeps, db
*pgxpool.Pool, redisClient *redis.Client)` (needs `redisClient` for the
checkout rate limit) called from `New` after `registerLearnerUIRoutes`.

**Course-scoped, under the flat `/api/courses/:courseId/...` +
`ResolveCourseOrg` convention** (reuse a `course := authed.Group("/courses/:courseId")`
group with `course.Use(middleware.ResolveCourseOrg(d.Courses,
d.Memberships, d.Profiles))`, the same shape `registerCourseRoutes` and
`registerLearnerRoutes` already use — this is a **separate** Gin group
local to this function, not a shared variable across functions):

| Method | Path | Middleware | Permission |
|---|---|---|---|
| POST | `/api/courses/:courseId/offers` | `authoring` (RequireRole(Owner, Teacher)) | `auth.Can(role, "offer.create")` enforced inside `RequireRole` via the matrix |
| GET | `/api/courses/:courseId/offers` | `entitled`-equivalent read is NOT required — any org member (or the public, if offers are meant to be visible pre-purchase) may list a course's offers to see pricing; use no extra gate beyond `ResolveCourseOrg` unless task-6's handler itself expects a role check (verify against the actual handler signature) | none beyond org-member visibility |
| PATCH | `/api/courses/:courseId/offers/:offerId` | `authoring` | `offer.update` |
| POST | `/api/courses/:courseId/offers/:offerId/archive` | `authoring` | `offer.archive` |
| POST | `/api/courses/:courseId/offers/:offerId/discount-codes` | `authoring` | `discount.create` |
| PATCH | `/api/courses/:courseId/offers/:offerId/discount-codes/:codeId` | `authoring` | `discount.update` |
| POST | `/api/courses/:courseId/offers/:offerId/discount-codes/:codeId/archive` | `authoring` | `discount.archive` |
| POST | `/api/courses/:courseId/offers/:offerId/invite-tokens` | `authoring` | `invitetoken.create` |
| POST | `/api/courses/:courseId/entitlements/grant` | `authoring` | `entitlement.grant` (reason required by the handler, not this layer) |
| GET | `/api/courses/:courseId/revenue-report` | `middleware.RequireRole(auth.RoleOwner)` (owner-only per the matrix) | `report.revenue.view` |

Where `authoring := middleware.RequireRole(auth.RoleOwner,
auth.RoleTeacher)` (mirror `registerCourseRoutes`'s own local `authoring`
var — each `register*Routes` function defines its own copy rather than
sharing one across functions, matching existing style).

`auth.Can` itself is not called directly in routing code anywhere in this
codebase — `RequireRole` only checks the caller's role is in the allowed
set (or is a platform owner). The `permissionMatrix`/`Can()` distinction
in `task-3-permissions-matrix.md` (Task 6's own permissions task) documents
which actions each role *should* be allowed, and this table's "Permission"
column exists so the acceptance criteria below can cross-check that
`RequireRole`'s argument list matches what that matrix grants — it is not
a second runtime check.

Revenue report is owner-only per `ownerOnlyCommerceDomainActions`
(`"report.revenue.view"`) — do **not** grant teachers access even though
they can create offers, matching the matrix exactly.

**Not course-scoped — standalone checkout and order-status pages, reached
by offer ID or order ID rather than course ID:**

Decision: mount these on a flat `/checkout/...` and `/orders/...` prefix,
NOT nested under `/api/courses/:courseId/...`, for two reasons: (1) the
checkout page is reached from an offer listing and the natural key a
learner clicks through is the offer, not the course — forcing a
`:courseId` segment into the URL means either duplicating it redundantly
alongside `:offerId` or doing an extra lookup to derive it, and (2) unlike
every other course-scoped route in this codebase, checkout is a page a
*prospective* learner (possibly already an org member, possibly not yet
enrolled) visits before any entitlement exists, so gating it behind
`ResolveCourseOrg` (which 404s non-members) is the wrong shape — it should
resolve the offer, and from the offer derive the org for RLS purposes,
independently of `:courseId` being in the URL at all. This mirrors how
`CreateCourse`/`ListCourses` and `ListCertificates` already resolve
context without an `:org_slug`/`:courseId` path segment when the natural
key is something else.

| Method | Path | Middleware | Notes |
|---|---|---|---|
| GET | `/checkout/:offerId` | `Authenticate` + `WithRequestTx` (no `ResolveOrg`/`ResolveCourseOrg` — the handler loads the offer, derives org internally, and stamps RLS context itself; verify against task-6's actual handler signature and adjust if it already expects an org-resolving middleware) | Server-rendered HTML+HTMX checkout page embedding Razorpay `checkout.js` (per `grilling-record.md` Q8) |
| POST | `/checkout/:offerId` | same as GET, PLUS `checkoutLimit` (see rate limiting below) | Creates the server-side order; amount/currency/tax/discount/commission computed server-side, never trusted from the request body |
| GET | `/orders/:orderId/status` | `Authenticate` + `WithRequestTx` | JSON polling endpoint task-6 builds; returns whether the order's entitlement now exists. Consumed by this task's order-status HTML page (below), not the JSON API test console |
| POST | `/orders/:orderId/refund` | `Authenticate` + `WithRequestTx` + `middleware.RequireRole(auth.RoleOwner)` | owner-only per `refund.initiate` |

If task-6's handlers instead expose these as `/api/offers/:offerId/checkout`
and `/api/orders/:orderId/...` (a JSON-only API shape) with a *separate*
HTML template route for the page itself, register both: the JSON route
under `/api/...` following this codebase's existing `/api/*` convention,
and a thin HTML page route (`GET /checkout/:offerId`) that renders the
template and lets the page's own inline script call the JSON route — this
is the same split `registerLearnerUIRoutes` uses relative to
`registerLearnerRoutes`. Confirm which shape task-6 actually produced
before finalizing this file's routes.

### 2. Register the Razorpay webhook route (task-7)

```go
engine.POST("/api/webhooks/razorpay", handlers.RazorpayWebhook(d))
```

Registered directly on `engine`, with **no** `Authenticate`,
`WithRequestTx`, `ResolveOrg`/`ResolveCourseOrg`, or `RequireRole` —
identical in shape to the existing `/api/webhooks/bunny` registration.
The handler (built by task-7) verifies the Razorpay webhook HMAC signature
itself before doing anything; do not add any framework-level auth in
front of it, since Razorpay's caller has no session/bearer token to
present. Apply only the webhook rate limit described below.

### 3. Register admin dashboard routes (task-9)

Add `registerAdminRoutes(engine *gin.Engine, d *handlers.AuthDeps, db
*pgxpool.Pool)`, called from `New`.

**Org-scoped dashboard** (own org only): reuse the `/api/orgs/:org_slug`
+ `ResolveOrg` convention already established in `registerOrgRoutes`/
`registerCourseRoutes` (a new local group in this function, same
middleware stack):

```go
authed := engine.Group("/api")
authed.Use(middleware.Authenticate(d.Verifier))
authed.Use(middleware.WithRequestTx(db))

org := authed.Group("/orgs/:org_slug")
org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))
org.GET("/dashboard", middleware.RequireRole(auth.RoleOwner), handlers.OrgDashboard(d))
```

`RequireRole(auth.RoleOwner)` matches `"dashboard.org.view"` in
`ownerOnlyCommerceDomainActions` — owner only, not teacher.

**Platform-owner cross-org dashboard**: per
`task-3-permissions-matrix.md`'s explicit instruction, this has no org
context at all and must use `middleware.RequirePlatformOwner(d.Profiles)`,
**not** `RequireRole`/the permission matrix:

```go
authed.GET("/admin/dashboard", middleware.RequirePlatformOwner(d.Profiles), handlers.PlatformDashboard(d))
```

Do not nest this under `/orgs/:org_slug` — it spans all organizations by
definition, so it takes no `:org_slug` parameter and does not run
`ResolveOrg` at all.

### 4. Rate limiting

Add two more `*Limit` helper closures alongside `registerAuthRoutes`'s
`authLimit`, following the exact same construction pattern (`ratelimit.New`
+ `middleware.RateLimit`):

- **Checkout create-order** (`POST /checkout/:offerId`): abuse-prone in
  the same way login/register are (a bad actor could hammer order
  creation to probe pricing/discount logic or exhaust Razorpay API
  quota). Reuse the auth-route shape: 5 requests per 15 minutes, keyed by
  `middleware.ByClientIP`:
  ```go
  checkoutLimit := ratelimit.New(redisClient, "ratelimit:checkout-create", 5, 15*time.Minute)
  ```
  Apply `middleware.RateLimit(checkoutLimit, middleware.ByClientIP)` to
  the `POST /checkout/:offerId` route only (not the GET). If task-6's
  checkout handler is per-authenticated-user rather than per-IP-meaningful
  (e.g. a household sharing an IP legitimately buying multiple courses),
  key by user ID instead via a small custom `KeyFunc` that reads
  `middleware.AuthContextFromGin(c).UserID` — check `internal/httpserver/middleware/auth.go`
  for the exact accessor name before wiring this, and prefer per-user
  keying if it exists, since IP-based limiting risks false positives for
  shared-IP legitimate buyers more than the auth routes' login-abuse case
  does.
- **Razorpay webhook** (`POST /api/webhooks/razorpay`): protect against
  retry storms, but Razorpay's own legitimate retry behavior must never
  be throttled into dropped events (a dropped webhook means a paying
  learner never gets their entitlement). Use a generous limit — e.g. 100
  requests per minute, keyed by client IP (Razorpay's webhook source IPs
  are a small, stable set, so an unexpectedly high volume from one IP is
  a meaningful signal, unlike a learner's browser IP):
  ```go
  webhookLimit := ratelimit.New(redisClient, "ratelimit:webhook-razorpay", 100, time.Minute)
  ```
  Because `ratelimit.Limiter.Allow` fails open on a Redis outage (see
  `internal/ratelimit/ratelimit.go`), a Redis blip never blocks a
  legitimate webhook delivery outright — this is a deliberate safety
  margin on top of the generous limit, not a substitute for choosing a
  high number. Do not reuse the 5-per-15-min auth limit shape here; it
  would be far too aggressive for a provider that may legitimately retry
  several times per minute during an incident.

Both new limiters are constructed with `redisClient` exactly like
`authLimit` — `registerCommerceRoutes`'s signature must accept
`redisClient *redis.Client` (see the signature above) since it isn't
currently threaded into any `register*Routes` function except
`registerAuthRoutes`.

### 5. Order-status "processing" page (new UI glue, built by this task)

This is the one piece of net-new code this task authors (everything else
is registration). It doesn't belong to task-6 (which only builds the JSON
`GET /orders/:orderId/status` polling endpoint) or task-8 (worker-only,
no HTTP surface) — it's the HTML page a learner's browser sits on between
clicking "Pay" in Razorpay's `checkout.js` widget and the webhook actually
landing.

Follow the exact pattern of the existing learner-UI templates
(`internal/httpserver/templates/course_learn.html` and
`internal/httpserver/templates/templates.go`):

1. Create `internal/httpserver/templates/order_status.html`:
   - Plain `html/template`, htmx via the same CDN `<script>` tag used by
     every other template in this package
     (`https://unpkg.com/htmx.org@1.9.10`), no build step, no JS
     framework — matching `templates.go`'s doc comment ("no client-side
     framework, no CSS/JS build step").
   - Root element polls the status endpoint every 2 seconds:
     ```html
     <div id="order-status"
          hx-get="/orders/{{.OrderID}}/status"
          hx-trigger="every 2s"
          hx-swap="outerHTML">
       Processing your payment&hellip;
     </div>
     ```
   - Because `GET /orders/:orderId/status` is a JSON endpoint (task-6's
     deliverable), not an HTML-fragment-returning endpoint, htmx's
     `hx-get` can't swap its raw JSON response into the DOM directly. Two
     ways to reconcile this — pick whichever is simpler given what task-6
     actually built, and document the choice in a comment in the handler:
     - **(a) Preferred:** have this task's own page-controller handler
       (see below) be what htmx polls, not the raw JSON endpoint directly:
       `hx-get="/orders/{{.OrderID}}/status-fragment"` pointing at a new
       thin handler in this task that calls the same repo/model lookup
       task-6's JSON handler uses (or calls it internally) and renders
       either a small "still processing" HTML fragment or, once an
       entitlement exists, a response carrying the `HX-Redirect` response
       header set to `/courses/:courseId/learn` — htmx follows
       `HX-Redirect` as a full client-side navigation automatically. This
       keeps task-6's JSON endpoint untouched and puts all the
       HTML-specific glue in this task, per the framing above.
     - (b) Alternative, only if task-6's endpoint already content-negotiates
       HTML vs JSON on `Accept` (mirroring the precedent already set by
       `handlers.VerifyCertificate`, see `server.go`'s comment on
       `/certificates/verify/:certificateId`): point `hx-get` directly at
       `/orders/:orderId/status` and skip the separate fragment route.
   - Once the entitlement exists, the redirect target is the course's
     learner landing page, `/courses/:courseId/learn` (existing route,
     see `registerLearnerUIRoutes`) — the order/offer record has the
     course ID needed to build this URL.
2. Add the parse/embed lines to `templates.go`:
   ```go
   //go:embed ... order_status.html
   var OrderStatus = template.Must(template.ParseFS(fs, "order_status.html"))
   ```
   (extend the existing single `//go:embed` directive's file list rather
   than adding a second one, matching the current style.)
3. Add a handler (new file, e.g.
   `internal/httpserver/handlers/order_status_ui.go`, matching
   `learner_ui.go`'s placement/naming convention) that renders the page
   template for `GET /orders/:orderId/status-page` (or whatever path this
   task chooses to serve the *page* at, distinct from the JSON/fragment
   polling path above — do not collide the two), plus the polling
   fragment/redirect handler from option (a) if that's the approach taken.
4. Wire both into `registerCommerceRoutes` (or a small dedicated
   `registerOrderStatusUIRoutes`, matching this codebase's granularity):
   `Authenticate` + `WithRequestTx` only, no CSRF (GET-only page, matching
   `registerLearnerUIRoutes`'s reasoning — nothing here POSTs directly).
   The checkout page's Razorpay `checkout.js` success handler should
   `window.location`-redirect the browser to this page after payment,
   **not** treat its own success callback as proof of payment — the page
   itself is what does the trustworthy work by polling until the webhook
   has landed, per `grilling-record.md` Q9 and `CLAUDE.md`'s "never from
   browser return URLs" rule. Do not let the checkout page or this status
   page grant access, set any session flag, or call any entitlement
   endpoint directly — they only read.

### 6. Cross-check against the permissions matrix

Before finishing, walk every route added in steps 1-3 against
`plans/task-6-implementation/task-3-permissions-matrix.md` line by line:

- Every `RequireRole(...)` call's argument list must match exactly which
  roles that matrix grants the corresponding action string to
  (`commerceDomainActions` → owner+teacher, `ownerOnlyCommerceDomainActions`
  → owner only).
- The two actions the matrix explicitly says must NOT go through
  `RequireRole`/`Can()` — platform commission configuration and the
  platform-owner cross-org dashboard — must use `RequirePlatformOwner`
  only, with no matrix entry, no `RequireRole` call, anywhere in this
  task's routes.
- If task-6 or task-9's handlers assume a role gate this task's route
  registration doesn't provide (or vice versa — a route gated more
  tightly than the handler expects, e.g. teacher access accidentally
  excluded from an offer-management endpoint), fix the mismatch in the
  route registration here rather than in the handler — this task is the
  documented integration point for exactly this class of bug, per the
  main-plan's Phase 4 placement after Phase 3's four parallel handler
  tracks.
- Grep the merged task-6/7/8/9 code for every exported handler function
  under `internal/httpserver/handlers/` whose name suggests a commerce,
  webhook, or admin-dashboard concern, and confirm each one is reachable
  from some route registered in this task — an unregistered handler is a
  bug this task must catch, not silently leave orphaned.

## Acceptance criteria

- [ ] `go build ./...` succeeds with all new routes wired.
- [ ] Every commerce-handlers route from task-6 (offer CRUD, discount code
      CRUD, invite token generation, checkout GET/POST, order-status GET,
      refund POST, entitlement-grant POST, revenue-report GET) is
      reachable at a registered route, with the middleware stack and
      route-grouping convention documented above (course-scoped resources
      under `/api/courses/:courseId/...` + `ResolveCourseOrg`; checkout/
      order-status under the flat `/checkout/...`/`/orders/...` prefix
      with the reasoning for that choice preserved as a comment in
      `server.go`).
- [ ] The Razorpay webhook route (`POST /api/webhooks/razorpay`) has NO
      `Authenticate`/`WithRequestTx`/`ResolveOrg`/`ResolveCourseOrg`/
      `RequireRole` middleware — registered directly on `engine`,
      identical in shape to the existing `/api/webhooks/bunny` route —
      and relies solely on the handler's own HMAC signature verification.
- [ ] The org-scoped admin dashboard route is gated by
      `middleware.RequireRole(auth.RoleOwner)` under the existing
      `/api/orgs/:org_slug` + `ResolveOrg` group.
- [ ] The platform-owner cross-org dashboard route is gated by
      `middleware.RequirePlatformOwner(d.Profiles)` only, takes no
      `:org_slug` parameter, and is NOT reachable via any `RequireRole`
      path — confirmed there is no corresponding `permissionMatrix` entry
      anywhere for this action.
- [ ] Checkout order-creation (`POST /checkout/:offerId`) and the
      Razorpay webhook route each have a distinct rate limiter
      constructed via `ratelimit.New` + `middleware.RateLimit`, following
      `registerAuthRoutes`'s `authLimit` closure pattern; the webhook
      limit is generous enough (documented reasoning, e.g. ~100/min) that
      Razorpay's own legitimate retry behavior is never dropped.
- [ ] The order-status processing page (`internal/httpserver/templates/order_status.html`)
      is embedded via `templates.go` following the existing single
      `//go:embed` directive convention, uses `hx-trigger="every 2s"` to
      poll, and on entitlement-confirmed response redirects the browser
      into `/courses/:courseId/learn` — implemented via `HX-Redirect` (or
      documented equivalent) rather than any client-side trust of the
      Razorpay `checkout.js` success callback.
- [ ] No route added by this task creates or revokes a
      `learner_course_access`/entitlement record directly from an HTTP
      handler reachable via a browser return URL or client-provided
      "payment succeeded" signal — every entitlement mutation still flows
      only through the webhook handler (task-7) or the admin-grant
      handler (task-6, itself audit-logged).
- [ ] Every `RequireRole(...)` call added by this task matches
      `plans/task-6-implementation/task-3-permissions-matrix.md` exactly
      (owner+teacher for `commerceDomainActions`-backed routes, owner-only
      for `ownerOnlyCommerceDomainActions`-backed routes); any mismatch
      discovered between what task-3 defined and what task-6/task-9's
      handlers expect is fixed in this task's route registration, and the
      fix is noted in the commit message.
- [ ] Manual smoke test (curl or the existing `/test-console` pattern,
      per `task-4-implementation/task-10-routes-wiring.md`'s precedent):
      create an offer as a teacher, attempt the same as a learner and
      confirm 403, hit `POST /api/webhooks/razorpay` with an invalid
      signature and confirm it's rejected without ever touching auth
      middleware, load the order-status page for a still-pending order
      and confirm it renders the polling markup rather than erroring.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
