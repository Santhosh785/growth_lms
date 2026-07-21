---
task: 2
name: config-secrets
parallel_group: 1
depends_on: []
issue: TBD
---

# Task 2: config-secrets

## What to build

Extend `internal/config/config.go` to add the Razorpay webhook secret that
Task 6 (commerce/payments) needs and to add a hardcoded timeout constant for
abandoning stale pending orders. No other config surface is needed for this
task.

Current state (read before editing):

- `RazorpayConfig` (in `internal/config/config.go`) currently has only:
  ```go
  type RazorpayConfig struct {
  	KeyID     string
  	KeySecret string
  }
  ```
  populated from required env vars `LMS_RAZORPAY_KEY_ID` and
  `LMS_RAZORPAY_KEY_SECRET` (no validator function, just presence-checked),
  both listed in the `required := []requiredVar{...}` slice in `Load()`.

- The existing model for a provider webhook secret is `BunnyNetConfig`:
  ```go
  type BunnyNetConfig struct {
  	APIKey        string
  	StorageZone   string
  	CDNURL        string
  	WebhookSecret string
  }
  ```
  populated from the required env var `LMS_BUNNY_WEBHOOK_SECRET` (also
  presence-checked only, no validator), and wired into `cfg.BunnyNet.WebhookSecret
  = values["LMS_BUNNY_WEBHOOK_SECRET"]` in the `cfg := &Config{...}` literal.

- The established pattern for a small tunable duration used by exactly one
  call site is a **hardcoded Go constant next to its usage**, not a config
  field. Precedent: `internal/worker/worker.go` declares
  `const publishSweepInterval = time.Minute` right above `Run()`, with a
  comment explaining the choice, and `internal/httpserver/server.go:141`
  passes a literal `15*time.Minute` directly as an argument to
  `ratelimit.New(...)` at its one call site. Neither of these is a `Config`
  field. There is no existing precedent in this codebase for promoting a
  single-use duration like this into `config.Config`.

### Changes to make

1. **Add `WebhookSecret` to `RazorpayConfig`**, mirroring `BunnyNetConfig`'s
   shape exactly:
   ```go
   type RazorpayConfig struct {
   	KeyID         string
   	KeySecret     string
   	WebhookSecret string
   }
   ```

2. **Add `LMS_RAZORPAY_WEBHOOK_SECRET` to the `required` slice** in `Load()`,
   directly after `{"LMS_RAZORPAY_KEY_SECRET", nil}`, with no validator
   (`nil`), matching how `LMS_BUNNY_WEBHOOK_SECRET` is declared:
   ```go
   {"LMS_RAZORPAY_KEY_ID", nil},
   {"LMS_RAZORPAY_KEY_SECRET", nil},
   {"LMS_RAZORPAY_WEBHOOK_SECRET", nil},
   ```
   This makes it required and fail-fast: if unset or blank, `Load()` returns
   a `config: invalid configuration` error listing
   `LMS_RAZORPAY_WEBHOOK_SECRET is required`, exactly like every other
   required var — no separate code path needed.

3. **Wire it into the `cfg := &Config{...}` literal**:
   ```go
   Razorpay: RazorpayConfig{
   	KeyID:         values["LMS_RAZORPAY_KEY_ID"],
   	KeySecret:     values["LMS_RAZORPAY_KEY_SECRET"],
   	WebhookSecret: values["LMS_RAZORPAY_WEBHOOK_SECRET"],
   },
   ```

4. **Do not add `WebhookSecret` (or `KeySecret`) to `Config.Redacted()`.**
   `Redacted()` currently only surfaces non-secret fields (URLs, zone names,
   flags) — it must never gain a Razorpay entry that exposes `KeySecret` or
   `WebhookSecret`, even in masked/redacted form, since this map is intended
   for startup logs.

5. **Do not add a config field for the stale-pending-order abandon
   timeout.** Instead, whichever Task 6 subtask implements the
   stale-order-abandon asynq sweep must declare a hardcoded constant next to
   its usage — e.g. in the file that defines the sweep loop/handler —
   following the `publishSweepInterval = time.Minute` precedent in
   `internal/worker/worker.go`:
   ```go
   // pendingOrderAbandonTimeout is how long a `pending` order may sit
   // before the sweep marks it `abandoned`. 30 minutes matches the plan's
   // stale-pending-order design (see plans/task-6-implementation/main-plan.md).
   const pendingOrderAbandonTimeout = 30 * time.Minute
   ```
   This task (config-secrets) only needs to leave a note pointing future
   Task 6 subtasks at this precedent — it does not itself implement the
   sweep, the asynq task type, or the query that uses this constant. Do not
   add `PendingOrderAbandonTimeout` (or similar) to `Config` or to any
   `RazorpayConfig`/commerce config struct.

6. **Update `internal/config/config_test.go`** so existing tests that build
   a full valid env (if any set every required var) also set
   `LMS_RAZORPAY_WEBHOOK_SECRET`, and add/extend a test asserting that
   `Load()` fails with a descriptive error when `LMS_RAZORPAY_WEBHOOK_SECRET`
   is missing, mirroring whatever existing test covers
   `LMS_BUNNY_WEBHOOK_SECRET` being required.

### Security requirement (hard constraint, not just a test)

- `Razorpay.KeyID` is the Razorpay **publishable/public key** — the Razorpay
  Checkout.js embed on the frontend needs it client-side, so it is safe to
  render into an HTML template or return in a JSON response to the browser.
- `Razorpay.KeySecret` and `Razorpay.WebhookSecret` are **never** safe to
  expose. Neither field may ever be:
  - interpolated into an HTML template rendered to a browser,
  - included in any JSON response body,
  - logged (including via `Config.Redacted()`, per point 4 above),
  - passed to any handler that constructs a browser-facing response.
  They must only be read server-side: `KeySecret` to sign/verify outgoing
  Razorpay API calls, `WebhookSecret` to verify the HMAC signature on
  incoming Razorpay webhook POSTs (a separate secret from `KeySecret`,
  configured independently in the Razorpay dashboard — do not conflate the
  two or reuse one value for both purposes). This constraint applies to all
  future Task 6 subtasks that consume `cfg.Razorpay.*`, not just this one;
  this task's job is to make sure the config struct's shape and comments
  make the distinction unambiguous (e.g. via doc comments on the
  `RazorpayConfig` fields) so later subtasks don't accidentally wire the
  wrong field into a template or JSON response.

## Acceptance criteria

- [ ] `RazorpayConfig` in `internal/config/config.go` has a `WebhookSecret
      string` field in addition to the existing `KeyID` and `KeySecret`
      fields, with doc comments distinguishing which field(s) are
      browser-safe (`KeyID` only) vs. server-only-secret (`KeySecret`,
      `WebhookSecret`).
- [ ] `LMS_RAZORPAY_WEBHOOK_SECRET` is added to the `required` slice in
      `Load()` (no validator function, matching `LMS_BUNNY_WEBHOOK_SECRET`'s
      pattern) and is wired into `cfg.Razorpay.WebhookSecret`.
- [ ] `Load()` returns a fail-fast, descriptive error (via the existing
      `errs`/`required` mechanism — no new error path) when
      `LMS_RAZORPAY_WEBHOOK_SECRET` is unset or blank, exactly like the
      other required vars.
- [ ] `Config.Redacted()` is unchanged with respect to Razorpay — it does
      not gain any entry exposing `KeySecret` or `WebhookSecret`, redacted
      or otherwise.
- [ ] No new config field, struct, or env var is added for the
      stale-pending-order abandon timeout. `internal/config/config.go`
      contains a comment (near `RazorpayConfig` or in this task's diff)
      pointing future Task 6 subtasks at the `publishSweepInterval =
      time.Minute` precedent in `internal/worker/worker.go` for how to
      declare the 30-minute abandon timeout as a hardcoded constant near
      its point of use.
- [ ] `internal/config/config_test.go` is updated: any test that builds a
      fully-valid env sets `LMS_RAZORPAY_WEBHOOK_SECRET`, and a test exists
      asserting `Load()` fails when `LMS_RAZORPAY_WEBHOOK_SECRET` is
      missing/blank.
- [ ] `go build ./...` and `go test ./internal/config/...` pass.
- [ ] Grep confirms `KeySecret` and `WebhookSecret` (Razorpay fields) are
      not referenced anywhere under `internal/httpserver/handlers/` in a way
      that writes them into an HTML template context or a JSON response
      struct — this task only touches `internal/config/`, so this should
      trivially hold, but the acceptance check is here to make the
      constraint explicit for reviewers of later Task 6 subtasks that do
      touch handlers.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
