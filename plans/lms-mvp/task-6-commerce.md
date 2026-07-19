---
task: 6
name: commerce
parallel_group: 5
depends_on: [3, 4]
issue: TBD
---

# Task 6: Commerce, payments, entitlements, and admin dashboard

## What to build

Implement the MVP commerce and payment system for the LMS, enabling course monetization, secure payment processing, and access entitlements — plus the basic admin dashboard promised in Task 1's product brief and feature matrix, since this task is where all the underlying data (organizations, courses, enrollments, payments) first fully exists together. This task does NOT implement the course player, progress tracking, or certificates (Task 5's responsibility).

### Offer system

Implement course offers with five types: free, paid (one-time), subscription, cohort-based, and invitation-only. Each offer is tied to a course (from Task 4) and includes:
- Pricing and currency fields (supports multi-currency, but MVP can start with a single currency)
- Tax configuration (tax rate, tax breakdown in receipts)
- Discount code and coupon support (percentage or fixed-amount discounts, expiry windows)
- Availability windows (start/end dates for when an offer is available for purchase)
- All offer records are organization-scoped with row-level security (RLS) following the pattern established in Task 3

### Checkout and order management

- Server-side order creation: the checkout order amount and currency are ALWAYS computed and validated server-side, never trusted from client input
- Orders reference an offer and contain computed price, applied discounts, tax, and total amount
- Order state machine: pending → (payment_initiated/failed/abandoned) → (succeeded/refunded/disputed)
- All order records are organization-scoped with RLS

### Payment provider adapter

Implement a payment-provider adapter interface that abstracts payment processing, enabling future providers (Stripe, etc.) without rewriting commerce logic.

**Razorpay implementation (MVP's sole concrete provider):**
- Razorpay API integration for creating orders and verifying payments
- Razorpay webhook signature verification: all webhook payloads MUST have their cryptographic signature validated before any processing occurs; tampered or unsigned payloads are rejected
- Payment API keys and webhook secrets are server-side only (see Task 2's config/secrets setup); they never reach the browser or appear in repository commits
- Use environment variables or secure config store for credentials; never hardcode or commit secrets

### Webhook processing and idempotency

Webhook events from Razorpay are processed via the Redis-backed job queue (per Task 2's async architecture):
- Each webhook event is deduplicated by its Razorpay event ID before processing
- Duplicate deliveries (Razorpay retries failed webhook attempts) must not create duplicate enrollment or revenue records
- Webhook processing is idempotent: applying the same event twice produces the same result as applying it once
- Failed webhook processing is logged and retried via the job queue with exponential backoff

### Payment state tracking

Implement distinct payment states and transitions:
- **Purchase record**: represents a learner's purchase intent, linked to an order and course offer
- **Payment record**: tracks individual payment attempts (pending, processing, succeeded, failed)
- **Refund record**: tracks refund requests and their states (pending, succeeded, failed)
- **Chargeback record**: tracks payment disputes and chargebacks
- **Entitlement record**: grants a learner access to a course; created ONLY after:
  - A payment webhook confirms successful payment (signature-verified), OR
  - An explicit admin grant via the API (audit-logged per Task 3's audit_events table)
  - An entitlement must NEVER be created from a browser redirect/return URL alone

All records include explicit state machines (e.g., pending → succeeded → refunded) with timestamp tracking for each state transition.

### Enrollment and access control

- A learner gains access to a course ONLY when an entitlement record exists for that course
- The entitlement is created automatically after a verified Razorpay webhook confirms payment success, OR via explicit admin action
- The browser checkout return URL is NOT a trusted signal of payment success; it is a client-side notification only
- Hitting the return URL cannot grant access — the only source of truth is the verified webhook event
- When a refund or chargeback is processed (via webhook), the associated entitlement is revoked (explicit state change)
- All access-granting and access-revoking actions are audit-logged per Task 3's audit_events table

### Platform monetization: commission on course sales

The platform's own revenue mechanism for MVP is a commission taken on each course sale — there is no separate organization subscription/billing system.

- Every successful order carries a platform commission (a configurable percentage or fixed fee, set per-platform not per-org for MVP) computed server-side alongside tax/discounts
- The commission amount is recorded on the order/payment record, separate from the organization's net revenue, so both parties' amounts are auditable
- Creator/organization revenue reporting (below) reflects net-of-commission amounts; the platform-owner view (below) reflects commission collected across all organizations
- Refunds and chargebacks reduce both the organization's net revenue and the platform's commission proportionally
- How commission is actually settled/transferred to organizations (e.g. Razorpay Route/sub-accounts vs. manual payout) is an implementation detail for whoever builds this task — the requirement is that commission is computed, recorded, and reflected in reporting; a real payout mechanism can be a manual/batch process for MVP if a real-time split isn't feasible

### Revenue and reporting

- Creator/teacher revenue reporting: basic reports showing total (net-of-commission) revenue per course and per offer
- Reports include filters for date ranges and offer types
- Revenue is only counted after verified payment confirmation (webhook processed successfully)
- Revenue is adjusted (decremented) when refunds or chargebacks are applied
- Reports are accessible to the creator/organization owner, scoped by organization RLS

### Basic admin dashboard

A minimal admin dashboard fulfilling Task 1's MVP promise, built now because it needs data from every prior task (organizations and roles from Task 3, courses from Task 4, enrollments/payments from this task):

- **Organization-scoped admin view** (visible to organization owners): user list (members of the org with their roles), course list (with publish status), enrollment overview (counts per course, revenue per course from the reporting above)
- **Platform-owner view** (visible only to `profiles.is_platform_owner`, per Task 3): a cross-organization list of all organizations with basic health signals (member count, course count, enrollment count, commission revenue generated), and the ability to view (not edit) any organization's basic details for support purposes
- Both views are read-only reporting screens for MVP — no bulk admin actions, feature flags, or quota management (that's the full Task 10 admin console, out of scope here)
- Enforced via Task 3's permission middleware and RLS: an organization owner sees only their own org's data; only the platform-owner flag unlocks the cross-org view

### Payment audit trail

Implement an append-only audit trail of all payment-related state transitions (created_at, event_type, order_id, payment_id, old_state, new_state, reason, user_id). This is used for:
- Support and dispute investigation
- Compliance and financial reconciliation
- Tracing the origin of refunds and chargebacks

### Required automated tests

These tests are mandatory (not deferred to later hardening):

1. **Webhook signature verification**: Verify that webhook payloads with invalid or missing signatures are rejected and not processed
2. **Duplicate webhook idempotency**: Verify that delivering the same webhook event twice (with identical event ID) results in only one enrollment/revenue record, not duplicates
3. **No access without verified payment**: Verify that hitting the browser checkout return URL alone does not grant course access; access is only granted after a verified webhook event
4. **Payment state transitions**: Verify refunds and chargebacks correctly update entitlement and revenue records
5. **Payment secrets security**: Verify that payment API keys and webhook secrets never appear in HTTP responses or are logged in plaintext

## Acceptance criteria

- [ ] Browser return/redirect URLs cannot grant course access — entitlements are created only after a verified Razorpay webhook signature is validated or via explicit admin grant (audit-logged)
- [ ] Duplicate webhook deliveries are deduplicated by Razorpay event ID and do not create duplicate enrollment or revenue records — proven by automated test
- [ ] Webhook signature verification rejects tampered or unsigned payloads — automated test confirms invalid signatures are rejected
- [ ] Failed, refunded, and disputed payments have explicit distinct states; entitlements and revenue records update correctly in response to refunds/chargebacks
- [ ] Payment secrets (Razorpay API keys, webhook secrets) never appear in HTTP responses, browser-visible fields, or repository commits — only in server-side config
- [ ] Offer types (free, paid, subscription, cohort-based, invitation-only) are implemented with pricing, currency, tax, discounts, and availability windows
- [ ] Server-side checkout orders compute amounts and currency; client input is not trusted to set order price
- [ ] A basic revenue report (net of platform commission) is available per course and per offer for the creator/organization
- [ ] Platform commission is computed and recorded server-side on every successful order, separate from the organization's net revenue, and correctly reduced on refunds/chargebacks
- [ ] All access-granting and access-revoking actions are audit-logged per Task 3's audit_events table
- [ ] An append-only payment audit trail tracks all state transitions (order, payment, refund, chargeback) for support/dispute investigation
- [ ] Automated tests cover webhook signature verification, webhook idempotency, and "no access without verified webhook"
- [ ] Payment provider adapter interface is implemented with Razorpay as the only concrete MVP implementation; future providers can be added without rewriting core commerce logic
- [ ] Organization owners can view a read-only admin dashboard scoped to their own organization (members/roles, courses with status, enrollment and revenue overview)
- [ ] Platform owners (per Task 3's `is_platform_owner` flag) can view a read-only cross-organization dashboard (org list with member/course/enrollment counts and commission revenue), and this view is inaccessible to non-platform-owners

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
