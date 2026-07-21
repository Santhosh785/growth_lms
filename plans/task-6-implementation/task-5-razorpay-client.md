---
task: 5
name: razorpay-client
parallel_group: 2
depends_on: [2]
issue: TBD
---

# Task 5: razorpay-client

## What to build

Create a new package `internal/payments` with a single file
`internal/payments/razorpay.go` implementing the Razorpay adapter that Task
6's commerce handlers, webhook handler, and worker jobs will consume. This
task builds the client only — it does not modify
`internal/httpserver/handlers/deps.go` or `internal/httpserver/server.go`
(those get a `Payments payments.Provider` field wired up by whichever
consuming task needs it first, per the dependency graph in
`plans/task-6-implementation/main-plan.md`). This task's only job is to
produce a package that compiles standalone and is trivially pluggable later.

Mirror `internal/media/bunny.go`'s pattern exactly — read that file in full
before writing code. It defines: a small interface for what handlers need
from the external provider, a real HTTP-backed implementation struct, a
constructor taking a typed config struct, a package-level
`var _ Interface = (*RealImpl)(nil)` compile-time assertion, and a
`VerifyWebhookSignature(payload []byte, signatureHeader string) bool` method
built on `crypto/hmac` + `crypto/sha256` + `encoding/hex` +
`crypto/subtle.ConstantTimeCompare`. Copy that exact security pattern
verbatim (hex-encode the computed HMAC digest, then
`subtle.ConstantTimeCompare` against the header bytes, returning `false`
early if the secret or header is empty) — do not reinvent it, and never
compare digests with `==`. Also copy `bunny.go`'s package doc-comment style
noting this is a best-effort implementation against documented REST API
shapes, not exercised against a live account in this session (no live
Razorpay credentials are available here — same caveat Task 4's Bunny client
carries).

`internal/config.RazorpayConfig` (already extended by Task 2 of this plan,
`plans/task-6-implementation/task-2-config-secrets.md`) has this shape by
the time this task runs:

```go
type RazorpayConfig struct {
	KeyID         string // browser-safe publishable key
	KeySecret     string // server-only secret — never render to browser/log
	WebhookSecret string // server-only secret — distinct from KeySecret
}
```

### Provider interface

Define in `internal/payments/razorpay.go`:

```go
type Provider interface {
	// CreateOrder creates a Razorpay order for amountMinorUnits (already
	// computed server-side, in the smallest currency unit — paise for
	// INR, cents for USD; Razorpay's Orders API always takes an integer
	// minor-unit amount, never a decimal major-unit amount) in the given
	// currency ("INR" or "USD"), tagged with receipt as the caller's own
	// order/reference ID. Returns Razorpay's order ID (e.g. "order_xxx").
	CreateOrder(ctx context.Context, amountMinorUnits int64, currency string, receipt string) (orderID string, err error)

	// VerifyWebhookSignature HMAC-SHA256-verifies payload (the raw,
	// unparsed webhook request body) against signatureHeader (the raw
	// value of the X-Razorpay-Signature header) using the configured
	// webhook secret, constant-time. Only a call site that has already
	// gotten true from this may treat the webhook event as authentic.
	VerifyWebhookSignature(payload []byte, signatureHeader string) bool

	// VerifyPaymentSignature HMAC-SHA256-verifies Razorpay checkout.js's
	// client-side success-callback signature: HMAC-SHA256 of
	// "razorpayOrderID|razorpayPaymentID" using the key secret, hex
	// digest, constant-time compared against razorpaySignature.
	//
	// SECURITY CONSTRAINT — READ BEFORE CALLING THIS FROM A HANDLER:
	// a true result here proves the browser's checkout.js callback data
	// is authentic, but it must NEVER be used to mark an order/payment
	// succeeded or to grant a learner entitlement. Per this repo's
	// CLAUDE.md, entitlements/access are granted only from a verified
	// asynchronous webhook event (processed via VerifyWebhookSignature
	// above, on the server-to-server webhook POST), never from a
	// browser-driven return/callback — even one whose signature checks
	// out, since a browser round-trip can be interrupted, replayed, or
	// simply never happen (tab closed mid-checkout) while the webhook
	// still fires independently. This method exists solely so a
	// checkout-callback HTTP handler (Task 6's commerce-handlers, not
	// this task) can optimistically render a "verifying your payment..."
	// UI state while it polls/waits for the real webhook-driven order
	// status to flip. Do not wire this method's return value into any
	// code path that updates order status, entitlement status, or
	// access_status.
	VerifyPaymentSignature(razorpayOrderID, razorpayPaymentID, razorpaySignature string) bool

	// CreateRefund calls Razorpay's Refunds API for razorpayPaymentID,
	// refunding amountMinorUnits (same minor-unit convention as
	// CreateOrder; pass the full original amount for a full refund).
	// Returns Razorpay's refund ID (e.g. "rfnd_xxx").
	CreateRefund(ctx context.Context, razorpayPaymentID string, amountMinorUnits int64) (refundID string, err error)
}
```

### Real implementation

```go
type RazorpayProvider struct {
	keyID         string
	keySecret     string
	webhookSecret string
	http          *http.Client
}

func NewRazorpayProvider(cfg config.RazorpayConfig) *RazorpayProvider {
	return &RazorpayProvider{
		keyID:         cfg.KeyID,
		keySecret:     cfg.KeySecret,
		webhookSecret: cfg.WebhookSecret,
		http:          &http.Client{Timeout: 15 * time.Second},
	}
}

var _ Provider = (*RazorpayProvider)(nil)
```

(15-second timeout and the constructor shape match `NewBunnyClient`'s
pattern exactly — this makes future wiring into `AuthDeps` a one-line
`Payments: payments.NewRazorpayProvider(cfg.Razorpay)`, mirroring
`server.go`'s existing `Bunny: media.NewBunnyClient(cfg.BunnyNet)`.)

Implementation notes per method, against Razorpay's documented REST API:

1. **`CreateOrder`**: `POST https://api.razorpay.com/v1/orders`, HTTP Basic
   Auth with `req.SetBasicAuth(keyID, keySecret)`, JSON body
   `{"amount": amountMinorUnits, "currency": currency, "receipt": receipt}`,
   `Content-Type: application/json`. On a non-2xx response, return an error
   including the status code (mirror `bunny.go`'s
   `fmt.Errorf("media: bunny create-library returned status %d", ...)`
   style, using a `payments:` prefix instead). Decode the JSON response body
   (`{"id": "order_xxx", ...}`) and return the `id` field as `orderID`.

2. **`VerifyWebhookSignature`**: identical structure to
   `RealBunnyClient.VerifyWebhookSignature` in `bunny.go` — return `false`
   immediately if `webhookSecret == "" || signatureHeader == ""`; otherwise
   `hmac.New(sha256.New, []byte(webhookSecret))`, write `payload`, hex-encode
   the sum, and `subtle.ConstantTimeCompare` against `[]byte(signatureHeader)
   == 1`. Razorpay sends this as a hex digest in the `X-Razorpay-Signature`
   header (the header name itself is documentation context for the future
   webhook handler — this method takes the header value as a plain string
   parameter and does not read `net/http.Header` itself, matching
   `BunnyClient.VerifyWebhookSignature`'s signature).

3. **`VerifyPaymentSignature`**: return `false` immediately if `keySecret ==
   "" || razorpayOrderID == "" || razorpayPaymentID == "" ||
   razorpaySignature == ""`. Otherwise compute
   `hmac.New(sha256.New, []byte(keySecret))`, write
   `[]byte(razorpayOrderID + "|" + razorpayPaymentID)`, hex-encode, and
   `subtle.ConstantTimeCompare` against `[]byte(razorpaySignature)`. Same
   constant-time pattern, `keySecret` instead of `webhookSecret` as the HMAC
   key. Carry the full security-constraint doc comment from the interface
   definition above onto this method's implementation too (not just the
   interface) so it's visible from either side.

4. **`CreateRefund`**: `POST
   https://api.razorpay.com/v1/payments/{razorpayPaymentID}/refund`, same
   Basic Auth, JSON body `{"amount": amountMinorUnits}`, `Content-Type:
   application/json`. On non-2xx, error with status code, same style as
   `CreateOrder`. Decode `{"id": "rfnd_xxx", ...}` and return `id` as
   `refundID`.

Use `context.Context` (first param) on `CreateOrder` and `CreateRefund` via
`http.NewRequestWithContext`, matching `bunny.go`. `VerifyWebhookSignature`
and `VerifyPaymentSignature` take no context (pure functions), matching
`BunnyClient.VerifyWebhookSignature`'s existing signature.

### Test fake (optional but recommended, mirrors `internal/media/mediatest`)

If time permits, add `internal/payments/paymentstest/fakes.go` providing a
`FakeProvider` implementing `payments.Provider` with configurable
return/error values and a `var _ payments.Provider = (*FakeProvider)(nil)`
assertion, following `internal/media/mediatest/fakes.go`'s `FakeBunnyClient`
shape exactly (deterministic IDs, a `WebhookSecret`/valid-signature-string
convention for the verify methods). This lets Task 6's handler/webhook/worker
tasks unit-test against a fake instead of needing live Razorpay credentials,
same role `FakeBunnyClient` plays for Task 4/6 media tests.

### Out of scope for this task

- Do not modify `internal/httpserver/handlers/deps.go` or
  `internal/httpserver/server.go` — no `Payments` field is added to
  `AuthDeps`, and no `payments.NewRazorpayProvider(...)` call is added to
  `server.go`'s dependency-construction block. That wiring belongs to
  whichever Task 6 subtask (commerce-handlers, webhook-handler, or
  worker-jobs) first needs `Payments` on `AuthDeps`.
- Do not implement order/payment/refund persistence, checkout HTML pages,
  webhook HTTP handlers, or asynq worker jobs — those are separate Task 6
  subtasks (commerce-handlers, webhook-handler, worker-jobs) that will
  import and call this package.
- Do not add Razorpay Subscriptions API calls — per this plan's binding
  decision, "subscription" offers are fixed-term passes billed as a single
  one-time `CreateOrder` call, not real recurring billing.

## Acceptance criteria

- [ ] `internal/payments/razorpay.go` exists with a package doc comment
      describing scope and the "best-effort, not exercised against a live
      Razorpay account" caveat, mirroring `internal/media/bunny.go`'s doc
      comment style.
- [ ] `Provider` interface is defined with exactly the four methods above:
      `CreateOrder`, `VerifyWebhookSignature`, `VerifyPaymentSignature`,
      `CreateRefund`, with doc comments on each (including the full
      security-constraint comment on `VerifyPaymentSignature`).
- [ ] `RazorpayProvider` struct holds `keyID`, `keySecret`, `webhookSecret`
      (all unexported), and an `*http.Client`.
- [ ] `NewRazorpayProvider(cfg config.RazorpayConfig) *RazorpayProvider`
      constructor exists and populates all fields from `cfg`, with a 15
      second HTTP client timeout.
- [ ] `var _ Provider = (*RazorpayProvider)(nil)` compile-time assertion is
      present.
- [ ] `CreateOrder` POSTs to `https://api.razorpay.com/v1/orders` with HTTP
      Basic Auth (`keyID`/`keySecret`), a JSON body containing `amount`,
      `currency`, and `receipt`, and returns the decoded `id` field as the
      order ID. Non-2xx responses return a descriptive error including the
      HTTP status code.
- [ ] `VerifyWebhookSignature` uses `crypto/hmac` + `crypto/sha256` +
      `encoding/hex` + `crypto/subtle.ConstantTimeCompare` exactly as
      `RealBunnyClient.VerifyWebhookSignature` does in `bunny.go` — no `==`
      comparison of digests anywhere in this file. Returns `false`
      immediately when the webhook secret or signature header is empty.
- [ ] `VerifyPaymentSignature` computes HMAC-SHA256 of
      `razorpayOrderID + "|" + razorpayPaymentID` using `keySecret`,
      hex-encodes it, and compares via `subtle.ConstantTimeCompare` against
      `razorpaySignature`. Returns `false` immediately when `keySecret` or
      any input string is empty. Its doc comment explicitly states this
      method's result must never be used to grant access/entitlement or mark
      an order/payment succeeded — only a verified webhook event
      (`VerifyWebhookSignature`) may do that.
- [ ] `CreateRefund` POSTs to
      `https://api.razorpay.com/v1/payments/{razorpayPaymentID}/refund` with
      the same Basic Auth, a JSON body containing `amount`, and returns the
      decoded `id` field as the refund ID. Non-2xx responses return a
      descriptive error including the HTTP status code.
- [ ] No `==` comparison is used anywhere on HMAC digests or signature
      strings in this file (grep for `subtle.ConstantTimeCompare` to confirm
      it's used in both `VerifyWebhookSignature` and
      `VerifyPaymentSignature`).
- [ ] `internal/httpserver/handlers/deps.go` and
      `internal/httpserver/server.go` are unmodified by this task.
- [ ] `go build ./...` succeeds with the new package included.
- [ ] `go vet ./internal/payments/...` is clean.
- [ ] If `internal/payments/paymentstest/fakes.go` is added, it compiles and
      `var _ payments.Provider = (*FakeProvider)(nil)` holds.
- [ ] If unit tests are added for `RealRazorpayProvider`/`RazorpayProvider`
      (recommended, especially for the two pure signature-verification
      methods — valid signature, tampered payload, wrong secret, empty
      secret/header cases — these don't require live Razorpay credentials),
      `go test ./internal/payments/...` passes.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
