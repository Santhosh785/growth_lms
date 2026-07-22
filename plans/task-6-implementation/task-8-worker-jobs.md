---
task: 8
name: worker-jobs
parallel_group: 3
depends_on: [4, 5]
issue: TBD
---

# Task 8: worker-jobs

## What to build

Extend the existing `internal/worker` package (do not restructure it) with
three things: the Razorpay webhook-processing job, two periodic sweep jobs,
and the receipt-email job. This package already has two precedents to copy
exactly rather than reinvent:

- **Periodic sweep registration.** Task 4's `TypeSweepScheduledPublish` is
  NOT registered via asynq's built-in periodic-task scheduler
  (`asynq.PeriodicTaskConfigProvider`/`asynq.NewPeriodicTaskManager`). It is
  a plain `time.Ticker`-driven goroutine (`runPublishSweepLoop` in
  `internal/worker/publish.go`) started with `go runPublishSweepLoop(sweepCtx,
  pool, logger, publishSweepInterval)` inside `Run()` in `internal/worker/
  worker.go`, alongside (not through) the asynq mux/server. The two new
  sweep jobs in this task MUST follow that same ticker-goroutine shape, not
  asynq's periodic-task machinery — do not introduce a new registration
  mechanism into this package for this task.
- **Async email send via Resend.** Task 5's `internal/worker/
  notifications.go` (task type consts + payload structs + `Enqueue*`
  functions using `client.Enqueue(asynq.NewTask(taskType, data),
  asynq.Queue(QueueDefault))`) and `internal/worker/notifications_handlers.go`
  (the actual `mux.HandleFunc` handlers, which load a `*models.Profile` via
  `models.ProfileRepo.GetByID`, then call `notify.EmailClient.SendEmail(ctx,
  profile.Email, subject, body)` — the `notify.EmailClient` interface and
  its `notify.NewResendClient(cfg.Resend)` constructor are already wired
  into `Run()` in `worker.go` and passed to every notification handler
  today). The receipt-email job in this task reuses this exact
  `notify.EmailClient`/`notify.NewResendClient` plumbing — do not build a
  second email client or call Resend's API directly.

All new handlers run with the pool's own admin-level privileges, same trust
boundary as every existing worker task (see the comments in `worker.go`'s
`Run()` and `bunny_webhook.go`) — there is no per-request caller to scope
RLS session variables to for a background job.

This task assumes Task 4 (models-repositories: `OrderRepo`, `PaymentRepo`,
`RefundRepo`, `ChargebackRepo`, `EntitlementRepo`, `WebhookEventRepo`, an
`OfferRepo` with `access_duration_days`, and the `learner_course_access`
table's repo) and Task 5 (razorpay-client: the Razorpay provider adapter,
which is what parses/validates a webhook payload's event type and body
shape before the HTTP handler in Task 7 enqueues it) are both done. Exact
repo/method names, `payments`/`orders`/`entitlements`/`refunds`/
`chargebacks`/`webhook_events`/`payment_audit_trail`/`learner_course_access`
column names, and the `models.Querier`/`dbctx` transaction helper signatures
are whatever Task 4 actually produced — read those files first and match
them; the names below (e.g. `OrderRepo.ListPendingOlderThan`,
`EntitlementRepo.ListActiveExpired`) are the ones Task 4 was asked to
provide and should exist, but confirm signatures against the real code
before wiring calls.

### 1. Razorpay webhook processing job (`internal/worker/payments.go`)

A new task type, e.g. `TypeRazorpayWebhook = "payments:razorpay_webhook"`,
plus an `EnqueueRazorpayWebhook(client *asynq.Client, payload
RazorpayWebhookPayload) error` following the exact `enqueueNotification`-
style helper in `notifications.go` (marshal to JSON, `asynq.NewTask`,
`asynq.Queue(QueueDefault)` — use `QueueCritical` instead if Task 4/7
established payment webhooks as higher-priority than notifications; check
before assuming `QueueDefault`). The payload carries whatever Task 7's
webhook HTTP handler has already signature-verified and decided to hand
off: at minimum the Razorpay event type/id and the raw (or parsed) event
body needed to look up the matching `orders`/`payments`/`refunds` row.

**This handler is the ONLY place that is ever allowed to write a
`succeeded` payment or mutate an entitlement's status as a result of a
payment provider event.** The HTTP webhook handler (Task 7) does the HMAC
signature verification and nothing else — this worker task has no
HTTP-level trust decision to make, matching the Bunny-webhook precedent in
`bunny_webhook.go`.

**Idempotency (mandatory, tested by Task 11):** before doing anything else,
look up the `webhook_events` row by the Razorpay event ID. If
`processed_at` is already set, return `nil` immediately — do not re-apply
any state change, do not re-enqueue the receipt email, do not write a
second audit/payment-audit-trail row. If no `webhook_events` row exists yet
for this event ID, create one first (or Task 7's HTTP handler may have
already inserted it before enqueueing — confirm the exact division of
responsibility against Task 7's actual code and don't duplicate the
dedupe-insert). Only after successful processing, set
`webhook_events.processed_at = now()`.

Branch on the Razorpay event type carried in the payload:

- **`payment.captured`** (Razorpay's success event for a captured payment —
  confirm this exact string against Task 5's razorpay-client code/tests,
  since Razorpay's naming can vary slightly by API version):
  1. Resolve the matching `orders` row via the Razorpay order ID present in
     the payload (`OrderRepo` lookup by provider order ID).
  2. Create or update the `payments` row for this order to `status =
     'succeeded'`, recording the Razorpay payment ID and captured amount.
  3. Update `orders.status = 'succeeded'`.
  4. Create an `entitlements` row with `status = 'active'`. Set
     `expires_at` from `offers.access_duration_days` (load the offer the
     order references) when the offer is a fixed-term/subscription-style
     offer per Task 6's "subscription = fixed-term pass" decision
     (`plans/task-6-implementation/grilling-record.md` Q2); otherwise leave
     `expires_at` null (perpetual access).
  5. Update (or create, if none exists yet) the `learner_course_access` row
     linking the learner to the course, pointing at the new entitlement,
     with an active access status.
  6. Write one `payment_audit_trail` row (`created_at`, `event_type`,
     `order_id`, `payment_id`, `old_state`, `new_state`, `reason`,
     `user_id` — per the spec's exact field list in
     `plans/lms-mvp/task-6-commerce.md` "Payment audit trail" section) for
     this transition.
  7. Write one `audit_events` row via `models.AuditRepo.Record` (see
     `internal/models/audit.go`) — access-GRANTING actions must be
     audit-logged per the spec's explicit requirement in
     `task-6-commerce.md`. Use an `Action` like
     `"entitlement.granted"`/`"payment.succeeded"` (match whatever naming
     convention Task 4's models package already established for other
     `audit_events` actions, e.g. how Task 3 named auth-related actions —
     check `internal/models/audit.go`'s callers), `ResourceType =
     "entitlement"` (or `"order"`, whichever this codebase's convention
     favors — be consistent), `ResourceID` = the entitlement ID,
     `UserID` = the learner's profile ID, `OrgID` = the course's org ID.
  8. Enqueue the receipt email job (`EnqueueSendReceiptEmail`, see #3
     below) with the order's price/tax/discount/total breakdown.
  9. Set `webhook_events.processed_at = now()`.
  All of steps 1-9 (excluding the async enqueue side-effect of step 8,
  which is safe to happen after commit) should run inside one DB
  transaction so a failure partway through rolls back cleanly and the
  event is safely retried by asynq (it will retry because
  `processed_at` was never set).

- **`payment.failed`**: update the `payments` row (create if none exists)
  to `status = 'failed'` and `orders.status = 'failed'`; write a
  `payment_audit_trail` row; write an `audit_events` row is NOT required by
  the spec for a failure (only access-granting/revoking actions are
  spec-mandated for `audit_events` — a failed payment grants/revokes
  nothing) but writing one is not wrong either if it helps
  support/debugging — prefer keeping `audit_events` reserved for
  access-affecting actions and rely on `payment_audit_trail` for the
  failure trail, to match the spec's stated purpose split between the two
  tables. Mark `webhook_events.processed_at`.

- **`refund.processed`** (or whatever Task 5's client names Razorpay's
  refund-success webhook event — confirm exact string):
  1. Resolve the matching `refunds` row (created earlier when the in-app
     refund action, per `task-6-commerce.md`'s Q10 decision, called
     Razorpay's Refund API — that refund row should already exist in
     `pending` state; if it doesn't, this is likely a Razorpay-dashboard-
     initiated refund with no prior app-side row, so create one).
  2. Update `refunds.status = 'succeeded'`.
  3. Revoke the associated `entitlements` row: `status = 'revoked'`.
  4. Update the `learner_course_access` row's access status to reflect
     revoked access.
  5. Adjust the order's recorded net revenue for reporting purposes (the
     order/payment record that Task 4/6's revenue reporting reads from —
     reduce the org's net-of-commission revenue and the platform's
     commission proportionally, per `task-6-commerce.md`'s "Platform
     monetization" section: "Refunds and chargebacks reduce both the
     organization's net revenue and the platform's commission
     proportionally"). Exactly which column(s) this touches depends on how
     Task 4 modeled revenue aggregation (a running total column vs.
     compute-on-read from `payments`/`refunds` rows) — read Task 4's actual
     schema/repo before assuming a specific column exists; if revenue is
     computed on-read from payment/refund rows rather than stored as a
     running total, this step may reduce to "nothing extra to do beyond
     the `refunds` row update" — note in code comments which model applies.
  6. Write a `payment_audit_trail` row.
  7. Write an `audit_events` row — access-REVOKING actions must also be
     audit-logged per the spec, same as the granting path (`Action` like
     `"entitlement.revoked"`, `ResourceType = "entitlement"`).
  8. Mark `webhook_events.processed_at`.

- **`refund.failed`**: update `refunds.status = 'failed'`; write a
  `payment_audit_trail` row; mark processed. Do NOT touch the entitlement —
  a failed refund leaves the learner's access exactly as it was.

- **A chargeback/dispute event** (Razorpay's dispute webhook — likely named
  something like `payment.dispute.created` / `payment.dispute.won` /
  `payment.dispute.lost`, but **the implementer must verify the exact
  event names against live Razorpay documentation or Task 5's client code
  before wiring this branch** — no live Razorpay account/dashboard was
  available while writing this task file, so the names above are a
  best-guess placeholder, not a confirmed contract):
  - On `.created` (dispute opened): create or update a `chargebacks` row
    for the payment (status reflecting "open"/"pending" per whatever state
    machine Task 4 gave the `chargebacks` table), write a
    `payment_audit_trail` row, mark processed. Do not touch the
    entitlement yet — a dispute being opened is not the same as it being
    lost.
  - On `.lost` (merchant loses the dispute): update the `chargebacks` row
    to a "lost"/final state, then revoke the entitlement and update
    `learner_course_access` and adjust net revenue the exact same way the
    `refund.processed` branch does (steps 3-5 above), write both
    `payment_audit_trail` and `audit_events` rows (access-revoking), mark
    processed.
  - On `.won` (merchant wins the dispute, no payout to the payer):
    update the `chargebacks` row to a "won"/closed state, write a
    `payment_audit_trail` row, do NOT touch the entitlement (access was
    never revoked for an open dispute, so there's nothing to restore).
    Mark processed.

Unrecognized/unhandled Razorpay event types should be logged and the task
should return `nil` (ack the job, mark `webhook_events.processed_at`) rather
than erroring forever — asynq's retry-with-backoff is for transient
failures (DB down, etc.), not for "we don't have a handler for this event
type yet."

### 2. Two periodic sweep jobs (new files, e.g. `internal/worker/orders.go` and `internal/worker/entitlements.go`, or combined into one file — either is fine as long as each sweep is its own function)

Both sweeps follow `publish.go`'s exact shape: a pure function that does one
pass (`sweepAbandonedOrders(ctx, pool, logger) error` /
`sweepExpiredEntitlements(ctx, pool, logger) error`), and a
`runXSweepLoop(ctx, pool, logger, interval)` wrapper with a `time.Ticker`,
started as its own `go runXSweepLoop(...)` call inside `Run()` in
`worker.go` — mirroring `sweepCtx`/`cancelSweep`/`go
runPublishSweepLoop(...)` exactly (each sweep gets its own ticker/interval;
they don't need to share a context with the publish sweep, but reuse the
same cancellation-on-shutdown pattern). Match Task 4's interval choice
(`publishSweepInterval = time.Minute`) unless the spec's "~30 minute
timeout" demands a coarser interval for efficiency — a 1-minute tick
checking for orders/entitlements that are 30+ minutes stale is fine and
keeps both sweeps on the same simple cadence as Task 4's precedent; define
each as its own named constant (e.g. `abandonOrdersSweepInterval`,
`expireEntitlementsSweepInterval`) even if the values match, so they can be
tuned independently later.

- **Abandon stale orders**: each pass calls `OrderRepo.ListPendingOlderThan`
  (or equivalent — confirm the real Task 4 method name/signature) to find
  `orders` rows with `status IN ('pending', 'payment_initiated')` and
  `created_at` (or whatever the order's "started at" timestamp column is
  called) older than 30 minutes, and for each, sets `status = 'abandoned'`.
  No entitlement/revenue side effects — an abandoned order never had a
  successful payment. A `payment_audit_trail` row per abandoned order is
  reasonable (state transition) but not spec-mandated for `audit_events`
  (no access change happened).

- **Expire fixed-term entitlements**: each pass calls
  `EntitlementRepo.ListActiveExpired` (or equivalent) to find
  `entitlements` rows with `status = 'active'` and `expires_at` in the past
  (non-null and `<= now()`), and for each, in one transaction: set
  `status = 'expired'`, update the corresponding `learner_course_access`
  row's access status to reflect expired access, and write one
  `audit_events` entry (`Action` like `"entitlement.expired"`,
  `ResourceType = "entitlement"`) — this is spec-mandated because letting a
  fixed-term pass lapse is an access-REVOKING action, same category as a
  refund revocation. Process each expired entitlement independently (one
  failure shouldn't block the others), logging per-row errors the same way
  `sweepScheduledPublishes` logs per-course errors in `publish.go`.

Wire both sweeps into `Run()` in `worker.go` next to the existing
`runPublishSweepLoop` call, without altering the existing sweep's
behavior.

### 3. Receipt email job (extend `internal/worker/payments.go` or a
   sibling file, e.g. `internal/worker/receipts.go`)

A new asynq task type, e.g. `TypeSendReceiptEmail = "payments:send_receipt_email"`,
with a payload struct (e.g. `SendReceiptEmailPayload`) carrying: learner ID
(to resolve the recipient email — same `models.ProfileRepo.GetByID` lookup
`notificationRecipient` in `notifications_handlers.go` already does),
course name, currency, subtotal, discount applied (amount and/or code, if
any), tax breakdown (rate and amount), and total paid. **Deliberately does
NOT carry platform commission** — commission is an internal figure between
the platform and the organization, never shown to the learner; the payload
struct should not even have a field for it, so there's no way for a future
template change to accidentally leak it.

`EnqueueSendReceiptEmail(client *asynq.Client, payload
SendReceiptEmailPayload) error` follows the same
`enqueueNotification`-style marshal-and-enqueue helper as
`notifications.go`. The handler follows `notifications_handlers.go`'s exact
shape: resolve the learner's `*models.Profile`, build a subject/HTML body
listing course name / subtotal / discount / tax breakdown / total / currency,
and call `notify.EmailClient.SendEmail(ctx, profile.Email, subject, body)` —
reuse the same `notify.EmailClient` interface and `notify.NewResendClient`
instance already constructed in `worker.go`'s `Run()`, do not build a
second Resend client.

**Design note — do NOT gate this send on `profiles.notification_opt_out`.**
Task 5 established `notificationRecipient`/`sendIfOptedIn` in
`notifications_handlers.go`, which skips sending (but still acks the job)
when `profile.NotificationOptOut` is true. That flag is meant for
marketing/reminder-type notifications (course reminders, announcements),
not proof-of-purchase. A payment receipt is a transactional message the
learner is entitled to regardless of their marketing-notification
preference — suppressing it via `notification_opt_out` would mean a learner
who opted out of marketing emails never receives a receipt for money they
spent, which is a support/compliance problem, not a courtesy. Therefore the
receipt-email handler must call `email.SendEmail(...)` unconditionally
(after resolving the profile), NOT through `sendIfOptedIn`. If a real
transactional-vs-marketing preference field is ever added to `profiles`,
receipts should respect that new field instead — but they must never be
gated by the existing marketing-only opt-out flag.

Register the new `TypeRazorpayWebhook` and `TypeSendReceiptEmail` handlers
on the existing `mux` in `worker.go`'s `Run()`, next to the existing
`mux.HandleFunc(...)` calls — do not change the mux's overall structure or
how it's constructed.

## Acceptance criteria

- [ ] `TypeRazorpayWebhook` handler is idempotent: calling it twice with the
      same Razorpay event ID (i.e. `webhook_events.processed_at` already
      set on the second call) performs zero additional writes to
      `payments`, `orders`, `entitlements`, `learner_course_access`,
      `payment_audit_trail`, or `audit_events`, and does not enqueue a
      second receipt email.
- [ ] A `payment.captured` event, when processed, results in: `payments`
      row `succeeded`, `orders.status = 'succeeded'`, exactly one new
      `active` `entitlements` row with `expires_at` set only for
      fixed-term/subscription offers (from `offers.access_duration_days`),
      an updated/created `learner_course_access` row, one
      `payment_audit_trail` row, one `audit_events` row logging the
      access-granting action, and one enqueued receipt-email job.
- [ ] A `payment.failed` event updates `payments`/`orders` to `failed` and
      writes a `payment_audit_trail` row without creating or touching any
      `entitlements` row.
- [ ] A refund-success event revokes the matching `entitlements` row
      (`status = 'revoked'`), updates `learner_course_access` accordingly,
      writes both a `payment_audit_trail` row and an `audit_events` row
      (access-revoking), and adjusts the order's recorded net revenue.
- [ ] A refund-failure event updates `refunds.status = 'failed'` and does
      NOT touch the associated entitlement.
- [ ] A "lost" chargeback/dispute event revokes the entitlement the same
      way a successful refund does (including both audit writes); a "won"
      dispute event does not touch the entitlement; a "created"/opened
      dispute event creates/updates a `chargebacks` row without touching
      the entitlement. The task file's TODO note about verifying exact
      Razorpay dispute event names against live docs is preserved as a
      code comment at the dispute-handling branch for whoever implements
      it.
- [ ] The stale-order sweep and the expired-entitlement sweep are each
      registered as their own `time.Ticker`-driven goroutine started in
      `Run()` in `worker.go`, following `publish.go`'s
      `runPublishSweepLoop` shape exactly — neither uses asynq's
      `PeriodicTaskConfigProvider`/periodic-task manager.
- [ ] The stale-order sweep flips `orders` with `status IN ('pending',
      'payment_initiated')` older than 30 minutes to `abandoned`, and
      never touches an order that was updated (e.g. to `succeeded`) after
      the 30-minute mark.
- [ ] The expired-entitlement sweep flips `active` entitlements whose
      `expires_at` has passed to `expired`, updates the corresponding
      `learner_course_access` row, and writes one `audit_events` row per
      expired entitlement.
- [ ] The receipt-email job sends via the same `notify.EmailClient`/
      `notify.NewResendClient` plumbing Task 5 built (no second Resend
      client), includes course name, subtotal, discount (if any), tax
      breakdown, total paid, and currency, and never includes platform
      commission anywhere in the payload or rendered email.
- [ ] The receipt-email handler does NOT gate sending on
      `profiles.notification_opt_out` (unlike the Task 5 notification
      handlers) — it always sends to a resolved learner profile's email.
- [ ] All new task types are registered on the existing `asynq.NewServeMux()`
      in `worker.go`'s `Run()` without changing its overall construction,
      and the worker package still builds/runs standalone exactly as
      before (no change to `cmd/worker`'s entrypoint contract).

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
