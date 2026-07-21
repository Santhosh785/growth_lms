# Plan: task-6-implementation

## Goal

Implement Task 6 (`plans/lms-mvp/task-6-commerce.md`): course monetization —
offers (free/paid/subscription/cohort/invitation-only), server-side checkout,
a Razorpay payment adapter, signature-verified idempotent webhook processing,
entitlements that are only ever granted from a verified webhook or an
audit-logged admin action, refunds/chargebacks, platform commission, revenue
reporting, and a basic read-only admin dashboard (org-scoped + platform
cross-org). This is the last engineering task in the MVP boundary (Tasks
1-6); Task 7 (human-readable overview) follows once this merges.

## Approach

Follow Task 3/4/5's established conventions exactly: `models.Querier`-based
repos, `dbctx`/RLS session-variable pattern, `RequireRole`/`Can()`
defense-in-depth, an interface-based external-provider client
(`internal/payments.Provider`, mirroring `internal/media`'s Bunny client
interface), and the signature-verify-then-enqueue-to-asynq webhook pattern
already proven by Task 4's Bunny Stream webhook. Schema and money-state
tables land first (Task 4/5 precedent: migration before everything else),
then repositories, then the provider client, then handlers/webhook/worker
jobs/admin dashboard in parallel, then routing, then tests.

## Decisions & Rejected Alternatives

See `grilling-record.md` in this directory for the full 14-question grilling
session (recommendation vs. chosen option for each). Summary of the binding
decisions:

- **Currency: INR + USD, no FX conversion** — each offer priced/taxed in its
  own currency; revenue/commission reports segment by currency rather than
  summing across them. Rejected: INR-only (narrower than what the user
  wanted) or fully currency-agnostic (unbounded complexity for no ask).
- **"Subscription" offer = fixed-term access pass, not recurring billing** —
  one-time payment, `expires_at` on the entitlement, revoked by a sweep job.
  Rejected: real Razorpay Subscriptions integration (a second payment
  integration the spec's word choice doesn't actually require); dropping the
  offer type (diverges from the spec's explicit 5-type list).
- **"Cohort-based" offer = enrollment window + optional seat cap** on a paid
  offer. Rejected: a full cohort/course-run entity (materially bigger scope,
  closer to scheduled-course-runs than a checkout variant).
- **"Invitation-only" offer = single-use, org-scoped invite token**, mirrors
  Task 3's org-invitation token pattern. Rejected: email allowlist (weaker,
  no per-link revocation) or admin-grant-only with no self-checkout path.
- **Tax: flat, manually-configured rate per offer**, no jurisdiction
  detection. Rejected: platform-default-with-override (extra config surface
  not asked for) or no tax computation (fails the spec's explicit "tax
  breakdown in receipts" criterion).
- **Discount codes: org-created, scoped to specific offer(s), one code per
  order** (no stacking). Rejected: org-wide + stacking (more edge cases in
  server-side total computation) or platform-owner-only codes (removes an
  org-level marketing lever).
- **Platform commission: DB-backed `platform_settings` row, editable via a
  platform-owner endpoint, snapshotted onto every order at purchase time.**
  Rejected: env-var/config-file (needs a deploy to change, doesn't meet
  "configurable") or a hardcoded constant.
- **Checkout: server-rendered HTML+HTMX page embedding Razorpay
  `checkout.js`**, consistent with every prior task's dual HTML+JSON
  approach. Rejected: JSON-API-only (would leave commerce unusable from the
  browser given no other client exists yet).
- **Post-payment: an order-status page HTMX-polls until the webhook has
  landed and the entitlement exists**, then redirects into the course. The
  client-side `checkout.js` success callback is never trusted to grant
  access — matches `CLAUDE.md`'s non-negotiable "never from browser return
  URLs" rule. Rejected: immediate redirect with a soft pending banner (worse
  UX if the webhook is delayed, though considered).
- **Refunds: in-app "Refund" action calls Razorpay's Refund API**; the actual
  entitlement revocation/revenue adjustment still only happens when the
  resulting refund webhook is verified and processed. Rejected:
  Razorpay-dashboard-only refunds (worse admin UX, requires sharing Razorpay
  dashboard credentials with org owners).
- **Admin entitlement grants: org owner or teacher, reason required,
  audit-logged.** Rejected: owner-only (tighter but not asked for) or no
  reason field (thinner audit trail for a money-adjacent action).
- **Stale pending orders: periodic asynq sweep marks them `abandoned` after
  30 minutes**, same shape as Task 4's `TypeSweepScheduledPublish`. Rejected:
  no timeout (lets stale rows accumulate and pollute reporting).
- **Receipts: async email via Resend on verified payment success**, itemized
  with price/tax/discount, never showing platform commission to the learner.
  Rejected: no receipt email (fails the spec's "tax breakdown in receipts"
  criterion under most readings).
- **Fixed-term pass expiry: periodic asynq sweep flips `access_status` to
  `expired` + audit log**, not a lazy check-at-access-time-only. Rejected:
  lazy-only (DB state misleadingly shows "active" long after expiry, hurts
  admin dashboard accuracy).
- **No GitHub remote configured in this repo** → this plan is a disk-only
  draft (not published as GitHub issues); `/run-plan` is not used.
  Implementation proceeds directly in this session/worktree by the same
  agent; the phase/task breakdown below still describes the true dependency
  graph even though it isn't executed via separate subagents through
  `/run-plan`.

## Tasks

| # | Task | Phase | Depends on | Status |
|---|------|-------|------------|--------|
| 1 | db-migration | 1 | — | done |
| 2 | config-secrets | 1 | — | done |
| 3 | permissions-matrix | 1 | — | done |
| 4 | models-repositories | 2 | 1 | done |
| 5 | razorpay-client | 2 | 2 | done |
| 6 | commerce-handlers | 3 | 3, 4, 5 | done |
| 7 | webhook-handler | 3 | 4, 5 | done |
| 8 | worker-jobs | 3 | 4, 5 | done |
| 9 | admin-dashboard | 3 | 4 | done |
| 10 | routes-wiring | 4 | 6, 7, 8, 9 | done |
| 11 | tests | 5 | 10 | done |

## Status: complete

All 11 tasks landed. Two real gaps surfaced by task-11's test coverage
(paid invitation-only tokens never marked used; refund/chargeback not
proportionally reducing reported commission) were fixed as part of this
same branch rather than left open — see the git log for the two
follow-up commits after phase 5. A migration follow-up
(`000007_commerce_rls_fixes`) also closes two RLS gaps discovered while
implementing commerce-handlers and admin-dashboard: the free-offer
checkout path's entitlement insert, and the platform-owner cross-org
dashboard's course/order/enrollment visibility.

This completes the full MVP boundary (Tasks 1-6 per `CLAUDE.md`).

## Execution phases

- **Phase 1 (parallel):** task-1 (db-migration), task-2 (config-secrets), task-3 (permissions-matrix)
- **Phase 2 (parallel):** task-4 (models-repositories), task-5 (razorpay-client)
- **Phase 3 (parallel):** task-6 (commerce-handlers), task-7 (webhook-handler), task-8 (worker-jobs), task-9 (admin-dashboard)
- **Phase 4:** task-10 (routes-wiring)
- **Phase 5:** task-11 (tests)
