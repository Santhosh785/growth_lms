---
task: 4
name: models-repositories
parallel_group: 2
depends_on: [1]
issue: TBD
---

# Task 4: models-repositories

## What to build

Add `internal/models` repositories for the eleven commerce tables created by
Task 1 (db-migration) of this plan: `offers`, `discount_codes`,
`commerce_invite_tokens`, `orders`, `payments`, `refunds`, `chargebacks`,
`entitlements`, `payment_audit_trail`, `webhook_events`,
`platform_settings`.

Follow the exact conventions already established in `internal/models`
(read `internal/models/audit.go`, `internal/models/learner_course_access.go`,
and `internal/models/course.go` before writing anything):

- Every repo method takes `q Querier` (defined in `internal/models/querier.go`,
  satisfied by both `*pgxpool.Pool` and `pgx.Tx`) as its first parameter after
  `ctx` — never a concrete pool or transaction type. This lets every method
  run inside the request's RLS-scoped transaction (`dbctx.RequestTx`, the
  normal path so `app.current_org_id`/`app.current_user_id` session
  variables are in effect), a worker's pool connection, or a test harness's
  transaction.
- One repo type per table (`type XRepo struct{}`), constructed with
  `NewXRepo() *XRepo { return &XRepo{} }` — no fields, no state.
- One plain Go struct per table mirroring its columns 1:1, in table column
  order. Nullable columns are pointer fields (`*string`, `*time.Time`,
  `*int64`); non-nullable columns are value fields.
- A `const xColumns = "col1, col2, ..."` package-level string per table,
  reused by every `SELECT`/`RETURNING` clause for that table (see
  `learnerCourseAccessColumns`, `courseColumns`).
- A `scanX(row pgx.Row) (*X, error)` helper for single-row reads
  (`QueryRow(...).Scan`), translating `pgx.ErrNoRows` into the shared
  `models.ErrNotFound` (defined in `internal/models/profile.go`, already
  package-visible — do not redeclare it). Any method that returns multiple
  rows also gets a `scanXRows(rows pgx.Rows) (*X, error)` helper called in a
  `for rows.Next()` loop, exactly like
  `scanLearnerCourseAccess`/`scanLearnerCourseAccessRows`.
- Every returned error is wrapped `fmt.Errorf("models: <action>: %w", err)`,
  e.g. `fmt.Errorf("models: create offer: %w", err)`,
  `fmt.Errorf("models: list orders by org: %w", err)`. Match the verb
  phrasing already used elsewhere (`"scan learner course access"`, `"list
  active learner ids by course"`).
- Doc comments on exported types/methods explain *why*, matching the style in
  `learner_course_access.go` (e.g. why a field is nullable, what invariant a
  method enforces) — not just restating the signature.
- Money columns are `NUMERIC(12,2)` in Postgres (Task 1's actual schema —
  an exact decimal type, not floating point, so there is no precision-loss
  concern the way there would be with a raw IEEE float). Map them to
  `float64` in Go, matching how this codebase already handles the other
  `NUMERIC` columns it has (e.g. `grade_percentage NUMERIC(5,2)` in Task 5).
  Every amount field name ends in `Amount` (e.g. `TotalAmount float64`) and
  its doc comment states the currency comes from the row's own
  `Currency`/`currency` column. Do NOT introduce an integer-minor-units
  (paise/cents) convention — that would diverge from the column type Task 1
  actually created. All rounding to 2 decimal places happens where the
  amount is computed (the checkout handler, in a later Task 6 subtask),
  before it's ever passed into these repos.
- The table/column list below is copied exactly from Task 1's actual
  migration (`db/migrations/000006_commerce.up.sql`, specified in
  `plans/task-6-implementation/task-1-db-migration.md`) — Task 1 is the
  source of truth. If the merged migration differs from what's listed here
  (e.g. a column was renamed during Task 1's implementation), this task's
  repos must be adjusted to match the actual migration, not this document.
- None of these repos perform authorization checks (role/permission
  enforcement is `internal/auth`'s `Can()` + `middleware.RequireRole`/
  `RequirePlatformOwner`, applied by the handler layer in later Task 6
  subtasks) or emit audit events themselves except where explicitly noted
  (`PaymentAuditRepo`, and `AuditRepo` calls the handlers make separately for
  `audit_events`). Keep repos to data access only.

Task 1's actual schema (org-scoped tables carry `org_id NOT NULL` with RLS
exactly like every other Task 3+ table; `webhook_events` and
`platform_settings` are the two exceptions, explained under those repos
below):

- `offers`: `id, org_id, course_id, type, price, currency,
  tax_rate_percent, access_duration_days, max_seats, enrollment_starts_at,
  enrollment_ends_at, status, created_by, created_at, updated_at`. `type`
  CHECK IN (`free`,`paid`,`subscription`,`cohort`,`invitation_only`).
  `status` CHECK IN (`active`,`archived`). `price` is `0` for `free` offers.
  `access_duration_days` is non-NULL only for `subscription` (fixed-term
  pass) offers. `max_seats`/`enrollment_starts_at`/`enrollment_ends_at` are
  non-NULL only for `cohort` offers. No `title`/`description` columns —
  an offer's display copy comes from its `courses` row via `course_id`.
- `discount_codes`: `id, org_id, offer_id, code, discount_type, value,
  expires_at, max_redemptions, redemption_count, created_by, created_at`.
  `discount_type` CHECK IN (`percent`,`fixed`). No `status`/`updated_at`
  columns — a code is deactivated by setting `expires_at` to a past
  timestamp (the handler layer's job), not a separate status flag. Unique
  per `(offer_id, code)`.
- `commerce_invite_tokens`: `id, org_id, offer_id, token, bound_email,
  used_at, used_by_learner_id, used_by_order_id, expires_at, created_by,
  created_at`. `token` is a unique, unguessable string (generation is this
  repo's caller's responsibility, not this repo's). `bound_email` is
  nullable; when set, only that email may redeem the token.
- `orders`: `id, org_id, offer_id, learner_id, currency, subtotal,
  discount_amount, tax_amount, commission_amount, total,
  commission_rate_snapshot, status, razorpay_order_id, discount_code_id,
  created_at, updated_at`. `status` CHECK IN (`pending`,
  `payment_initiated`,`succeeded`,`failed`,`abandoned`) — exactly these five;
  there is no `refunded`/`disputed` order status. A refund or chargeback
  does NOT change `orders.status` (an order that was `succeeded` stays
  `succeeded` even after a later refund) — the refund/chargeback's own
  effect on revenue is tracked via the `refunds`/`chargebacks` rows
  themselves and rolled up at query time by the reporting code, not by
  mutating the order. No `invite_token_id` column — an invitation-only
  offer's gating is enforced entirely by `commerce_invite_tokens.used_at`
  before order creation is even allowed; the order itself doesn't need a
  back-reference. `commission_rate_snapshot` is the
  `platform_settings.commission_percent` value at order-creation time (Task
  6 main-plan decision: commission is snapshotted per order, not looked up
  live later).
- `payments`: `id, org_id, order_id, razorpay_payment_id, status,
  raw_provider_data, created_at, updated_at`. `status` CHECK IN
  (`pending`,`processing`,`succeeded`,`failed`). `raw_provider_data` is
  nullable `JSONB` holding the raw Razorpay payment payload for audit/
  debugging. No separate `amount`/`currency`/`method`/`failure_reason`
  columns — the payment's amount/currency are the same as its order's
  `total`/`currency` (join to `orders` if needed); a failure reason, if any,
  lives inside `raw_provider_data`.
- `refunds`: `id, org_id, payment_id, razorpay_refund_id, status, amount,
  reason, created_at, updated_at`. `status` CHECK IN
  (`pending`,`succeeded`,`failed`). No `order_id` column — join through
  `payments.order_id` if the order is needed.
- `chargebacks`: `id, org_id, payment_id, status, amount, reason,
  created_at, updated_at`. `status` CHECK IN (`pending`,`won`,`lost`) — note
  `pending`, not `open`. No `razorpay_dispute_id`/`order_id` columns; if the
  Razorpay dispute ID is needed for reconciliation, it goes in `reason` or a
  future column — do not invent one here since Task 1 didn't create it.
- `entitlements`: `id, org_id, order_id, learner_id, course_id, status,
  expires_at, granted_by, grant_reason, created_at, updated_at`. `order_id`
  is NULL for admin-grant rows (non-NULL for purchase-driven rows).
  `granted_by`/`grant_reason` are NULL for purchase-driven rows and required
  (enforced by the handler, not this repo) for admin-grant rows — whether a
  row came from a webhook or an admin grant is therefore derived from
  `granted_by IS NULL` rather than a separate `source` enum column (Task 1
  has no `source` column). `status` CHECK IN
  (`active`,`revoked`,`expired`). `expires_at` is non-NULL only for
  fixed-term (`subscription`-offer-sourced) entitlements. No `offer_id`
  column (derivable via `order_id → orders.offer_id` when there is an
  order) and no `revoked_at` column (use `updated_at` as the revocation
  timestamp when `status = 'revoked'`). This is the table
  `internal/models.LearnerCourseAccess.EntitlementID` (Task 5) points at
  once real commerce exists — creating a row here does not by itself grant
  the learner course access; the handler/webhook-processing code (Task 6
  later subtasks) is responsible for also creating/updating the
  corresponding `learner_course_access` row via
  `LearnerCourseAccessRepo.Create`, in the same transaction.
- `payment_audit_trail`: `id, org_id, created_at, event_type, order_id,
  payment_id, old_state, new_state, reason, user_id`. Append-only, no update
  or delete methods — mirrors `audit_events`'s shape and purpose but scoped
  to payment/order/refund/chargeback state transitions specifically (finer
  grained than a generic `audit_events` row).
- `webhook_events`: `id, razorpay_event_id, event_type, payload,
  processed_at, created_at`. **No `org_id`, no RLS.** The org isn't
  reliably known until the payload is parsed and the referenced order is
  looked up, and idempotency must be checked before any org context exists.
  This table is accessed with the worker/pool `Querier`, not a
  request-scoped RLS transaction — note this explicitly in the file's doc
  comment so a future engineer doesn't assume every `models` table is
  org-scoped. `payload` is `NOT NULL JSONB`, the raw webhook body.
- `platform_settings`: `id, commission_percent, updated_by, updated_at`.
  Platform-wide, not org-scoped — no RLS, single logical row (Task 1 seeds
  exactly one row; this repo does not create additional rows). Enforcement
  that only the platform owner can call `Update` is the caller's job
  (`middleware.RequirePlatformOwner`, per
  `plans/task-6-implementation/task-3-permissions-matrix.md`), not this
  repo's.

Group the eleven tables into files the same way Task 5 grouped closely
related learner-journey tables (not strictly one file per table):

### `internal/models/offer.go` — `OfferRepo`

```go
type Offer struct {
	ID                  string
	OrgID               string
	CourseID            string
	Type                string
	Price               float64 // NUMERIC(12,2); 0 for "free" offers
	Currency            string
	TaxRatePercent      float64 // NUMERIC(5,2); a rate, never a money amount
	AccessDurationDays  *int    // non-NULL only for "subscription" (fixed-term pass) offers
	MaxSeats            *int    // non-NULL only for "cohort" offers
	EnrollmentStartsAt  *time.Time // non-NULL only for "cohort" offers
	EnrollmentEndsAt    *time.Time // non-NULL only for "cohort" offers
	Status              string
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}
```

Note: `Offer` has no `Title`/`Description` field — Task 1's schema has no
such columns; display copy comes from the offer's course (join `CourseID`
to `courses` when rendering).

- `Create(ctx, q, o Offer) (*Offer, error)` — inserts using every field
  above except `ID`/`CreatedAt`/`UpdatedAt` (DB-generated/defaulted);
  returns the full row via `RETURNING`.
- `Get(ctx, q, id string) (*Offer, error)`.
- `ListByCourse(ctx, q, courseID string) ([]*Offer, error)` — all offers
  (any status) for a course, ordered by `created_at`; the caller (checkout
  handler) filters by `status`/enrollment window as needed.
- `Update(ctx, q, id string, o Offer) (*Offer, error)` — updates the mutable
  pricing/tax/availability fields (`Price`, `Currency`, `TaxRatePercent`,
  `AccessDurationDays`, `MaxSeats`, `EnrollmentStartsAt`,
  `EnrollmentEndsAt`); does not touch `Status`.
- `Archive(ctx, q, id string) (*Offer, error)` — sets `status = 'archived'`.
  An archived offer is excluded from new checkouts by the handler layer
  (this repo does not enforce that; `ListByCourse` still returns archived
  rows so existing purchasers/admin views can see them).

### `internal/models/discount_code.go` — `DiscountCodeRepo`

```go
type DiscountCode struct {
	ID               string
	OrgID            string
	OfferID          string
	Code             string
	DiscountType     string  // "percent" or "fixed"
	Value            float64 // NUMERIC(12,2); a money amount if DiscountType == "fixed", a percent (0-100) if "percent"
	ExpiresAt        *time.Time
	MaxRedemptions   *int
	RedemptionCount  int
	CreatedBy        string
	CreatedAt        time.Time
}
```

Note: `DiscountCode` has no `Status`/`UpdatedAt` field — Task 1's schema has
no such columns. A code is deactivated by the handler layer setting
`ExpiresAt` to a past timestamp via a plain `Update`-style query (add one if
needed at implementation time); this repo does not need a separate
`Deactivate` method beyond that unless a later task asks for one.

- `Create(ctx, q, d DiscountCode) (*DiscountCode, error)`.
- `GetByCode(ctx, q, offerID, code string) (*DiscountCode, error)` — scoped
  to a single offer (`WHERE offer_id = $1 AND code = $2`); a code string is
  only unique within its offer, so callers must always know which offer
  they're checking out. Returns `ErrNotFound` if no row matches. Does not by
  itself validate `expires_at`/`redemption_count` — the checkout handler
  decides what to do with an expired/exhausted code it got back (e.g. reject
  with a specific user-facing message), since those are presentation
  decisions, not existence.
- `IncrementRedemption(ctx, q, id string) (*DiscountCode, error)` — atomic
  check-and-increment to close the race between two concurrent checkouts
  both reading `redemption_count < max_redemptions` as true and both
  incrementing. Implemented as a single statement:
  ```sql
  UPDATE discount_codes
  SET redemption_count = redemption_count + 1
  WHERE id = $1 AND (max_redemptions IS NULL OR redemption_count < max_redemptions)
  RETURNING <discountCodeColumns>
  ```
  (`max_redemptions IS NULL` means unlimited redemptions — without this
  clause a NULL cap would make the comparison evaluate to unknown/false and
  incorrectly block every redemption of an uncapped code) executed via
  `q.QueryRow(...).Scan(...)`. If `pgx.ErrNoRows` comes back,
  return the sentinel `ErrDiscountCodeExhausted` (defined in this file,
  `errors.New("models: discount code redemption cap reached")`) rather than
  `ErrNotFound` — callers are expected to have already resolved the code via
  `GetByCode` earlier in the same request, so a miss here specifically means
  the cap was hit (or the row vanished mid-request, which collapses to the
  same "can't apply this code" outcome for the caller). Call this only after
  the payment is confirmed (inside the same webhook-processing transaction
  that creates the `payments`/`entitlements` rows), never at checkout-order
  creation time, so an abandoned/failed order never permanently burns a
  redemption.

### `internal/models/commerce_invite_token.go` — `InviteTokenRepo`

```go
type InviteToken struct {
	ID              string
	OrgID           string
	OfferID         string
	Token           string
	BoundEmail      *string // nullable; when set, only this email may redeem the token
	UsedAt          *time.Time
	UsedByLearnerID *string
	UsedByOrderID   *string
	ExpiresAt       *time.Time
	CreatedBy       string
	CreatedAt       time.Time
}

var (
	ErrInviteTokenUsed    = errors.New("models: invite token already used")
	ErrInviteTokenExpired = errors.New("models: invite token expired")
)
```

- `Create(ctx, q, orgID, offerID, createdBy, token string, boundEmail *string, expiresAt *time.Time) (*InviteToken, error)`
  — token string generation (random, unguessable) is the caller's
  responsibility; this method just persists it.
- `GetByToken(ctx, q, token string) (*InviteToken, error)` — looks up the
  row by `token`, returns `ErrNotFound` if no row matches, then validates in
  Go (not SQL) and returns the appropriate sentinel instead of the row when
  invalid: `ErrInviteTokenUsed` if `used_at IS NOT NULL`,
  `ErrInviteTokenExpired` if `expires_at` is non-nil and in the past
  (compare against `time.Now()`). Returns `(*InviteToken, nil)` only when
  the token exists, is unused, and is unexpired — callers can treat a
  non-nil, non-error return as "this token is currently redeemable" without
  re-checking the fields themselves. Does NOT check `bound_email` — that
  requires the authenticated learner's email, which this repo has no access
  to; the checkout handler compares `BoundEmail` (if non-nil) against the
  session's email itself.
- `MarkUsed(ctx, q, id, learnerID, orderID string) (*InviteToken, error)` —
  sets `used_at = now(), used_by_learner_id = $2, used_by_order_id = $3`.
  Call only after the order this token gates has succeeded (same
  webhook-processing transaction as entitlement creation), not at checkout
  start, mirroring `DiscountCodeRepo.IncrementRedemption`'s timing rule.

### `internal/models/order.go` — `OrderRepo`

```go
type Order struct {
	ID                      string
	OrgID                   string
	OfferID                 string
	LearnerID               string
	Currency                string
	Subtotal                float64
	DiscountAmount          float64
	TaxAmount               float64
	CommissionAmount        float64
	Total                   float64
	CommissionRateSnapshot  float64
	Status                  string
	RazorpayOrderID         *string
	DiscountCodeID          *string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}
```

Order status constants, matching the CHECK constraint — exactly these five,
no `refunded`/`disputed` (a refund/chargeback never changes `orders.status`;
see the schema note above):
```go
const (
	OrderStatusPending          = "pending"
	OrderStatusPaymentInitiated = "payment_initiated"
	OrderStatusSucceeded        = "succeeded"
	OrderStatusFailed           = "failed"
	OrderStatusAbandoned        = "abandoned"
)
```

- `Create(ctx, q, o Order) (*Order, error)` — inserts with `status =
  'pending'` regardless of any `Status` field the caller set (force it
  server-side in the SQL, don't trust the struct field on insert) and every
  amount/currency/commission field taken from server-side computation the
  handler already performed (offer price + tax rate, discount code lookup,
  current `platform_settings.commission_percent`) — this repo does not
  itself compute pricing, it only persists numbers the caller computed and
  never reads amounts from client-controlled request bodies. Doc comment on
  this method must restate the "never trust client input for amount/
  currency" rule from `CLAUDE.md` so it isn't lost if this file is edited
  later.
- `Get(ctx, q, id string) (*Order, error)`.
- `UpdateStatus(ctx, q, id, status string) (*Order, error)` — sets `status =
  $2, updated_at = now()`. No `succeeded_at` column exists (Task 1's
  schema tracks the success moment via `updated_at` at the point `status`
  becomes `succeeded`, not a dedicated timestamp column). No state-machine
  validation here (like `CourseRepo`, the valid-transition graph lives in
  the handler/webhook processing code, not the repo).
- `ListPendingOlderThan(ctx, q, cutoff time.Duration) ([]*Order, error)` —
  `SELECT ... WHERE status = 'pending' AND created_at < now() - $1::interval`
  (pass the Go `time.Duration` as a Postgres interval string, e.g.
  `fmt.Sprintf("%d seconds", int(cutoff.Seconds()))`, or bind it as a
  `time.Time` cutoff computed in Go — either works, pick the simpler one and
  be consistent). Used by the abandon-sweep worker job (Task 8) to find
  orders to flip to `abandoned`.
- `ListByOrg(ctx, q, orgID string, from, to time.Time) ([]*Order, error)` —
  `WHERE org_id = $1 AND created_at >= $2 AND created_at < $3 ORDER BY
  created_at DESC`, for the admin dashboard/revenue reporting (Task 9).

### `internal/models/payment.go` — `PaymentRepo`

```go
type Payment struct {
	ID                string
	OrgID             string
	OrderID           string
	RazorpayPaymentID *string
	Status            string
	RawProviderData   []byte // nullable JSONB; the raw Razorpay payment payload, for audit/debugging
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

const (
	PaymentStatusPending    = "pending"
	PaymentStatusProcessing = "processing"
	PaymentStatusSucceeded  = "succeeded"
	PaymentStatusFailed     = "failed"
)
```

Note: `Payment` has no `Amount`/`Currency`/`Method`/`FailureReason` fields —
Task 1's schema has no such columns. The payment's amount/currency are the
order's (`OrderRepo.Get(orderID).Total`/`.Currency`); a failure reason, if
Razorpay provides one, lives inside `RawProviderData`.

- `Create(ctx, q, p Payment) (*Payment, error)`.
- `UpdateStatus(ctx, q, id, status string, razorpayPaymentID *string, rawProviderData []byte) (*Payment, error)`
  — sets `status`, `updated_at = now()`, and conditionally
  `razorpay_payment_id`/`raw_provider_data` when the corresponding
  argument is non-nil (use `COALESCE($3, razorpay_payment_id)` style SQL so
  passing `nil` leaves the existing value untouched rather than nulling it
  out).
- `GetByOrderID(ctx, q, orderID string) (*Payment, error)` — an order has at
  most one payment attempt that matters for MVP (retries create a new
  `payments` row rather than reusing one); if multiple rows exist, return
  the most recently created (`ORDER BY created_at DESC LIMIT 1`).

### `internal/models/refund_chargeback.go` — `RefundRepo`, `ChargebackRepo`

```go
type Refund struct {
	ID               string
	OrgID            string
	PaymentID        string
	RazorpayRefundID *string
	Status           string
	Amount           float64
	Reason           *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const (
	RefundStatusPending   = "pending"
	RefundStatusSucceeded = "succeeded"
	RefundStatusFailed    = "failed"
)

type Chargeback struct {
	ID        string
	OrgID     string
	PaymentID string
	Status    string
	Amount    float64
	Reason    *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const (
	ChargebackStatusPending = "pending"
	ChargebackStatusWon     = "won"
	ChargebackStatusLost    = "lost"
)
```

Note: neither struct has an `OrderID` field — Task 1's schema doesn't
denormalize it onto these two tables; join through `PaymentID → payments →
orders` if the order is needed. `Chargeback` also has no
`RazorpayDisputeID` column; if that ID needs to be tracked, put it in
`Reason` for MVP rather than inventing a column Task 1 didn't create.

- `RefundRepo.Create(ctx, q, r Refund) (*Refund, error)` — inserted with
  `status = 'pending'` server-side (same rule as `OrderRepo.Create`), when
  the in-app "Refund" action calls the Razorpay Refund API; the row's
  `status` only moves to `succeeded`/`failed` once the refund webhook is
  verified and processed.
- `RefundRepo.UpdateStatus(ctx, q, id, status string, razorpayRefundID *string) (*Refund, error)`.
- `RefundRepo.GetByPaymentID(ctx, q, paymentID string) ([]*Refund, error)` —
  a payment can have more than one refund attempt (e.g. a failed retry), so
  this returns a slice, most recent first.
- `ChargebackRepo.Create(ctx, q, c Chargeback) (*Chargeback, error)` —
  always created from a verified dispute-opened webhook, never from any
  in-app action (there is no "initiate a chargeback" button); inserted with
  `status = 'pending'`.
- `ChargebackRepo.UpdateStatus(ctx, q, id, status string) (*Chargeback, error)`.

### `internal/models/entitlement.go` — `EntitlementRepo`

```go
type Entitlement struct {
	ID          string
	OrgID       string
	OrderID     *string // NULL for admin grants, set for purchase-driven entitlements
	LearnerID   string
	CourseID    string
	Status      string
	ExpiresAt   *time.Time
	GrantedBy   *string // NULL for purchase-driven entitlements, set (granting user) for admin grants
	GrantReason *string // required (handler-enforced) when GrantedBy is set
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const (
	EntitlementStatusActive  = "active"
	EntitlementStatusRevoked = "revoked"
	EntitlementStatusExpired = "expired"
)
```

Note: `Entitlement` has no `OfferID`, `Source`, or `RevokedAt` field — Task
1's schema has no such columns. Whether a row is purchase-driven or an
admin grant is derived from `GrantedBy == nil` (purchase-driven) vs.
`GrantedBy != nil` (admin grant), not a separate enum; the offer, if
needed, is derivable via `OrderID → orders.offer_id` when `OrderID` is
set; the revocation timestamp is `UpdatedAt` at the moment `Status` becomes
`revoked`.

- `Create(ctx, q, e Entitlement) (*Entitlement, error)` — the only two
  legitimate call sites are (a) verified-webhook payment-success processing
  (`OrderID` set, `GrantedBy`/`GrantReason` nil) and (b) an admin-grant
  handler (`OrderID` nil, `GrantedBy` set to the granting user's ID,
  `GrantReason` required non-empty — the handler validates
  `GrantReason != ""` before calling this, this repo does not re-validate
  it). Doc comment must state plainly: never call this from a
  browser-return-URL handler, only from verified-webhook processing or an
  explicit audit-logged admin action, per `CLAUDE.md`'s non-negotiable rule.
- `Get(ctx, q, id string) (*Entitlement, error)`.
- `ListByLearner(ctx, q, learnerID string) ([]*Entitlement, error)` —
  ordered `created_at DESC`.
- `Revoke(ctx, q, id string) (*Entitlement, error)` — sets `status =
  'revoked', updated_at = now()`. Called on a verified refund/chargeback
  webhook, or an equivalent admin action; does not itself touch
  `learner_course_access` — the caller updates that row too, in the same
  transaction, mirroring the relationship documented on the `entitlements`
  table above.
- `ListExpiringBefore(ctx, q, cutoff time.Time) ([]*Entitlement, error)` —
  `WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at < $1`,
  used by the expiry-sweep worker job (Task 8) to find fixed-term passes
  that need to flip to `expired`.

### `internal/models/payment_audit.go` — `PaymentAuditRepo`

Mirrors `internal/models/audit.go`'s `AuditRepo.Record` closely — read that
file and match its shape exactly, adapted to `payment_audit_trail`'s
narrower, payment-specific columns.

```go
// PaymentAuditEvent is a single payment/order state transition to record.
// OrderID/PaymentID/UserID are nullable because not every transition has
// all three (e.g. a chargeback-opened event may have no acting user).
type PaymentAuditEvent struct {
	OrgID     string
	EventType string
	OrderID   *string
	PaymentID *string
	OldState  *string
	NewState  string
	Reason    *string
	UserID    *string
}

type PaymentAuditRepo struct{}

func NewPaymentAuditRepo() *PaymentAuditRepo { return &PaymentAuditRepo{} }
```

- `Record(ctx context.Context, q Querier, e PaymentAuditEvent) error` —
  inserts a `payment_audit_trail` row. Callers pass the same `Querier`
  (typically `dbctx.RequestTx`, or the worker's transaction when called from
  webhook processing) used for the state-changing mutation being audited —
  same "insert inside the same transaction as the mutation" rule as
  `AuditRepo.Record`, and for the same reason: a failure to log should roll
  back the mutation too. No `Get`/`List` methods on this repo for MVP (the
  admin dashboard's "support and dispute investigation" need, per
  `plans/lms-mvp/task-6-commerce.md`, is out of scope for Task 4 — Task 9
  can add a read method against this same table later if it needs one;
  don't speculatively add it here).

### `internal/models/webhook_event.go` — `WebhookEventRepo`

```go
// Package-level doc note (add near the top of this file, not a repeated
// package comment): webhook_events has no org_id and is not RLS-protected.
// The organization a webhook event belongs to isn't knowable until its
// payload is parsed and the referenced order is looked up — TryRecord must
// run before any org context exists, so callers pass the worker/pool
// Querier here, not a request-scoped RLS transaction.

type WebhookEvent struct {
	ID              string
	RazorpayEventID string
	EventType       string
	Payload         []byte // NOT NULL JSONB; raw webhook body, for reprocessing/debugging
	ProcessedAt     *time.Time
	CreatedAt       time.Time
}

type WebhookEventRepo struct{}

func NewWebhookEventRepo() *WebhookEventRepo { return &WebhookEventRepo{} }
```

- `TryRecord(ctx context.Context, q Querier, razorpayEventID, eventType string, payload []byte) (isNew bool, err error)`
  — the idempotency gate the webhook HTTP handler calls before enqueueing
  any processing job:
  ```sql
  INSERT INTO webhook_events (razorpay_event_id, event_type, payload)
  VALUES ($1, $2, $3)
  ON CONFLICT (razorpay_event_id) DO NOTHING
  ```
  executed via `q.Exec(...)`. `isNew` is `true` iff the command tag's rows
  affected is `1` (via `pgconn.CommandTag.RowsAffected()`); `false` means
  this event ID was already recorded (a Razorpay retry) and the handler
  must skip enqueueing. Returns a wrapped error only on an actual query
  failure, never for the "already exists" case (that's the normal `false`
  path, not an error).
- `MarkProcessed(ctx context.Context, q Querier, razorpayEventID string) error`
  — sets `processed_at = now() WHERE razorpay_event_id = $1`, called by the
  asynq job handler (Task 8) after it finishes processing the event, so a
  future support query can distinguish "recorded but never finished
  processing" (e.g. the job crashed) from "recorded and processed".

### `internal/models/platform_settings.go` — `PlatformSettingsRepo`

```go
type PlatformSettings struct {
	ID                string
	CommissionPercent float64
	UpdatedBy         *string
	UpdatedAt         time.Time
}

type PlatformSettingsRepo struct{}

func NewPlatformSettingsRepo() *PlatformSettingsRepo { return &PlatformSettingsRepo{} }
```

- `Get(ctx context.Context, q Querier) (*PlatformSettings, error)` —
  `SELECT ... FROM platform_settings LIMIT 1` (Task 1 seeds exactly one
  row; this repo never creates one — if the table is empty, that's a
  migration/seed bug and this surfaces as `ErrNotFound`, which is the
  correct failure mode, not a silent default). This is the single source
  `OrderRepo.Create`'s caller reads `commission_percent` from before
  snapshotting it onto a new order.
- `Update(ctx context.Context, q Querier, id string, commissionPercent float64, updatedBy string) (*PlatformSettings, error)`
  — sets `commission_percent = $2, updated_by = $3, updated_at = now()
  WHERE id = $1`. This repo does not check who `updatedBy` is or whether
  they're the platform owner — that's `middleware.RequirePlatformOwner`'s
  job at the route layer, per
  `plans/task-6-implementation/task-3-permissions-matrix.md`'s explicit
  "out of scope for `permissionMatrix`/`Can()`" note. Existing orders keep
  their already-snapshotted `commission_percent_snapshot` — this update
  only affects orders created after it.

## Acceptance criteria

- [ ] `internal/models/offer.go` implements `OfferRepo` with `Create`,
      `Get`, `ListByCourse`, `Update`, `Archive` exactly as specified above.
- [ ] `internal/models/discount_code.go` implements `DiscountCodeRepo` with
      `Create`, `GetByCode`, `IncrementRedemption`; `IncrementRedemption` is
      a single atomic `UPDATE ... WHERE redemption_count < max_redemptions
      RETURNING ...` statement (verified by reading the SQL, not just the
      method signature) and returns `ErrDiscountCodeExhausted` rather than
      silently succeeding or double-incrementing when at cap.
- [ ] `internal/models/commerce_invite_token.go` implements `InviteTokenRepo`
      with `Create`, `GetByToken` (returns `ErrInviteTokenUsed`/
      `ErrInviteTokenExpired` as distinct sentinels from `ErrNotFound`),
      `MarkUsed`.
- [ ] `internal/models/order.go` implements `OrderRepo` with `Create` (never
      trusts a caller-supplied `Status`, always inserts `pending`; doc
      comment states amounts must be server-computed), `Get`, `UpdateStatus`,
      `ListPendingOlderThan`, `ListByOrg` with a date-range filter.
- [ ] `internal/models/payment.go` implements `PaymentRepo` with `Create`,
      `UpdateStatus`, `GetByOrderID`.
- [ ] `internal/models/refund_chargeback.go` implements `RefundRepo`
      (`Create`, `UpdateStatus`, `GetByPaymentID`) and `ChargebackRepo`
      (`Create`, `UpdateStatus`) in one file.
- [ ] `internal/models/entitlement.go` implements `EntitlementRepo` with
      `Create`, `Get`, `ListByLearner`, `Revoke`, `ListExpiringBefore`; the
      doc comment on `Create` states entitlements are only ever created from
      verified-webhook processing or an explicit admin grant, never from a
      browser-return-URL handler.
- [ ] `internal/models/payment_audit.go` implements `PaymentAuditRepo` with
      a single `Record` method, modeled closely on `AuditRepo.Record` in
      `internal/models/audit.go` (append-only, same-transaction insert, no
      `Get`/`List`/`Update`/`Delete` methods).
- [ ] `internal/models/webhook_event.go` implements `WebhookEventRepo` with
      `TryRecord` (INSERT ... ON CONFLICT DO NOTHING, `isNew` derived from
      rows-affected, not a pre-check-then-insert) and `MarkProcessed`; file
      contains a doc comment explaining why this table has no `org_id`/RLS,
      unlike every other Task 6 table.
- [ ] `internal/models/platform_settings.go` implements
      `PlatformSettingsRepo` with `Get` and `Update`; no authorization logic
      inside the repo.
- [ ] Every repo method's first two parameters are `(ctx context.Context, q
      Querier, ...)` — no method anywhere in these new files accepts a
      concrete `*pgxpool.Pool` or `pgx.Tx` type directly (grep confirms no
      `pgxpool.Pool` or `pgx.Tx` in any new file's function signatures
      outside of `querier.go` itself).
- [ ] Every `Get`/`GetBy*` method returns the shared `models.ErrNotFound` on
      no rows (via `errors.Is(err, pgx.ErrNoRows)` in its scan helper), not
      a bare `pgx.ErrNoRows` or a newly-invented not-found error.
- [ ] Every non-nullable struct field maps to a `NOT NULL` column and every
      pointer field maps to a nullable column, matching whatever Task 1
      actually created (adjust field types here if Task 1's migration
      differs from the column list assumed above).
- [ ] All money fields are `float64`, mapping directly to Task 1's
      `NUMERIC(12,2)` columns (consistent with this codebase's existing
      `NUMERIC` handling elsewhere, e.g. `grade_percentage`) — no
      integer-minor-units convention is introduced anywhere in these files.
- [ ] `go build ./...` and `go vet ./...` pass with the new files in place.
- [ ] No handler, middleware, route, or authorization logic is added in this
      task — `internal/models` only. No `internal/payments` provider client
      code (that's Task 5, `razorpay-client`) is added either, even though
      these repos exist to support it.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
