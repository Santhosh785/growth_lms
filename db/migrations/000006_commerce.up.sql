-- Task 6 (db-migration): commerce, payments, and entitlements schema.
--
-- Every org-scoped table below follows Task 3/4/5's RLS convention:
-- org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, RLS
-- ENABLE + FORCE, policies built on is_org_member(org_id) /
-- is_org_owner(org_id) / app_current_role() (see
-- 000002_auth_tenancy.up.sql). None of these tables hang directly off
-- organizations by FK — they hang off offers, which hangs off courses,
-- which has org_id. Rather than requiring every policy to join through
-- orders -> offers -> courses to find org_id, org_id is denormalized
-- directly onto every table below that is conceptually org-scoped
-- (offers, discount_codes, commerce_invite_tokens, orders, payments,
-- refunds, chargebacks, entitlements, payment_audit_trail), the same way
-- 000004_learner_journey.up.sql denormalizes course_id onto
-- learner_lesson_progress "for RLS/query convenience". The org_id value
-- is derived at insert time from the offer's course's org (application
-- code sets it — no trigger needed).
--
-- orders.commission_rate_snapshot copies platform_settings.commission_percent
-- at order-creation time so a later change to the platform-wide commission
-- rate never retroactively alters historical orders' math.
--
-- entitlements is the single source of truth for "does this learner have
-- access to this course" — learner_course_access.entitlement_id (left as
-- a bare nullable UUID with no FK in 000004_learner_journey.up.sql,
-- before this table existed) points at it, and the FK is added below now
-- that entitlements exists. Per this repo's CLAUDE.md non-negotiable rule
-- ("payment/enrollment access must only ever be granted after verified
-- provider webhook events, never from browser return URLs"), entitlement
-- rows must only ever be created by a verified-webhook worker path or an
-- audit-logged admin grant — never directly from a browser request. This
-- migration only creates the schema that makes that rule enforceable
-- (entitlements has no general application INSERT policy beyond the
-- admin-grant path below); it does not implement the webhook-driven write
-- path itself — that privileged write mechanism (whether a service-role
-- DB session, a SECURITY DEFINER function, or something else) is decided
-- and built in the webhook-handler/commerce-handlers tasks, once this
-- codebase's actual worker auth context is confirmed. The same open point
-- applies to payments and chargebacks below (no INSERT/UPDATE policy for
-- ordinary application requests is defined for those either), and to
-- webhook_events (SELECT/INSERT restricted to app_is_platform_owner()
-- only) — this migration deliberately does not fabricate a policy shape
-- for a write path whose real auth context isn't known yet.
--
-- payment_audit_trail is append-only by omission of any UPDATE/DELETE
-- policy: under FORCE ROW LEVEL SECURITY, no policy for a command means
-- no row satisfies it, so every UPDATE/DELETE is rejected outright (table
-- owner included, once FORCE is set) — the same mechanism
-- learner_quiz_attempt/learner_quiz_score in 000004_learner_journey.up.sql
-- rely on to keep submissions immutable. No trigger is used.

-- === offers ================================================================
-- A purchasable/enrollable variant of a course.

CREATE TABLE offers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('free', 'paid', 'subscription', 'cohort', 'invitation_only')),
    price NUMERIC(12,2) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL CHECK (currency IN ('INR', 'USD')),
    tax_rate_percent NUMERIC(5,2) NOT NULL DEFAULT 0,
    access_duration_days INT,
    max_seats INT,
    enrollment_starts_at TIMESTAMPTZ,
    enrollment_ends_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_by UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX offers_org_idx ON offers (org_id);
CREATE INDEX offers_course_idx ON offers (course_id);

ALTER TABLE offers ENABLE ROW LEVEL SECURITY;
ALTER TABLE offers FORCE ROW LEVEL SECURITY;

CREATE POLICY offers_select ON offers FOR SELECT
  USING (is_org_member(offers.org_id));
CREATE POLICY offers_insert ON offers FOR INSERT
  WITH CHECK (is_org_member(offers.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY offers_update ON offers FOR UPDATE
  USING (is_org_member(offers.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY offers_delete ON offers FOR DELETE
  USING (is_org_member(offers.org_id) AND app_current_role() IN ('owner', 'teacher'));

-- === discount_codes ========================================================
-- Staff-only concept: discount codes are never learner-readable directly,
-- since checkout code redemption is validated server-side, not via a
-- client SELECT.

CREATE TABLE discount_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    offer_id UUID NOT NULL REFERENCES offers(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    discount_type TEXT NOT NULL CHECK (discount_type IN ('percent', 'fixed')),
    value NUMERIC(12,2) NOT NULL,
    expires_at TIMESTAMPTZ,
    max_redemptions INT,
    redemption_count INT NOT NULL DEFAULT 0,
    created_by UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (offer_id, code)
);
CREATE INDEX discount_codes_org_idx ON discount_codes (org_id);
CREATE INDEX discount_codes_offer_idx ON discount_codes (offer_id);

ALTER TABLE discount_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE discount_codes FORCE ROW LEVEL SECURITY;

CREATE POLICY discount_codes_select ON discount_codes FOR SELECT
  USING (is_org_member(discount_codes.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY discount_codes_insert ON discount_codes FOR INSERT
  WITH CHECK (is_org_member(discount_codes.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY discount_codes_update ON discount_codes FOR UPDATE
  USING (is_org_member(discount_codes.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY discount_codes_delete ON discount_codes FOR DELETE
  USING (is_org_member(discount_codes.org_id) AND app_current_role() IN ('owner', 'teacher'));

-- === commerce_invite_tokens ================================================
-- Single-use invitation-only offer access tokens. used_by_order_id
-- references orders, a table created later in this same migration; its FK
-- is added via a trailing ALTER TABLE statement after orders' CREATE
-- TABLE block (below), not inline here, to avoid a forward-reference
-- ordering problem.
--
-- No general UPDATE policy: token redemption (setting used_at) must go
-- through a SECURITY DEFINER function analogous to accept_invitation() in
-- 000002_auth_tenancy.up.sql, since the redeeming learner has no org
-- membership yet at redemption time. That function is out of scope for
-- this migration (built in the commerce-handlers task) — no UPDATE policy
-- is added here that would let an authenticated-but-unrelated user flip
-- used_at.

CREATE TABLE commerce_invite_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    offer_id UUID NOT NULL REFERENCES offers(id) ON DELETE CASCADE,
    token TEXT NOT NULL UNIQUE,
    bound_email TEXT,
    used_at TIMESTAMPTZ,
    used_by_learner_id UUID REFERENCES profiles(id),
    used_by_order_id UUID,
    expires_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX commerce_invite_tokens_org_idx ON commerce_invite_tokens (org_id);
CREATE INDEX commerce_invite_tokens_offer_idx ON commerce_invite_tokens (offer_id);

ALTER TABLE commerce_invite_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE commerce_invite_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY commerce_invite_tokens_select ON commerce_invite_tokens FOR SELECT
  USING (is_org_member(commerce_invite_tokens.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY commerce_invite_tokens_insert ON commerce_invite_tokens FOR INSERT
  WITH CHECK (is_org_member(commerce_invite_tokens.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY commerce_invite_tokens_delete ON commerce_invite_tokens FOR DELETE
  USING (is_org_member(commerce_invite_tokens.org_id) AND app_current_role() IN ('owner', 'teacher'));

-- === orders =================================================================

CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    offer_id UUID NOT NULL REFERENCES offers(id) ON DELETE CASCADE,
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK (currency IN ('INR', 'USD')),
    subtotal NUMERIC(12,2) NOT NULL,
    discount_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    tax_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    commission_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    total NUMERIC(12,2) NOT NULL,
    commission_rate_snapshot NUMERIC(5,2) NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
      CHECK (status IN ('pending', 'payment_initiated', 'succeeded', 'failed', 'abandoned')),
    razorpay_order_id TEXT,
    discount_code_id UUID REFERENCES discount_codes(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_org_idx ON orders (org_id);
CREATE INDEX orders_learner_idx ON orders (learner_id);
CREATE INDEX orders_offer_idx ON orders (offer_id);
CREATE INDEX orders_razorpay_order_id_idx ON orders (razorpay_order_id);

ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders FORCE ROW LEVEL SECURITY;

CREATE POLICY orders_select ON orders FOR SELECT
  USING (
    orders.learner_id = app_current_user_id()
    OR (is_org_member(orders.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY orders_insert ON orders FOR INSERT
  WITH CHECK (orders.learner_id = app_current_user_id() AND is_org_member(orders.org_id));
-- State transitions (pending -> payment_initiated -> succeeded/failed/
-- abandoned) are driven by the checkout handler and the webhook worker;
-- this UPDATE policy matches learner_course_access_update's shape.
CREATE POLICY orders_update ON orders FOR UPDATE
  USING (
    orders.learner_id = app_current_user_id()
    OR (is_org_member(orders.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
-- No DELETE policy: orders are never deleted, only transitioned to a
-- terminal status.

-- Now that orders exists, add commerce_invite_tokens.used_by_order_id's FK
-- (deferred from that table's own CREATE TABLE block above to avoid a
-- forward reference).
ALTER TABLE commerce_invite_tokens
  ADD CONSTRAINT commerce_invite_tokens_used_by_order_id_fkey
  FOREIGN KEY (used_by_order_id) REFERENCES orders(id);

-- === payments ================================================================
-- No INSERT/UPDATE/DELETE policy for ordinary application requests:
-- payment rows are only ever written by whatever privileged write path
-- the webhook-handler/worker-jobs tasks build (see this file's header
-- comment) — this migration does not guess that mechanism's shape.

CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    razorpay_payment_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'succeeded', 'failed')),
    raw_provider_data JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payments_org_idx ON payments (org_id);
CREATE INDEX payments_order_idx ON payments (order_id);
CREATE INDEX payments_razorpay_payment_id_idx ON payments (razorpay_payment_id);

ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments FORCE ROW LEVEL SECURITY;

CREATE POLICY payments_select ON payments FOR SELECT
  USING (
    (is_org_member(payments.org_id) AND app_current_role() IN ('owner', 'teacher'))
    OR EXISTS (
      SELECT 1 FROM orders o
      WHERE o.id = payments.order_id AND o.learner_id = app_current_user_id()
    )
  );

-- === refunds =================================================================
-- INSERT is an owner/teacher initiating a refund from the admin UI; the
-- row's status starts 'pending' and is only flipped to succeeded/failed
-- by the verified webhook (no UPDATE policy — same open point as
-- payments above).

CREATE TABLE refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    razorpay_refund_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    amount NUMERIC(12,2) NOT NULL,
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX refunds_org_idx ON refunds (org_id);
CREATE INDEX refunds_payment_idx ON refunds (payment_id);

ALTER TABLE refunds ENABLE ROW LEVEL SECURITY;
ALTER TABLE refunds FORCE ROW LEVEL SECURITY;

CREATE POLICY refunds_select ON refunds FOR SELECT
  USING (
    (is_org_member(refunds.org_id) AND app_current_role() IN ('owner', 'teacher'))
    OR EXISTS (
      SELECT 1 FROM payments p
      JOIN orders o ON o.id = p.order_id
      WHERE p.id = refunds.payment_id AND o.learner_id = app_current_user_id()
    )
  );
CREATE POLICY refunds_insert ON refunds FOR INSERT
  WITH CHECK (
    is_org_member(refunds.org_id)
    AND app_current_role() IN ('owner', 'teacher')
    AND refunds.status = 'pending'
  );

-- === chargebacks =============================================================
-- Staff/finance concern only, no learner-facing read. No INSERT/UPDATE
-- policy for ordinary requests: chargebacks are only ever created/updated
-- by the webhook worker from a verified Razorpay dispute event, same open
-- point as payments above.

CREATE TABLE chargebacks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'won', 'lost')),
    amount NUMERIC(12,2) NOT NULL,
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX chargebacks_org_idx ON chargebacks (org_id);
CREATE INDEX chargebacks_payment_idx ON chargebacks (payment_id);

ALTER TABLE chargebacks ENABLE ROW LEVEL SECURITY;
ALTER TABLE chargebacks FORCE ROW LEVEL SECURITY;

CREATE POLICY chargebacks_select ON chargebacks FOR SELECT
  USING (is_org_member(chargebacks.org_id) AND app_current_role() IN ('owner', 'teacher'));

-- === entitlements =============================================================
-- The actual "does this learner have access to this course" grant record
-- that learner_course_access.entitlement_id points to. INSERT/UPDATE here
-- covers the admin-grant path only (owner/teacher granting access
-- directly); the purchase-driven path is the same open point flagged for
-- payments above, not a second policy shape fabricated here.

CREATE TABLE entitlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    order_id UUID REFERENCES orders(id),
    learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    expires_at TIMESTAMPTZ,
    granted_by UUID REFERENCES profiles(id),
    grant_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX entitlements_org_idx ON entitlements (org_id);
CREATE INDEX entitlements_learner_idx ON entitlements (learner_id);
CREATE INDEX entitlements_course_idx ON entitlements (course_id);
CREATE INDEX entitlements_order_idx ON entitlements (order_id);

ALTER TABLE entitlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlements FORCE ROW LEVEL SECURITY;

CREATE POLICY entitlements_select ON entitlements FOR SELECT
  USING (
    entitlements.learner_id = app_current_user_id()
    OR (is_org_member(entitlements.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );
CREATE POLICY entitlements_insert ON entitlements FOR INSERT
  WITH CHECK (is_org_member(entitlements.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY entitlements_update ON entitlements FOR UPDATE
  USING (is_org_member(entitlements.org_id) AND app_current_role() IN ('owner', 'teacher'));

-- Now that entitlements exists, close the gap Task 5 left open:
-- learner_course_access.entitlement_id (introduced NULL/no-FK in
-- 000004_learner_journey.up.sql, before this table existed) gets a real
-- foreign key. No ON DELETE action is specified deliberately (default NO
-- ACTION): an entitlement should never be hard-deleted while a
-- learner_course_access row still points at it; the revocation path is
-- always the entitlements.status = 'revoked' update, not a delete.
ALTER TABLE learner_course_access
  ADD CONSTRAINT learner_course_access_entitlement_id_fkey
  FOREIGN KEY (entitlement_id) REFERENCES entitlements(id);

-- === payment_audit_trail ======================================================
-- Append-only ledger of every money-state transition, for support/dispute
-- investigation and compliance. INSERT is permissive (any org member's
-- authenticated request context may append a row), mirroring
-- audit_events_insert's permissive-insert/restricted-read shape from
-- Task 3. No UPDATE or DELETE policy at all — see this file's header
-- comment for why that omission alone makes the table append-only.

CREATE TABLE payment_audit_trail (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    order_id UUID REFERENCES orders(id),
    payment_id UUID REFERENCES payments(id),
    old_state TEXT,
    new_state TEXT,
    reason TEXT,
    user_id UUID REFERENCES profiles(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_audit_trail_org_idx ON payment_audit_trail (org_id, created_at DESC);
CREATE INDEX payment_audit_trail_order_idx ON payment_audit_trail (order_id);

ALTER TABLE payment_audit_trail ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_audit_trail FORCE ROW LEVEL SECURITY;

CREATE POLICY payment_audit_trail_select ON payment_audit_trail FOR SELECT
  USING (is_org_member(payment_audit_trail.org_id) AND app_current_role() IN ('owner', 'teacher'));
CREATE POLICY payment_audit_trail_insert ON payment_audit_trail FOR INSERT
  WITH CHECK (is_org_member(payment_audit_trail.org_id));

-- === webhook_events ===========================================================
-- Idempotency dedup table for the Razorpay webhook handler. Not
-- org-scoped (a webhook event isn't known to belong to one org until its
-- payload is parsed) — no org_id column, but RLS is still enabled/forced
-- per this codebase's blanket "every table gets RLS" convention.
-- SELECT/INSERT restricted to app_is_platform_owner() only; the actual
-- webhook-handler write path's auth context (and its
-- INSERT ... ON CONFLICT (razorpay_event_id) DO NOTHING dedup check) is
-- built in the webhook-handler task, not here.

CREATE TABLE webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    razorpay_event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_events FORCE ROW LEVEL SECURITY;

CREATE POLICY webhook_events_select ON webhook_events FOR SELECT
  USING (app_is_platform_owner());
CREATE POLICY webhook_events_insert ON webhook_events FOR INSERT
  WITH CHECK (app_is_platform_owner());

-- === platform_settings ========================================================
-- Single-row, platform-wide (not org-scoped) commission configuration. No
-- INSERT/DELETE policy — the single row is seeded by this migration
-- itself (which runs as the migration's own privileged role and so isn't
-- subject to RLS) and is never re-created or removed by application code
-- afterward.

CREATE TABLE platform_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    commission_percent NUMERIC(5,2) NOT NULL DEFAULT 0,
    updated_by UUID REFERENCES profiles(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE platform_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_settings FORCE ROW LEVEL SECURITY;

CREATE POLICY platform_settings_select ON platform_settings FOR SELECT
  USING (app_is_platform_owner());
CREATE POLICY platform_settings_update ON platform_settings FOR UPDATE
  USING (app_is_platform_owner());

-- Seed the MVP's single platform-wide commission rate (10%).
INSERT INTO platform_settings (commission_percent) VALUES (10);
