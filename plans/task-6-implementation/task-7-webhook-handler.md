---
task: 7
name: webhook-handler
parallel_group: 3
depends_on: [4, 5]
issue: TBD
---

# Task 7: webhook-handler

## What to build

Add a `POST /api/webhooks/razorpay` endpoint that receives Razorpay's payment
webhook deliveries (`payment.captured`, `payment.failed`, `refund.processed`,
`refund.failed`, and any other subscribed event types), verifies the
cryptographic signature, idempotency-gates the event, and enqueues an asynq
task for asynchronous processing. This handler is the payments-domain analog
of the existing `BunnyWebhook` handler in
`internal/httpserver/handlers/webhooks.go` and MUST mirror its structure
exactly — read the whole of that file before writing any code here. Route
registration in `internal/httpserver/server.go` (alongside the existing
`engine.POST("/api/webhooks/bunny", handlers.BunnyWebhook(d))` line, at
`internal/httpserver/server.go:298`) is this task's responsibility too;
follow the URL path convention already established there
(`/api/webhooks/<provider>`, not `/webhooks/<provider>`).

### Where the code goes

Check the current line count and structure of
`internal/httpserver/handlers/webhooks.go` before deciding. If it is still
small (it is ~65 lines as of this writing, holding only `BunnyWebhook` and
its payload struct), add the new handler to that same file, following its
existing naming convention (`bunnyWebhookPayload` → a new
`razorpayWebhookEnvelope`-style struct, `BunnyWebhook` → `RazorpayWebhook`).
If by the time this task is implemented the file has grown large enough that
adding a second, unrelated provider's webhook logic would make it unwieldy
(a matter of judgment — there is no hard line count), create a sibling file
`internal/httpserver/handlers/webhooks_razorpay.go` in the same `handlers`
package instead, and leave `webhooks.go` untouched. Either way, both
handlers must end up registered the same way (a `func(d *AuthDeps)
gin.HandlerFunc` constructor closed over `AuthDeps`, matching every other
handler in this package — see `internal/httpserver/handlers/deps.go`).

### Dependencies this task assumes exist

This task depends on Task 4 (models-repositories) and Task 5
(razorpay-client), both landing schema/code this handler consumes but does
not itself define:

- **`models.WebhookEventRepo`** (Task 4) — must expose a method with
  idempotency-gating INSERT semantics, e.g.
  `TryRecord(ctx context.Context, eventID, eventType string) (isNew bool,
  err error)`, backed by a table (e.g. `webhook_events`) with a UNIQUE
  constraint on the dedup key and an `INSERT ... ON CONFLICT DO NOTHING`
  (or equivalent) that reports whether the row was newly inserted. If Task
  4 named this repo, its constructor, or its method differently, use
  whatever it actually shipped as — the important contract this task relies
  on is: **one DB round trip, one write, returns a new-vs-duplicate signal,
  no other side effects.**
- **`Provider.VerifyWebhookSignature(body []byte, signature string) bool`**
  (Task 5) — a method on the `internal/payments.Provider` interface (the
  Razorpay adapter, mirroring `internal/media.BunnyClient`'s
  `VerifyWebhookSignature` used by `BunnyWebhook` today at
  `internal/httpserver/handlers/webhooks.go:38`). This task calls it but
  does not implement it.
- **`AuthDeps`** (`internal/httpserver/handlers/deps.go`) must have grown a
  field exposing the payments provider and the new repo by the time this
  task lands — e.g. `Payments payments.Provider` and `WebhookEvents
  *models.WebhookEventRepo` (exact field names depend on what Task 4/5/6
  actually named them; check `deps.go` at implementation time and use
  whatever is there, following the existing pattern where Task 4's course
  fields and Task 5's learner-journey fields were added to the same struct
  under a `// Task N: <name>` comment block).
- **`worker.EnqueueRazorpayWebhook`** and a `worker.TypeRazorpayWebhook`
  task type constant, following `internal/worker/tasks.go`'s existing
  `TypeBunnyTranscodeComplete` / `EnqueueBunnyTranscodeComplete` pattern
  exactly (see below — this task's own responsibility, not Task 8's, since
  the enqueue function and its payload type are part of the handler/worker
  contract this task defines).

### Handler flow (mirrors `BunnyWebhook` step for step)

1. Read the **raw** request body via `io.ReadAll(c.Request.Body)`. The raw
   bytes are what gets HMAC-verified and what should be forwarded to the
   worker — never re-marshal a parsed struct and verify/forward that
   instead, since re-serialization is not guaranteed byte-identical to what
   Razorpay actually sent and signed.
2. Extract the `X-Razorpay-Signature` header via `c.GetHeader(...)`.
3. Call `d.Payments.VerifyWebhookSignature(body, signature)` (adjust the
   `AuthDeps` field name to whatever Task 5 actually named it) **before
   parsing or touching the body's contents in any other way.** If it
   returns `false`, respond `401` immediately (`c.JSON(http.StatusUnauthorized,
   gin.H{"error": "invalid signature"})`, matching `BunnyWebhook`'s exact
   wording) and return. Do nothing else — no DB write, no enqueue, no
   logging of the payload contents.
4. Only after signature verification succeeds, unmarshal enough of the JSON
   body to extract:
   - the event type string (Razorpay's top-level `event` field, e.g.
     `"payment.captured"`, `"payment.failed"`, `"refund.processed"`,
     `"refund.failed"`), and
   - a stable, unique identifier to use as the idempotency dedup key.

   **Open question the implementer must resolve against Razorpay's live
   webhook docs, not this task file:** unlike some providers, Razorpay's
   webhook POST body does not always carry one single unmistakable
   top-level "event id" field the way (for example) Stripe's `id` field
   does. This task file was written without access to a live Razorpay
   account or a captured real payload to inspect, so treat the following as
   a **starting hypothesis to verify, not a confirmed contract**:
   - Check whether Razorpay sends a delivery-level unique ID as an HTTP
     request header (something in the shape of
     `X-Razorpay-Event-Id` — the exact header name is unconfirmed and must
     be checked against current Razorpay webhook documentation and/or a
     real captured payload before relying on it). If such a header exists
     and is reliably present, prefer it as the dedup key — it is the
     cleanest option because it does not require parsing payload internals
     to deduplicate.
   - If no such header exists, derive a composite dedup key from the
     payload body instead: combine the `event` type string with the
     relevant entity's provider ID (e.g. `payload.payment.entity.id` for
     `payment.*` events, `payload.refund.entity.id` for `refund.*` events —
     exact JSON path depends on Razorpay's current webhook envelope shape,
     which must be checked at implementation time) and a timestamp field
     present in the payload (e.g. `created_at`), joined into one string
     (e.g. `"payment.captured:pay_ABC123:1700000000"`). The goal is a key
     that is (a) identical across Razorpay's retries of the *same*
     delivery attempt for the *same* underlying event, and (b) different
     across genuinely distinct events (so a `payment.captured` followed
     later by a `refund.processed` for the same payment ID must NOT
     collide).
   - Whichever approach is used, add a short code comment at the extraction
     site recording which option was chosen and why, so a future reader
     doesn't have to re-derive this reasoning.
   - Malformed JSON, or JSON missing the fields needed to build the dedup
     key, should be rejected with `400` (mirroring `BunnyWebhook`'s
     `json.Unmarshal` failure path) — signature verification passing does
     not mean the payload is well-formed.
5. Call `d.WebhookEvents.TryRecord(c.Request.Context(), eventID, eventType)`
   (adjust the field/method name to whatever Task 4 actually shipped). This
   is the **only** DB write this handler ever performs.
   - If it reports the event was **already recorded** (a duplicate
     delivery — Razorpay retries webhooks that don't get a prompt `200`),
     respond `200 OK` immediately and return, **without enqueueing
     anything**. The prior delivery either already queued or already
     finished the real processing; silently accept-and-ignore is correct
     here, and returning `200` is what stops Razorpay from continuing to
     retry.
   - If it reports the event is **new**, continue to step 6.
   - If the repo call itself errors (DB failure, not a duplicate), respond
     `500` (mirroring `BunnyWebhook`'s enqueue-failure path) — do not
     silently swallow a DB error as if it were a duplicate.
6. Enqueue a `worker.TypeRazorpayWebhook` asynq task carrying the event type
   and the raw payload bytes (or a typed struct decoded from them — follow
   whichever `BunnyWebhook` does today, which is a typed payload struct;
   for Razorpay, a typed struct covering the fields needed to route/process
   the event downstream is preferable to passing raw bytes through, but
   raw-bytes-plus-event-type is acceptable if simpler and the eventual
   worker job (Task 8) can parse it). Add the corresponding
   `RazorpayWebhookPayload` struct, `TypeRazorpayWebhook` constant, and
   `EnqueueRazorpayWebhook(client *asynq.Client, payload
   RazorpayWebhookPayload) error` function to `internal/worker/tasks.go`,
   directly mirroring `BunnyTranscodeCompletePayload` /
   `TypeBunnyTranscodeComplete` / `EnqueueBunnyTranscodeComplete` (lines
   14-48 of that file) in shape, doc-comment style, and error-wrapping
   (`fmt.Errorf("worker: marshal razorpay webhook payload: %w", err)` /
   `fmt.Errorf("worker: enqueue razorpay webhook task: %w", err)`). If
   enqueueing fails, respond `500` and return — do not respond `200` for an
   event that was recorded as seen but never actually queued for
   processing (that would permanently drop it, since Razorpay would stop
   retrying).
7. On successful enqueue, respond `200 OK` immediately
   (`c.Status(http.StatusOK)`). Do not block on or wait for the worker to
   process the task — the worker job itself (Task 8's responsibility) runs
   fully asynchronously.

### Non-negotiable security constraint (from this repo's CLAUDE.md)

Payment/entitlement access must only ever be granted after verified
provider webhook events, never synchronously from an HTTP request path.
This handler's **only** DB write, in any code path, is the
`WebhookEventRepo.TryRecord` idempotency-gate insert in step 5. It must
**never**:

- create, update, or look up an `orders`, `payments`, `refunds`,
  `chargebacks`, or `entitlements` row (or any other business-state table),
- grant, extend, or revoke a learner's course access,
- perform any write that depends on interpreting the *meaning* of the
  event (e.g. "this was a successful payment, so grant access") — that
  interpretation and every resulting state change belongs entirely to the
  Task 8 worker job that consumes `TypeRazorpayWebhook`, which runs after
  this handler has already returned `200`.

This exactly mirrors `BunnyWebhook`'s existing division of responsibility:
the HTTP handler's job is authenticate-the-caller (signature) +
deduplicate-the-delivery (idempotency insert) + hand-off (enqueue); the
worker's job is everything that actually changes state. Do not collapse
these two concerns to "simplify" this task — the separation is what lets
the payment webhook remain fast, retry-safe, and safe to call from an
unauthenticated public endpoint (Razorpay, like Bunny, cannot present a
session cookie or bearer token — signature verification is the only trust
boundary).

### Route registration

In `internal/httpserver/server.go`, register the new route next to the
existing Bunny webhook route (near line 298):

```go
engine.POST("/api/webhooks/bunny", handlers.BunnyWebhook(d))
engine.POST("/api/webhooks/razorpay", handlers.RazorpayWebhook(d))
```

This route must sit outside any auth/CSRF middleware group that ordinary
API routes go through, exactly like the Bunny webhook route does today —
confirm by checking how `/api/webhooks/bunny` is currently exempted (or not
subject to) session-auth/CSRF middleware in `server.go`'s route-group
setup, and register `/api/webhooks/razorpay` the same way. Signature
verification (step 3 above) is this route's only authentication
mechanism, by design.

## Acceptance criteria

- [ ] `POST /api/webhooks/razorpay` exists and is wired in
      `internal/httpserver/server.go`, registered the same way (same
      middleware exemptions) as the existing `/api/webhooks/bunny` route.
- [ ] The handler reads the raw request body and verifies
      `X-Razorpay-Signature` against it via the Task 5 `Provider`'s
      `VerifyWebhookSignature` method **before** any JSON parsing occurs.
- [ ] An invalid or missing signature results in `401` with no DB write and
      no enqueue — proven by an automated test that POSTs a
      tampered/unsigned body and asserts both the HTTP status and that
      neither `webhook_events` nor any downstream table changed.
- [ ] A valid signature with a well-formed payload results in exactly one
      `webhook_events` row (via `WebhookEventRepo.TryRecord`) and exactly
      one enqueued `TypeRazorpayWebhook` asynq task.
- [ ] Delivering the identical payload+signature twice (simulating a
      Razorpay retry) results in only one `webhook_events` row and only one
      enqueued task in total — the second delivery gets `200` without a
      second enqueue. Proven by an automated test.
- [ ] The handler's code path contains no call to any repository or query
      touching `orders`, `payments`, `refunds`, `chargebacks`, or
      `entitlements` (or equivalent Task 4/6 table names) — its only write
      is the `webhook_events` idempotency insert. Verify by grep/review,
      not just by test passing.
- [ ] A DB error from `TryRecord` (not a duplicate — an actual failure)
      results in `500`, not a silent `200`.
- [ ] An enqueue failure after a successful `TryRecord` insert results in
      `500`, not `200` (so Razorpay retries and the event is not
      permanently lost).
- [ ] `worker.TypeRazorpayWebhook`, a `RazorpayWebhookPayload` struct, and
      `EnqueueRazorpayWebhook` exist in `internal/worker/tasks.go`,
      matching `TypeBunnyTranscodeComplete` /
      `BunnyTranscodeCompletePayload` / `EnqueueBunnyTranscodeComplete` in
      structure and doc-comment style.
- [ ] The dedup-key derivation strategy actually implemented (delivery
      header vs. composite `event`+entity-ID+timestamp) is documented in a
      code comment at the extraction site, noting that it was chosen after
      checking Razorpay's current webhook documentation/payload shape at
      implementation time (this task file could not verify that shape
      against a live account).
- [ ] `go build ./...` passes and `go test ./internal/httpserver/...
      ./internal/worker/...` passes, including the new signature-rejection
      and idempotency tests above.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
