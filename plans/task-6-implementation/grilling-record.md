# Grilling Record

> Reference only — not part of the spec. Kept for a future agent, developer,
> or the author revisiting why this plan is shaped this way. This record was
> produced by a `/grill-me` session over `plans/lms-mvp/task-6-commerce.md`
> before drafting this implementation plan.

### Q1: Which currency should MVP commerce support at launch?

**Options presented:**
1. INR only — matches the Razorpay/India-market rationale already in `plans/lms-mvp/main-plan.md`.
2. INR + USD — wider reach, doubles pricing/tax/reporting complexity.
3. Fully currency-agnostic from day one — most flexible, unbounded complexity.

**Recommended:** Option 1 (INR only) — smallest scope consistent with the existing Razorpay decision.
**Chosen:** Option 2 (INR + USD) — against the recommendation; the user wanted wider currency reach without full FX complexity.

### Q2: How should the 'subscription' offer type work for MVP?

**Options presented:**
1. Fixed-term pass, not recurring billing — one-time payment granting time-limited access, no Razorpay Subscriptions API.
2. Real recurring billing via Razorpay Subscriptions — matches the word literally, materially bigger integration.
3. Drop subscription from MVP entirely.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q3: What does a 'cohort-based' offer mean for MVP?

**Options presented:**
1. Fixed enrollment window + capacity cap on a paid offer, no separate cohort entity.
2. Full cohort entity with its own course-run dates, learners assigned to a specific run.
3. Alias for paid with a capacity cap only, no date window.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q4: How does an 'invitation-only' offer restrict who can check out?

**Options presented:**
1. Per-learner invite token/link — mirrors Task 3's org-invitation pattern.
2. Allowlist of emails on the offer — simpler, weaker (no per-link revocation).
3. Manual admin-grant only, no self-checkout.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q5: How should tax be computed for MVP checkout?

**Options presented:**
1. Flat, manually-configured rate per offer.
2. Platform-level default rate with per-org override.
3. No tax computation in MVP.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q6: How are discount codes scoped and applied?

**Options presented:**
1. Org-created, tied to specific offer(s), one code per order (no stacking).
2. Org-created, apply org-wide, stacking allowed.
3. Platform-owner-managed codes only.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q7: How is the platform commission rate configured?

**Options presented:**
1. DB-backed `platform_settings` row, editable via platform-owner API, snapshotted onto each order.
2. Env var / config file, requires a deploy to change.
3. Hardcoded constant in code for MVP.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q8: Should the checkout page be server-rendered HTML+HTMX, or JSON-API only?

**Options presented:**
1. Server-rendered HTML page embedding Razorpay Checkout.js, JSON API underneath.
2. JSON-API only for MVP, no HTML checkout page.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q9: What should the learner see between checkout.js success and the webhook actually granting access?

**Options presented:**
1. "Processing" page that HTMX-polls order status until entitlement appears.
2. Immediate redirect to course page with a soft "access pending" banner.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q10: How are refunds initiated for MVP?

**Options presented:**
1. In-app refund action calling Razorpay's Refund API; state change still only happens on the verified refund webhook.
2. Razorpay-dashboard-only; system just reacts to webhooks.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q11: Who can manually grant a free entitlement, and what's required?

**Options presented:**
1. Org owner + teacher, reason required, audit-logged.
2. Org owner only, reason required.
3. Org owner + teacher, no reason field required.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q12: How should stale 'pending' orders be handled?

**Options presented:**
1. Scheduled asynq sweep marks pending orders abandoned after a timeout (~30 min), same shape as Task 4's scheduled-publish sweep.
2. No automatic timeout — rely entirely on Razorpay's own order expiry.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q13: Should successful payments trigger an emailed receipt?

**Options presented:**
1. Yes — async email via Resend with price/tax/discount breakdown, reusing Task 5's notification-email worker pattern.
2. No receipt email for MVP, in-app order record only.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

### Q14: How should expired fixed-term (subscription) entitlements lose access?

**Options presented:**
1. Scheduled asynq sweep flips `access_status` to `expired` + audit log.
2. Lazy check at access time only, no sweep.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.
