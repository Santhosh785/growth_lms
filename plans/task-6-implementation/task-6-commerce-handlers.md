---
task: 6
name: commerce-handlers
parallel_group: 3
depends_on: [3, 4, 5]
issue: TBD
---

# Task 6: commerce-handlers

## What to build

Build the learner-facing and teacher/owner-facing HTTP handlers for
commerce in a new file `internal/httpserver/handlers/commerce.go` (or, if
the number of endpoints makes one file unwieldy, split by concern —
`commerce_offers.go` (offers + discount codes + invite tokens),
`commerce_checkout.go` (checkout page + create-order + order-status),
`commerce_refunds.go` (refunds + manual entitlement grant),
`commerce_reports.go` (revenue reporting) — matching Task 4's precedent of
splitting `courses.go` / `chapters.go` / `lessons.go` / `blocks.go` /
`categories.go` / `tags.go` / `collections.go` one-concern-per-file rather
than one giant handlers file). Register constructors in `AuthDeps`
(`internal/httpserver/handlers/deps.go`) alongside the existing `// Task 5:
learner journey` block, adding a new `// Task 6: commerce` block with the
new repos this task depends on (`Offers`, `DiscountCodes`, `InviteTokens`,
`Orders`, `Entitlements`, `Refunds`, `PaymentAuditTrail`, plus a
`Payments payments.Provider` field for the Razorpay client) — do not
redesign these repo/interface shapes here; they are Task 4
(models-repositories) and Task 5 (razorpay-client)'s deliverables. If a
method this task needs turns out not to exist on those repos/interfaces
(e.g. a specific `GetActiveByOffer` lookup, or a `Provider.CreateRefund`
signature that doesn't match what's needed here), note it explicitly as an
assumption/gap in a code comment at the call site rather than silently
inventing repo internals — a later task or a follow-up fix can reconcile
it.

### Boundary — what this task does NOT include

- The Razorpay webhook endpoint and its signature verification / asynq
  enqueue (task-7, webhook-handler). This task's handlers never mark an
  order `succeeded` or create a paid entitlement — only the webhook-driven
  worker job does that.
- The order-status HTMX polling page **template** and its route wiring
  (task-10, routes-wiring / UI). This task builds the JSON endpoint the
  polling page will call (`GET .../orders/:orderId/status`), but not the
  page itself.
- Worker sweep jobs: stale-pending-order abandonment, fixed-term-pass
  expiry, refund/chargeback webhook processing (task-8, worker-jobs).
- The admin dashboard (org-scoped + platform cross-org) — that is task-9,
  admin-dashboard, a separate read-only reporting surface. This task's
  "Revenue reporting" endpoint is a narrower, creator-facing
  per-course/per-offer report, not the dashboard.
- Route registration into `internal/httpserver/server.go` and Gin group
  wiring — that happens in task-10 (routes-wiring), though this task's doc
  comments on each handler should say which middleware chain they expect
  (mirroring every existing handler's doc-comment convention, e.g.
  `learner.go`'s `EnrollCourse` comment or `orgs.go`'s `UpdateOrg`
  comment), so task-10 can wire them mechanically.

### Conventions to follow exactly (read before writing code)

Read `internal/httpserver/handlers/orgs.go`, `internal/httpserver/handlers/learner.go`,
and `internal/httpserver/handlers/learner_ui.go` first — they establish every
pattern this task must reuse:

- **Handler shape**: `func HandlerName(d *AuthDeps) gin.HandlerFunc`,
  closing over `*AuthDeps`. Pull `tx` via
  `middleware.RequestTxFromGin(c)`, the caller via
  `middleware.AuthContextFromGin(c)`, and org context via
  `middleware.OrgContextFromGin(c)` — never open a new DB connection or
  transaction inside a handler; the request's transaction (set up by the
  `WithRequestTx` middleware, per Task 4/5's `dbctx` pattern) is what
  carries the RLS session variables (`app.current_org_id`,
  `app.current_role`) set by `ResolveOrg`/`ResolveCourseOrg`, so every
  repo call must use that same `tx`.
- **Permission gating is via `middleware.RequireRole(...)` on the route**,
  not by calling `auth.Can()` inside the handler body — `Can()` /
  `permissionMatrix` (task-3, permissions-matrix,
  `internal/auth/permissions.go`) is a documentation/testing reference,
  not a runtime call site; every existing route (see
  `internal/httpserver/server.go`) gates with
  `middleware.RequireRole(auth.RoleOwner)`,
  `middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)`, etc.,
  applied either per-route or via `.Use(...)` on a sub-group (see
  `server.go`'s `authoring := middleware.RequireRole(auth.RoleOwner,
  auth.RoleTeacher)` and `editor.Use(authoring)` pattern). This task's "What
  to build" section below states which role(s) gate each endpoint — use
  that, plus `auth.RoleOwner`/`auth.RoleTeacher` constants from
  `internal/auth/permissions.go`, exactly the way `server.go` already does
  for Task 4/5 routes. Do not invent a new gating mechanism.
- **Two-tier HTML/JSON pattern, established by `learner_ui.go`**: this
  codebase's dual approach is NOT one handler branching on `Accept`
  header — it is two separate handler functions per concept, an
  `..._ui.go` HTML-page handler (calls `templates.X.Execute(c.Writer,
  gin.H{...})`, sets `Content-Type: text/html; charset=utf-8`) that either
  renders server-side state directly or embeds small inline `fetch()`
  calls to a sibling JSON API handler for mutations. Follow this exactly:
  the checkout page is an HTML-page handler
  (`internal/httpserver/handlers/commerce_checkout.go` or
  `commerce.go`, doc-commented like `CourseLearnPage`/`LessonPlayerPage`)
  that renders a `templates.Checkout` template (add this template
  alongside the existing ones in `internal/httpserver/templates`, following
  that package's existing template-registration pattern — read
  `internal/httpserver/templates` briefly to match its structure) embedding
  Razorpay's `checkout.js` `<script>` tag and a small inline script that:
  (1) `fetch()`s `POST .../checkout/order` to get `order_id`/`amount`/
  `currency`/`key_id`, (2) opens Razorpay's checkout widget with those
  values, (3) on the widget's client-side "success" callback, does nothing
  more than a client-side navigation to the order-status polling page
  (task-10 builds that page's template; this task only needs the
  create-order and order-status JSON endpoints to exist so task-10 can call
  them) — the widget callback is UI convenience only and must never be
  treated as a trust signal.
- **Audit-log pattern** (`internal/models/audit.go`'s `AuditRepo.Record`,
  used exactly as in `orgs.go`'s `UpdateOrg`/`DeleteOrg`/`CreateOrg`): call
  `d.Audit.Record(ctx, tx, models.AuditEvent{OrgID: &oc.OrgID, UserID:
  &ac.UserID, Action: "<resource>.<verb>", ResourceType: "<resource>",
  ResourceID: &id, IPAddress: c.ClientIP(), UserAgent:
  c.Request.UserAgent()})` inside the same transaction as the mutation
  (so a failure to audit rolls back the mutation — "the action happened
  but wasn't recorded" is worse than "the action didn't happen", per that
  file's doc comment). Every mutating endpoint in this task must call it:
  offer create/update/archive, discount create/update/deactivate, invite
  token create, refund initiate, manual entitlement grant. Use action
  strings from Task 3's `commerceDomainActions`/
  `ownerOnlyCommerceDomainActions` naming convention (`"offer.create"`,
  `"offer.update"`, `"offer.archive"`, `"discount.create"`,
  `"discount.update"`, `"discount.archive"`, `"invitetoken.create"`,
  `"entitlement.grant"`, `"refund.initiate"`) as the `Action` value so the
  audit trail lines up with the permission matrix's vocabulary.
- **Route grouping**: expect to be mounted under the existing
  `/api/orgs/:org_slug/...` `ResolveOrg`-gated group (owner/teacher-only
  commerce-admin endpoints: offers, discounts, invite tokens, refunds,
  manual grants, revenue report) and under the existing course-scoped
  `ResolveCourseOrg` group (learner-facing checkout page/order
  endpoints, since checkout targets a specific course's offer) — mirror
  `server.go`'s existing groupings exactly (see the `org :=
  authed.Group("/orgs/:org_slug")` and the course-domain group around line
  197-203). Do not register routes yourself in this task; just write
  handler doc comments stating the expected path/method/middleware chain
  so task-10 (routes-wiring) can wire them mechanically, exactly as every
  existing handler already documents (e.g. `UpdateOrg`'s comment: "Requires
  RequireRole(owner) on top of ResolveOrg").

## What to build — endpoints

Money amounts in requests/responses, in the DB (via the `models` repos from
task-4), and in all handler-side arithmetic are `float64`, matching Task 1's
`NUMERIC(12,2)` columns and task-4's `models` structs — do NOT use integer
minor units (paise/cents) anywhere in this task's own code or wire format.
The ONE exception is the two calls this task makes into
`internal/payments.Provider` (`CreateOrder`/`CreateRefund`, task-5): those
methods take `amountMinorUnits int64` because Razorpay's own API requires
integer minor units at that specific boundary. Convert
`float64` → minor units only at the call site, immediately before calling
`Provider.CreateOrder`/`CreateRefund` (e.g. `int64(math.Round(total * 100))`
for a 2-decimal currency), and never let a minor-units value leak back out
of that conversion into anything persisted or returned by this task's own
handlers.

### Offer management (`RequireRole(auth.RoleOwner, auth.RoleTeacher)`)

- `POST .../courses/:courseId/offers` — create an offer. Body varies by
  `type` (`free`, `paid`, `subscription`, `cohort`, `invitation_only`):
  common fields (price, currency, tax_rate_percent — required for all
  non-free types); `subscription` additionally requires
  `access_duration_days`; `cohort` additionally requires
  `enrollment_starts_at`, `enrollment_ends_at`, and optional
  `max_seats`; `invitation_only` requires no extra pricing beyond
  price/currency (access is gated by invite token, not open checkout).
  Validate the type-specific required fields server-side; reject with 400
  and a clear message if a type's required field is missing, and reject if
  extraneous fields for a different offer type are present with non-zero
  values (prevents a client from smuggling e.g. `max_seats` onto a `paid`
  offer that then gets misread later).
- `PATCH .../courses/:courseId/offers/:offerId` — update an offer's mutable
  fields (price, tax rate, availability window, seat cap, etc. — not
  `type`, which is immutable after creation to avoid ambiguity in
  historical orders that reference this offer).
- `POST .../courses/:courseId/offers/:offerId/archive` — archive (soft
  delete) an offer; archived offers are excluded from
  `GET .../courses/:courseId/offers` by default and reject new checkouts,
  but historical orders/entitlements referencing them are untouched.
- `GET .../courses/:courseId/offers` — list offers for a course (JSON).

### Discount codes (`RequireRole(auth.RoleOwner, auth.RoleTeacher)`)

- `POST .../offers/:offerId/discounts` — create a discount code scoped to
  one offer: `code` (unique per offer), `discount_type`
  (`percentage`|`fixed_amount`), `discount_value`, optional
  `expires_at`, optional `max_redemptions`.
- `GET .../offers/:offerId/discounts` — list discount codes for an offer.
- `POST .../offers/:offerId/discounts/:discountId/deactivate` — deactivate
  a code (stops future redemptions; does not affect orders that already
  redeemed it).

### Invite tokens (`RequireRole(auth.RoleOwner, auth.RoleTeacher)`, invitation_only offers only)

- `POST .../offers/:offerId/invite-tokens` — generate a single-use token;
  body optionally includes `email` to bind the token to a specific
  invitee (checkout then requires the authenticated learner's email to
  match, if bound). Reject with 400 if the offer's `type` is not
  `invitation_only`.
- `GET .../offers/:offerId/invite-tokens` — list outstanding (unused,
  unexpired) tokens for the offer, plus their status (used/expired) for
  already-issued ones — do not return the raw token value again once
  issued (return only a truncated/id reference), matching a "shown once at
  creation" convention if the underlying repo's `Create` returns the plain
  token but list/read endpoints don't.

### Checkout page (`GET .../courses/:courseId/offers/:offerId/checkout`, HTML+HTMX, `ResolveCourseOrg` — any authenticated org member, not staff-gated)

- Loads the offer; 404s if archived or not found.
- Computes a price/discount/tax preview server-side (subtotal from
  `offer.price`, discount preview if a `?discount_code=` query param is
  present — validated but the actual redemption/increment only happens at
  order-creation time to avoid a race between preview and purchase, tax
  from `offer.tax_rate_percent`) purely for display; the real numbers are
  recomputed independently (and are the only numbers that matter) by the
  create-order endpoint below.
- For `invitation_only` offers: requires a valid, unused, unexpired invite
  token supplied via `?invite_token=` query string (or session, if this
  codebase has session storage beyond the JWT — check
  `internal/httpserver/middleware/auth.go` before assuming; if there is no
  session store, query string is the only option and the checkout page
  must carry the token forward into the create-order POST body). Responds
  403 with a clear message if the token is missing, already used, expired,
  or (when bound) doesn't match the caller's email.
- For `cohort` offers: checks `now` against
  `enrollment_starts_at`/`enrollment_ends_at`; checks current active
  entitlement/order count against `max_seats` if set. Responds with a
  clear "enrollment window closed" or "course is full" message (still
  HTTP 200 rendering the checkout page with an error banner and no
  checkout.js widget, matching how a server-rendered page communicates a
  blocked state — not a raw 403/409 the learner can't see explained,
  unless the codebase's HTML-page error convention elsewhere is to
  `c.String(http.StatusXxx, ...)`; check `learner_ui.go`'s existing
  `c.String(http.StatusInternalServerError, "internal error")` precedent
  and stay consistent with whatever that convention actually is).
- Renders `templates.Checkout` with: offer summary, computed preview
  (subtotal/discount/tax/total), the Razorpay `checkout.js` script tag,
  and `d.Config.Razorpay.KeyID` (the **public** key id only — never
  `KeySecret`) passed into the template for the inline script to use when
  it later calls the create-order JSON endpoint and opens the widget.

### Create order (`POST .../courses/:courseId/offers/:offerId/checkout/order`, JSON, `ResolveCourseOrg`)

Server-side, in order:

1. Re-validate everything the checkout page validated (offer not archived,
   invite token for invitation_only, cohort window/seats) — never trust
   that the client only reached this endpoint via the checkout page.
2. Ignore/reject any client-supplied price, currency, subtotal, discount
   amount, tax amount, or total fields in the request body — the only
   client input this endpoint accepts is `discount_code` (a string to look
   up server-side) and, for invitation_only offers, `invite_token`. If the
   request body contains any of the forbidden money/currency fields with a
   non-empty/non-zero value, reject with 400 (defense-in-depth against a
   tampered request even though the server ignores these fields either
   way) — state this explicitly in a code comment.
3. Compute `subtotal = offer.price`.
4. If `discount_code` present: look it up scoped to this offer, check
   `active`, not expired, under `max_redemptions` — apply at most this one
   code (this codebase's decision, per `plans/task-6-implementation/main-plan.md`,
   is one discount per order, no stacking); if invalid/expired/exhausted,
   reject 400 with a clear message rather than silently ignoring it.
   Recompute the discount amount server-side from `discount_type`/
   `discount_value` — never accept a discount amount from the client.
5. Compute `tax = round((subtotal - discount) * offer.tax_rate_percent / 100)`
   using integer minor-unit arithmetic throughout (no floats).
6. Compute `total = subtotal - discount + tax`.
7. Fetch the current platform commission rate (from whatever
   `platform_settings` repo Task 4 (models-repositories) built) and
   snapshot the resulting commission amount onto the order at this
   moment — later commission-rate changes must never retroactively alter
   an already-created order.
8. **Free offer path**: if `offer.type == "free"` (or `total == 0` for a
   paid offer fully covered by a discount — decide and document which; the
   simplest documented reading is "offer.type == free skips Razorpay
   entirely; a paid offer that discounts to zero still goes through the
   normal paid flow and order state machine, just with a zero-amount
   Razorpay order" — state this choice as an assumption if
   Task 4/5 (models-repositories) didn't already decide it), skip Razorpay
   entirely: persist an `orders` row with `status` reflecting immediate
   completion (whatever the Task 4 models-repositories order-state-machine
   calls a terminal non-payment success state — do not invent a new status
   string; use what that task defined, noting it as an assumption if
   unclear), create an `active` entitlement (no `order_id` payment
   linkage needed since no money moved, or linked to this zero-payment
   order per that task's schema — again, follow Task 4/5's actual schema,
   noting an assumption if ambiguous) and a `learner_course_access` row
   (mirroring `EnrollCourse`'s `d.LearnerCourseAccess.Create(...)` call in
   `learner.go`), all server-side with no payment provider involved, and
   return a response shape the client can detect as "no payment needed,
   redirect straight into the course" (e.g. `{"free": true, "redirect_url":
   "/courses/:courseId/learn"}`) instead of Razorpay order fields.
9. **Paid path**: persist an `orders` row with status `pending`, call
   `d.Payments.CreateOrder(ctx, ...)` (the Task 5 razorpay-client
   `Provider` interface — use it exactly as designed; if it lacks a needed
   parameter such as receipt/notes, note the gap as an assumption rather
   than modifying that interface here) with the final integer minor-unit
   `total` and `currency`, attach the returned Razorpay order id to the
   `orders` row and transition its status to `payment_initiated`, and
   return `{"order_id": "<razorpay order id>", "amount": total,
   "currency": ..., "key_id": d.Config.Razorpay.KeyID}` — exactly the
   shape Razorpay's `checkout.js` widget needs, and nothing else. **Never**
   include `KeySecret` or `WebhookSecret` in this or any other response
   body from this file.
10. This endpoint must NEVER create or transition an entitlement to
    `active` as a result of the paid path — only the free-offer path
    (step 8) and the manual-grant endpoint below may do that in this
    file. All paid-order entitlement creation happens exclusively in the
    webhook-driven worker job (task-7/task-8), even if a client later
    calls this endpoint again or reports "payment succeeded" through some
    other channel.

### Order status (`GET .../orders/:orderId/status`, JSON, polled by task-10's HTMX page)

- Read-only: returns the order's current `status` and, if an entitlement
  now exists linked to this order (i.e. the webhook-driven worker job has
  already run), a `redirect_url` into the course. Must not write anything,
  must not itself check with Razorpay, must not grant anything — it only
  reads state the webhook path already persisted. Document this
  explicitly in the handler's doc comment, since it's the one endpoint in
  this file most likely to be misused as a shortcut around the webhook
  requirement.

### Refunds (`RequireRole(auth.RoleOwner)` only)

- `POST .../orders/:orderId/refund` — looks up the order's associated
  successful payment, calls `d.Payments.CreateRefund(ctx, ...)` (Task 5's
  `Provider` interface — again, use as designed; note any gap as an
  assumption), persists a `refunds` row with `status = "pending"` (NOT
  `"succeeded"` — this endpoint only *initiates* a refund; actual
  success/failure is written later by the refund webhook, task-7/task-8),
  and writes a `payment_audit_trail` row (`event_type` something like
  `"refund_initiated"`, `order_id`, the payment id, `old_state`/
  `new_state`, a `reason` if supplied in the request body, `user_id` =
  caller). Also call `d.Audit.Record(...)` with `Action:
  "refund.initiate"` per Task 3's action vocabulary. Reject 409 if the
  order has no successful payment to refund, or already has a pending/
  succeeded refund.

### Manual entitlement grant (`RequireRole(auth.RoleOwner, auth.RoleTeacher)`, reason required)

- `POST .../courses/:courseId/grant-access` — body requires `learner_id`
  (or email — pick one, document the choice) and a non-empty `reason`
  string (400 if missing/blank). Creates an `active` entitlement with
  `granted_by = caller`, `grant_reason = reason`, and no `order_id`;
  creates/updates the corresponding `learner_course_access` row
  (mirroring `EnrollCourse`'s pattern, but bypassing the
  published-course-only and prerequisite checks that self-service
  enrollment applies — an explicit admin grant is allowed to override
  both, since it's a deliberate staff action). Writes both to
  `audit_events` (via `d.Audit.Record(...)`, `Action: "entitlement.grant"`,
  `Details: {"reason": reason, "learner_id": ...}`) and to
  `payment_audit_trail` (`event_type: "entitlement_granted"`, no
  order/payment id, `reason`, `user_id` = caller) — this is the one
  non-payment event that still gets a payment-audit-trail row, since it's
  a money-adjacent access grant even though no money moved.

### Revenue reporting (`RequireRole(auth.RoleOwner)` only)

- `GET .../reports/revenue` — query params: `from`/`to` (date range,
  optional — default to some sane window if unset, e.g. last 90 days, and
  document the default), `offer_type` (optional filter), `course_id`
  (optional filter). Returns orders/revenue aggregated per course and per
  offer, **net of platform commission**, **segmented by currency** (a
  response shape like `{"INR": {...}, "USD": {...}}` or a list of rows
  each carrying its own `currency` field — pick one and document it; do
  not sum INR and USD figures together under any circumstance). Only
  counts orders whose status reflects verified payment success (whatever
  Task 4/5's order-state-machine calls that state), and correctly nets out
  orders with a `succeeded` refund against the same course/offer. Scoped
  by `ResolveOrg`'s RLS org context — an owner only ever sees their own
  org's orders, enforced independently by both `RequireRole(auth.RoleOwner)`
  and Postgres RLS on the `orders` table (defense-in-depth, matching this
  codebase's stated philosophy everywhere else).

## Hard security rules (non-negotiable — restate literally in code comments at the relevant call sites)

- No endpoint in this file may ever create or update an `entitlements` row
  to `active` status as a result of a payment. Only two paths in this file
  may create an active entitlement directly: (1) the free-offer path
  inside create-order, because no money moved and there is nothing for a
  webhook to confirm, and (2) the manual entitlement grant endpoint, which
  is an explicit, reason-required, audit-logged staff action. All PAID
  entitlement creation happens exclusively in the webhook-driven worker
  job (task-7/task-8) — never in these handlers, even if a client sends a
  request that claims "payment succeeded."
- The create-order handler must recompute EVERY money value server-side
  (price, discount, tax, commission) from the offer/discount-code/
  platform_settings rows. It must reject or silently ignore any
  client-supplied amount/currency/subtotal/discount/tax/total field in the
  request body — client input to this endpoint is limited to
  `discount_code` and (for invitation_only) `invite_token`.
- Razorpay's `key_secret` (`d.Config.Razorpay.KeySecret`) and the webhook
  secret (`d.Config.Razorpay.WebhookSecret`) must never appear in any
  response body, template, log line, or error message produced by any
  handler in this file — only `d.Config.Razorpay.KeyID` (the public key)
  may ever reach the browser, exactly like `checkout.js`'s own
  documented usage.

## Acceptance criteria

- [ ] `internal/httpserver/handlers/commerce.go` (or the
      `commerce_offers.go` / `commerce_checkout.go` / `commerce_refunds.go`
      / `commerce_reports.go` split) implements every endpoint listed above
      as `func(d *AuthDeps) gin.HandlerFunc` constructors, each with a doc
      comment stating its intended path, HTTP method, and required
      middleware chain (mirroring `orgs.go`/`learner.go`'s existing doc
      comment convention), so task-10 can wire routes mechanically.
- [ ] `AuthDeps` (`internal/httpserver/handlers/deps.go`) gains a `// Task
      6: commerce` field block with the repos/provider this task depends
      on (offers, discount codes, invite tokens, orders, entitlements,
      refunds, payment audit trail, and the Razorpay `Provider`).
- [ ] Offer create/update/archive/list endpoints support all 5 offer types
      with their type-specific required fields, gated by
      `RequireRole(auth.RoleOwner, auth.RoleTeacher)`.
- [ ] Discount code create/list/deactivate endpoints are scoped to a
      specific offer, gated by `RequireRole(auth.RoleOwner,
      auth.RoleTeacher)`.
- [ ] Invite token create/list endpoints work only for `invitation_only`
      offers, support optional email binding, and never re-expose an
      already-issued token's raw value.
- [ ] The checkout page (`GET`) is a server-rendered HTML+HTMX handler
      (not a JSON endpoint) that computes a price/discount/tax preview
      server-side, embeds Razorpay's `checkout.js` using only
      `d.Config.Razorpay.KeyID`, enforces the invite-token check for
      invitation_only offers (403 if missing/used/expired/mismatched), and
      enforces the enrollment-window/seat-cap check for cohort offers with
      a clear error state.
- [ ] The create-order endpoint (`POST`) recomputes subtotal, discount,
      tax, and commission entirely server-side; ignores/rejects any
      client-supplied money or currency fields; for `free` offers, skips
      Razorpay and directly creates an active entitlement +
      `learner_course_access` row; for paid offers, calls the Razorpay
      `Provider.CreateOrder` with the final integer minor-unit amount,
      persists the `orders` row through `pending` → `payment_initiated`,
      and returns only `order_id`/`amount`/`currency`/`key_id` (no secret
      ever included).
- [ ] The create-order endpoint never creates or activates an entitlement
      on the paid path under any circumstance — verified by reading the
      code, not just by test coverage (this is the task's central
      invariant).
- [ ] The order-status endpoint (`GET`, JSON) is read-only: it never
      writes to `orders`, `entitlements`, or `learner_course_access`; it
      only reports state already written by the webhook-driven worker
      job.
- [ ] The refund endpoint (`POST`, owner-only) looks up the order's
      payment, calls `Provider.CreateRefund`, persists a `refunds` row
      with `status = "pending"` (never `"succeeded"` directly), and writes
      both an `audit_events` row (`Action: "refund.initiate"`) and a
      `payment_audit_trail` row.
- [ ] The manual entitlement grant endpoint (`POST`, owner/teacher) requires
      a non-empty `reason`, creates an `active` entitlement with
      `granted_by`/`grant_reason` set and no `order_id`, updates
      `learner_course_access`, and writes both an `audit_events` row
      (`Action: "entitlement.grant"`) and a `payment_audit_trail` row.
- [ ] The revenue reporting endpoint (`GET`, owner-only) supports date-range
      and offer-type filters, returns net-of-commission amounts, segments
      results by currency (never sums INR and USD together), and only
      counts orders in a verified-payment-success state, netting out
      succeeded refunds.
- [ ] No handler in this file ever includes `d.Config.Razorpay.KeySecret`
      or `d.Config.Razorpay.WebhookSecret` in a response, template, or log
      line — grep-confirmable.
- [ ] Every mutating endpoint in this file (offer create/update/archive,
      discount create/update/deactivate, invite token create, refund
      initiate, manual grant) calls `d.Audit.Record(...)` inside the same
      request transaction as the mutation, using action strings from Task
      3's `commerceDomainActions`/`ownerOnlyCommerceDomainActions`
      vocabulary.
- [ ] Any repo/interface method assumed by this task but not actually
      defined by Task 4 (models-repositories) or Task 5 (razorpay-client)
      is called out with an explicit comment at the call site rather than
      silently worked around.
- [ ] `go build ./...` passes (handlers compile against whatever
      Task 4/5 actually shipped — adjust field/method names to match their
      real signatures rather than this task file's illustrative names if
      they differ).

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
