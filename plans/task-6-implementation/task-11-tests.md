---
task: 11
name: tests
parallel_group: 5
depends_on: [10]
issue: TBD
---

# Task 11: tests

## What to build

This task assumes ALL prior Task 6 subtasks (1 through 10 — schema/migrations,
permissions matrix, offer/checkout/order models, the Razorpay adapter, webhook
processing and idempotency, payment state machines and entitlements, revenue
reporting, the admin dashboard, and route wiring) are already implemented and
merged. Nothing here adds new product behavior; it adds the mandatory
automated test coverage that `plans/lms-mvp/task-6-commerce.md`'s "Required
automated tests" section and acceptance criteria call for, which is not
optional or deferrable to a later hardening task.

Write these tests against a **real local Supabase Postgres**, following the
exact convention already established in `internal/worker/notifications_test.go`
and `internal/models/rls_isolation_test.go`:

- Every test that touches the database calls `testutil.RequireDB(t)` /
  `testutil.DB(t)` first, so it is automatically skipped (not failed) when
  `LMS_TEST_DATABASE_URL` is unset, and runs for real in CI where that
  variable is set.
- `testutil.DB(t)` returns a pool connected as the non-superuser,
  non-BYPASSRLS `app_test` role — use it for anything that must go through
  RLS. `testutil.AdminDB(t)` returns a superuser/owner pool — use it only for
  seeding fixtures that must bypass RLS (e.g. inserting two organizations'
  worth of data before proving isolation), exactly as `rls_isolation_test.go`
  does with its `seedUser` / `seedOrgWithOwner` helpers.
- To run a query under a specific organization/role RLS context, use
  `dbctx.Begin(ctx, pool, userID, orgID, role)` (see
  `internal/dbctx/dbctx.go`) which issues `set_config('app.current_org_id',
  ...)` etc. on the transaction — this is the "SET LOCAL app.current_org_id"
  mechanism referenced below; do not hand-roll a different way of setting
  session context.
- Call handler functions and worker task-handler functions **directly**
  (e.g. `handleRazorpayWebhook(pool, ...)(ctx, task)` or the Gin handler
  function invoked with a constructed `*http.Request`/`httptest.ResponseRecorder`
  and a real DB pool), bypassing the full Gin router wiring and a real asynq
  server/Redis where practical — matching how
  `TestNotificationHandlers_RespectOptOut` in
  `internal/worker/notifications_test.go` calls `handleNotifyAssignmentGraded`
  directly rather than running a live asynq consumer. If Task 6's webhook
  ingestion is implemented as an HTTP endpoint (`POST /webhooks/razorpay`)
  that itself does synchronous signature verification before enqueueing a job,
  test that HTTP handler directly with `httptest`; then test the queued job's
  handler function directly for the idempotency/state-transition assertions.
- Assert on DB state afterward via direct SQL queries (row counts, column
  values), not via mocked return values — this is what makes these
  integration tests meaningful given no code exists in this repo without a
  real migrated schema to test against.
- Look up the actual package/type/function names introduced by Tasks 1-10
  (e.g. the webhook events repo, the entitlements repo, the checkout/webhook
  handler functions, the free-offer checkout path, the invitation-token
  model, the cohort seat-count column) by reading the merged code under
  `internal/models/`, `internal/httpserver/handlers/`, and `internal/worker/`
  before writing these tests — this spec describes required behavior and
  assertions, not literal signatures, since those are fixed by whichever
  earlier task actually implemented them. Where this spec names a
  type/table/column that differs slightly from what Tasks 1-10 actually
  produced (e.g. `WebhookEventRepo.TryRecord` vs. some other repo/method
  name that does the same ON CONFLICT DO NOTHING dedup), use the real name
  and preserve the described behavior under test.

Add new test files under `internal/httpserver/` (for HTTP-endpoint-level
tests: webhook signature verification, secrets-never-leaked, free-offer
checkout, cohort seat cap, invitation-only gating) and/or `internal/worker/`
(for webhook-job-handler-level tests: idempotency, payment state
transitions) and/or `internal/models/` (for the RLS isolation test and the
commission-snapshot-immutability test), matching where equivalent Task 4/5
tests already live for each concern (HTTP-endpoint behavior in
`internal/httpserver/*_test.go`, RLS in `internal/models/rls_isolation_test.go`
or a new `rls_isolation_commerce_test.go` alongside it following the same
naming pattern as `rls_isolation_learner_test.go`).

Write the following mandatory tests. Each corresponds 1:1 to an item in
`plans/lms-mvp/task-6-commerce.md`'s "Required automated tests" list and/or
its acceptance criteria — none of these are optional or may be skipped/left
as TODOs.

### 1. Webhook signature verification

POST to the Razorpay webhook endpoint (e.g. `/webhooks/razorpay`) with a
plausible payment-success payload but an invalid or missing
`X-Razorpay-Signature` header.

- Assert the HTTP response is `401 Unauthorized` (or the status code Task 6
  actually chose for a rejected signature — confirm against the merged
  handler, but it must be a 4xx rejection, never 200).
- Assert, via direct DB query against `testutil.AdminDB(t)`, that no row was
  inserted into `webhook_events`, `orders`, `payments`, or `entitlements` as
  a side effect of the rejected request — an invalid signature must short
  circuit before any write.
- Include a companion case with a syntactically well-formed but
  cryptographically wrong signature (not just a missing header) to prove the
  verification is a real HMAC/signature check, not just a presence check.

### 2. Duplicate webhook idempotency

Using a valid signature (computed the same way the real Razorpay client
would, with the test webhook secret configured via `LMS_RAZORPAY_WEBHOOK_SECRET`
or equivalent test config), deliver the same webhook event body twice — same
derived dedup event ID both times (Razorpay's `event_id`/`x-razorpay-event-id`
or whatever field Task 6's implementation dedups on).

- After both deliveries, assert exactly ONE `entitlements` row exists for the
  learner/course/offer, not two.
- Assert exactly one revenue-affecting state change occurred on the
  `orders`/`payments` row (e.g. `payments.status` transitioned to
  `succeeded` once, `orders.captured_amount`/equivalent was set once, not
  double-applied).
- Write a focused unit-level test directly against the webhook-events repo's
  dedup method (e.g. `WebhookEventRepo.TryRecord(ctx, eventID, ...)` or
  whatever the actual method is named) asserting: first call returns
  "recorded" (a new row inserted), second call with the same event ID
  returns "already recorded" (ON CONFLICT DO NOTHING — ON CONFLICT (event_id)
  DO NOTHING or similar — ⁠hit, no error, no duplicate row), confirmed by a
  `SELECT count(*) FROM webhook_events WHERE event_id = $1` equal to 1 after
  both calls.
- Separately assert the worker/job handler itself has an early-return guard:
  if `TryRecord` (or equivalent) reports "already processed", the handler
  must return success without re-running entitlement-granting/order-state
  logic — verify this by asserting no audit_events row (or no second one) is
  written for the second delivery, in addition to the single-entitlement
  assertion above.

### 3. No access without verified webhook (most safety-critical test in this task)

Simulate ONLY the browser-side checkout-return/callback path — whatever
endpoint or page Task 6 built to receive the Razorpay `checkout.js`
client-side "payment succeeded" callback/redirect (e.g. a `GET`/`POST`
`/checkout/:offer_id/return` or `/checkout/callback` route) — invoked with a
plausible-looking success payload/query params, but WITHOUT ever delivering
the corresponding webhook event.

- Assert no `entitlements` row was created for that learner/offer/course.
- Assert `learner_course_access` (or whatever view/table/query Task 5's
  learner-access check reads from) still reports no active access for that
  learner on that course — query it the same way the learner course-player
  gating logic would (reuse Task 5's access-check function/query if one
  exists, e.g. by calling the learner course-access handler directly and
  asserting it denies/redirects, in addition to the raw DB assertion).
- If the return/callback endpoint itself renders any "success" UI or returns
  a 200, explicitly assert that response does NOT imply access was granted
  (e.g. it must not set any session/cookie/flag that the player trusts) —
  the point of this test is that this endpoint is provably a no-op for
  access purposes.
- This test must fail loudly (not skip) if run without `LMS_TEST_DATABASE_URL`
  is unset only in the sense of being skipped like every other DB test —
  but it must never be marked `t.Skip()`'d for any other reason, and should
  be given a name that makes its importance obvious, e.g.
  `TestCheckoutReturnCallback_NeverGrantsAccessWithoutWebhook`.

### 4. Payment state transitions (success then refund; success then chargeback)

Two sub-tests (or one table-driven test with two cases), each starting from
a seeded successful-payment webhook delivery (reuse the valid-signature
helper from test 2):

**Refund case:**
- Process a successful payment webhook first → assert an `entitlements` row
  exists with active/granted status.
- Then process a refund-success webhook for the same payment (same
  order/payment identifiers, valid signature, distinct event ID from the
  original).
- Assert the entitlement's status flips to `revoked` (or the exact enum
  value Task 6 used — confirm against the merged schema/model).
- Assert `learner_course_access` (or equivalent) reflects the learner no
  longer having active access.
- Assert the order's recorded net revenue (and/or `payments`' captured
  amount) is adjusted down by the refunded amount — not merely left as the
  original succeeded amount.
- Assert the platform commission portion associated with that order is also
  reduced proportionally (per the commerce plan's "Refunds and chargebacks
  reduce both the organization's net revenue and the platform's commission
  proportionally" requirement).

**Chargeback case:** same shape, using a chargeback/dispute-lost webhook
event type instead of a refund event, asserting the same entitlement-revoked
/ access-revoked / revenue-and-commission-reduced outcomes.

### 5. Payment secrets security

Assert `LMS_RAZORPAY_KEY_SECRET` (or whatever env var name Task 2/6 actually
used for the Razorpay API secret) and the webhook signing secret's literal
value never appear as a substring in:
- The JSON/HTML response body of the checkout-initiation endpoint (the one
  that returns whatever `checkout.js` needs client-side — this SHOULD
  contain the public `key_id` but must NOT contain the secret).
- The response body of any order-status/order-detail endpoint a learner or
  org owner can hit.
- The response body of the webhook endpoint itself (both success and
  rejected-signature responses).

Implement as one or two checkout-flow tests that, after making the relevant
request(s) with `httptest`, run a plain `strings.Contains(body, secretValue)`
assertion (`require.NotContains`) against each response body, using the
actual secret value the test config/env was set to (so the assertion is
non-trivial — set a recognizable, non-guessable test secret value via env
before the test, not the literal empty string).

### 6. RLS isolation (one combined test, matching Task 4/5's precedent)

Add one test — e.g. `TestRLS_CommerceIsolation` in `internal/models/`,
following the exact structure of `TestRLS_CourseDomainIsolation` in
`internal/models/rls_isolation_test.go` (which spans
courses/chapters/lessons/blocks/assets in a single test rather than one test
per table) — spanning `offers`, `orders`, `payments`, and `entitlements`:

- Using `testutil.AdminDB(t)`, seed two organizations (org A, org B) each
  with an owner user, and seed org B with one row in each of `offers`,
  `orders`, `payments`, `entitlements` (entitlements tied to some learner
  user in org B).
- Open a `dbctx.Begin(ctx, pool, userA, orgA, "owner")` transaction for org
  A (this is the literal `SET LOCAL`-equivalent `app.current_org_id`
  session-context mechanism the task description refers to — use it, don't
  reinvent one).
- For each of the four tables, directly query
  `SELECT count(*) FROM <table> WHERE id = $1` for org B's seeded row and
  assert the count is 0 — proving the RLS policy itself hides the row (not
  just that some handler layer happens to filter it out).
- Also assert cross-org `UPDATE` and `DELETE` against org B's rows affect
  zero rows under org A's session context, mirroring
  `TestRLS_CourseDomainIsolation`'s pattern exactly.
- This test must run raw SQL against the tables directly (via `txA.Tx`), not
  through any repo/handler wrapper, so it is testing the Postgres RLS policy
  itself.

### 7. Commission snapshot immutability

- Using `testutil.AdminDB(t)`, set `platform_settings`' commission percent
  (or whatever the actual column/table name is — confirm against Tasks
  1-10's migrations) to a known value, e.g. `10.00`.
- Create an order (via the real order-creation code path, not a raw insert,
  so the snapshot-computation logic under test actually runs) and assert its
  `commission_rate_snapshot` (or equivalent column name) equals `10.00`.
- Update `platform_settings`' commission percent to a different value, e.g.
  `15.00`.
- Re-fetch the already-created order and assert its
  `commission_rate_snapshot` is still `10.00` — unchanged by the later
  platform-settings update. This proves commission is snapshotted per-order
  at creation time, not computed live/joined at read time.

### 8. Free-offer enrollment bypasses payment entirely

- Create a `free`-type offer for a course.
- Drive the real checkout entrypoint for that offer as a learner (calling
  the handler function directly, or via `httptest` against the real route).
- Assert an `entitlements` row is created immediately, in `active`/granted
  status, with no corresponding row created in `orders` or `payments` (or, if
  Task 6's design does create a zero-amount order record for bookkeeping
  consistency, assert no Razorpay API/adapter call was attempted — check
  this via whatever fake/mock Razorpay adapter Tasks 1-10 established for
  testing, following the same fake-client convention as
  `notify/notifytest.FakeEmailClient`; if no such fake exists yet, that is a
  gap this task should flag, not silently work around).

### 9. Cohort seat cap

- Create a `cohort`-type offer with `max_seats` (or equivalent column) set
  to a small number, e.g. `1`.
- Successfully check out one learner into it (via the real checkout path;
  use the free-offer path or a stubbed/faked successful payment webhook,
  whichever is simpler and still exercises the seat-count check) so the
  cohort is now at capacity.
- Attempt a second learner's checkout into the same offer.
- Assert the second checkout is rejected server-side with an explicit
  error/4xx response from the checkout-initiation handler itself — not
  merely something the frontend UI happens to hide (i.e. call the handler
  directly, bypassing any client-side "sold out" button-disabling, and
  confirm the server independently enforces the cap).
- Assert no second `entitlements` row (and no second order, if applicable)
  was created for that offer.

### 10. Invitation-only gating

- Create an `invitation_only`-type offer.
- Attempt checkout for that offer with no invite token at all → assert
  server-side rejection (4xx from the handler) and no entitlement/order
  created.
- Attempt checkout with a garbage/nonexistent token → same assertions.
- Attempt checkout with a real, unexpired invite token generated via the
  real invite-token-creation path (Task 6's `invitetoken.create` permission
  path from `plans/task-6-implementation/task-3-permissions-matrix.md`) →
  assert it succeeds (entitlement created).
- Immediately reattempt checkout with the SAME token → assert it is now
  rejected (already-used), and no second entitlement/order was created.
- If Task 6's invite tokens support expiry, add a case with a token whose
  expiry is seeded in the past → assert rejection.

## Running the tests

- Start a local Supabase Postgres (`supabase start`) and export
  `LMS_TEST_DATABASE_URL` pointing at it, per the convention already
  established for Task 4/5 (`internal/testutil/pgtest.go`'s doc comment and
  the CI workflow that sets this variable).
- Run the full suite with the race detector, matching Task 4/5's precedent:
  `go test ./... -race`.
- This task's Definition of Done also requires, as a final pass after all
  tests above are written and green:
  - `go build ./...` — clean build, no errors.
  - `go vet ./...` — clean, no warnings.
  - `go test ./... -race` — full suite passes, including every test added by
    this task and every test from Tasks 1-10.
  - A focused code-review pass (self-review or via the repo's `code-review`
    skill/process) specifically checking:
    - Every new commerce table (`offers`, `orders`, `payments`,
      `entitlements`, `webhook_events`, discount/coupon tables, invitation
      tokens, `platform_settings` if org-scoped in any way) has RLS enabled
      and a policy that was actually exercised by test 6 above.
    - No code path creates an `entitlements` row outside of: (a) the
      verified-webhook worker handler, (b) the free-offer checkout path, or
      (c) an explicit, audit-logged admin-grant path. Grep the codebase for
      every `INSERT INTO entitlements` (or equivalent repo method call) and
      confirm each call site is one of these three.
    - No secret value (`LMS_RAZORPAY_KEY_SECRET`, webhook secret) is ever
      logged in plaintext or serialized into any HTTP response — grep for
      the config field name across `internal/httpserver/handlers/` and
      `internal/logging/` usage sites in the commerce code.
    - Webhook idempotency: confirm the ON CONFLICT DO NOTHING dedup and the
      handler's early-return guard are both present (test 2 exercises both,
      but the review should confirm the code, not just the test, does this).
    - Any sweep/cron/reconciliation job Task 6 added (e.g. abandoned-order
      cleanup, expired-invite sweep) is correct and idempotent if run twice
      in a row.

## Acceptance criteria

- [ ] A webhook-signature-verification test exists asserting invalid/missing
      `X-Razorpay-Signature` yields a 4xx response and creates zero rows in
      `webhook_events`, `orders`, `payments`, and `entitlements`.
- [ ] A duplicate-webhook-idempotency test exists proving exactly one
      `entitlements` row and one revenue-affecting state change result from
      two deliveries of the same event ID, including a focused test of the
      webhook-events repo's ON CONFLICT DO NOTHING dedup method and the
      worker handler's early-return-if-already-processed guard.
- [ ] A test exists proving the browser checkout-return/callback path alone
      (no webhook delivered) creates no `entitlements` row and leaves
      `learner_course_access` (or equivalent) showing no active access.
- [ ] A payment-state-transition test exists covering both refund-success
      and chargeback/dispute-lost webhooks following a prior successful
      payment, asserting the entitlement flips to revoked, access is
      revoked, and both order revenue and platform commission are reduced
      accordingly.
- [ ] A payment-secrets test exists asserting the Razorpay API secret and
      webhook secret values never appear as substrings in any response body
      from the checkout-initiation, order-status, or webhook endpoints.
- [ ] A single combined RLS-isolation test exists spanning `offers`,
      `orders`, `payments`, and `entitlements`, using a real
      `dbctx.Begin`/session-context transaction (not the handler layer) to
      prove org A's queries never see org B's rows and cross-org
      UPDATE/DELETE affect zero rows, following
      `TestRLS_CourseDomainIsolation`'s structure.
- [ ] A commission-snapshot-immutability test exists proving an order's
      `commission_rate_snapshot` (or equivalent) is unaffected by a later
      change to `platform_settings`' commission percent.
- [ ] A free-offer test exists proving checkout on a `free` offer creates an
      active entitlement immediately with no Razorpay order/payment/API
      interaction.
- [ ] A cohort-seat-cap test exists proving server-side rejection (not just
      UI hiding) of checkout once a cohort offer is at `max_seats`.
- [ ] An invitation-only test exists proving checkout is rejected without a
      valid unused unexpired invite token, that a valid token succeeds
      exactly once, and reuse of the same token is rejected.
- [ ] All new tests follow the established convention: skip cleanly when
      `LMS_TEST_DATABASE_URL` is unset (via `testutil.RequireDB`/`DB`), run
      against real Postgres in CI, use `testutil.AdminDB` only for fixture
      seeding, and call handler/worker functions directly rather than
      standing up a full router or live asynq/Redis server where practical.
  - [ ] `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass
      cleanly with a real `LMS_TEST_DATABASE_URL` configured.
- [ ] A final code-review pass has been done covering: RLS present and
      tested on every new commerce table, no entitlement created outside the
      three sanctioned paths (verified webhook / free-offer / audited admin
      grant), no secret leakage in responses or logs, webhook idempotency
      enforced in code (not only proven by test), and correctness/idempotency
      of any sweep/reconciliation job Task 6 introduced.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
