---
task: 1
name: db-migration
parallel_group: 1
depends_on: []
issue: TBD
---

# Task 1: db-migration

## What to build

Add a new migration pair `db/migrations/000006_commerce.up.sql` and
`db/migrations/000006_commerce.down.sql` that creates the full commerce/
payments schema for Task 6. This is the first task in Task 6 and has no
dependencies — everything else in Task 6 (repositories, Razorpay client,
handlers, webhook processing, worker jobs, admin dashboard) is built on top
of the tables created here.

Read `db/migrations/000002_auth_tenancy.up.sql` (helper functions
`app_current_user_id()`, `app_current_org_id()`, `app_current_role()`,
`app_is_platform_owner()`, `is_org_member(p_org_id)`, `is_org_owner(p_org_id)`),
`db/migrations/000003_course_domain.up.sql` (`courses` table shape), and
`db/migrations/000004_learner_journey.up.sql` (org-scoped RLS conventions,
denormalization precedent) in full before writing the migration — match
their conventions exactly: table/column naming style, index naming
(`<table>_<col>_idx`), `ENABLE ROW LEVEL SECURITY` + `FORCE ROW LEVEL
SECURITY` on every table, policy naming (`<table>_select`, `<table>_insert`,
`<table>_update`, `<table>_delete`), and a header comment block at the top
of the `.up.sql` file explaining the RLS pattern used, matching the style of
the header comments in 000004 and 000005.

### RLS pattern for this migration

Every org-owned table must carry `org_id UUID NOT NULL REFERENCES
organizations(id) ON DELETE CASCADE` and enforce it via `is_org_member(org_id)`
/ `is_org_owner(org_id)` / `app_current_role()`, exactly as Task 4/5 do. None
of the commerce tables hang off `organizations` directly by FK — they hang
off `offers`, which hangs off `courses`, which has `org_id`. Rather than
requiring every policy to join through `orders → offers → courses` to find
`org_id` (expensive and error-prone to get right in every policy), **denormalize
`org_id` directly onto every table below that is conceptually org-scoped**
(`orders`, `payments`, `refunds`, `chargebacks`, `entitlements`,
`payment_audit_trail`), exactly the way `000004_learner_journey.up.sql`
denormalizes `course_id` onto `learner_lesson_progress` "for RLS/query
convenience." The org_id value is derived at insert time from the offer's
course's org (application code / a `SELECT courses.org_id ... JOIN offers
...` sets it — no trigger needed, but note this expectation in a comment).

Two general shapes of policy are needed:

1. **Owner/teacher full org visibility, no learner-owned-row concept**
   (`offers`, `discount_codes`, `commerce_invite_tokens`, `payment_audit_trail`,
   `webhook_events`): SELECT/INSERT/UPDATE/DELETE (as applicable) gated on
   `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')` for
   write-type operations that only staff perform (creating offers, discount
   codes, invite tokens), and `is_org_member(org_id)` alone where a broader
   org-member read makes sense. `webhook_events` is a special case — see below.

2. **Learner-owned row + org owner/teacher override**, matching the
   `learner_lesson_progress`/`learner_course_access` pattern from Task 5
   (`learner_id = app_current_user_id() OR (is_org_member(org_id) AND
   app_current_role() IN ('owner', 'teacher'))`): `orders`, `payments` (via
   join or denormalized learner_id — see below), `entitlements`.

`payments`, `refunds`, and `chargebacks` don't have their own `learner_id`
column (they hang off `orders`/`payments`), so their learner-read policy
must be expressed via `EXISTS` back to the owning `orders` row's
`learner_id`, following the same join-based-read pattern
`learner_assignment_grade_select` uses in `000004_learner_journey.up.sql` to
read back to `learner_assignment_submission.learner_id`. Owner/teacher access
on these three tables can use the denormalized `org_id` column directly
(no join needed for that half of the policy).

### Tables to create

All monetary columns use `NUMERIC(12,2)` (money needs more headroom than the
`NUMERIC(5,2)` used for percentages/grades elsewhere in this codebase).
Percentage columns (commission rate, tax rate, discount value-as-percent)
follow the existing `NUMERIC(5,2)` convention (e.g. `grade_percentage
NUMERIC(5,2)` in `learner_assignment_grade`). All tables get a primary key
`id UUID PRIMARY KEY DEFAULT gen_random_uuid()` unless noted otherwise, and
an index on `org_id` (and any FK column used in RLS or frequently queried)
following the `<table>_<col>_idx` naming convention.

1. **`offers`** — a purchasable/enrollable variant of a course.
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE`
   - `course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE`
   - `type TEXT NOT NULL CHECK (type IN ('free', 'paid', 'subscription', 'cohort', 'invitation_only'))`
   - `price NUMERIC(12,2) NOT NULL DEFAULT 0`
   - `currency TEXT NOT NULL CHECK (currency IN ('INR', 'USD'))`
   - `tax_rate_percent NUMERIC(5,2) NOT NULL DEFAULT 0`
   - `access_duration_days INT` — nullable; used by `subscription`-type offers (fixed-term access pass duration in days; NULL means unlimited/not applicable)
   - `max_seats INT` — nullable; used by `cohort`-type offers (seat cap)
   - `enrollment_starts_at TIMESTAMPTZ` — nullable; used by `cohort`-type offers
   - `enrollment_ends_at TIMESTAMPTZ` — nullable; used by `cohort`-type offers
   - `status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived'))`
   - `created_by UUID NOT NULL REFERENCES profiles(id)`
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `offers_org_idx (org_id)`, `offers_course_idx (course_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT: `is_org_member(org_id)` (all org members can browse offers, matching `courses_select`'s convention). INSERT/UPDATE/DELETE: `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')`.

2. **`discount_codes`**
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE` (denormalized from `offers.org_id` for RLS convenience)
   - `offer_id UUID NOT NULL REFERENCES offers(id) ON DELETE CASCADE`
   - `code TEXT NOT NULL`
   - `discount_type TEXT NOT NULL CHECK (discount_type IN ('percent', 'fixed'))`
   - `value NUMERIC(12,2) NOT NULL`
   - `expires_at TIMESTAMPTZ`
   - `max_redemptions INT`
   - `redemption_count INT NOT NULL DEFAULT 0`
   - `created_by UUID NOT NULL REFERENCES profiles(id)`
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - `UNIQUE (offer_id, code)`
   - Indexes: `discount_codes_org_idx (org_id)`, `discount_codes_offer_idx (offer_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT/INSERT/UPDATE/DELETE all gated on `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')` — discount codes are a staff-only concept, never learner-readable directly (checkout code redemption is validated server-side, not via a client SELECT).

3. **`commerce_invite_tokens`** — single-use invitation-only offer access tokens.
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE`
   - `offer_id UUID NOT NULL REFERENCES offers(id) ON DELETE CASCADE`
   - `token TEXT NOT NULL UNIQUE`
   - `bound_email TEXT` — nullable; if set, the token can only be redeemed by a matching authenticated email (mirrors `invitations.email` from Task 3)
   - `used_at TIMESTAMPTZ` — nullable; set once redeemed
   - `used_by_learner_id UUID REFERENCES profiles(id)` — nullable; set alongside `used_at`, records which learner redeemed the token (support/audit trail)
   - `used_by_order_id UUID REFERENCES orders(id)` — nullable; set alongside `used_at`, records which order the redemption produced
   - `expires_at TIMESTAMPTZ` — nullable
   - `created_by UUID NOT NULL REFERENCES profiles(id)`
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `commerce_invite_tokens_org_idx (org_id)`, `commerce_invite_tokens_offer_idx (offer_id)`
   - Note: `used_by_order_id` references `orders`, a table created later in this same migration — since both tables are created in one transaction, add this FK via a trailing `ALTER TABLE commerce_invite_tokens ADD CONSTRAINT ... FOREIGN KEY (used_by_order_id) REFERENCES orders(id);` statement after the `orders` table's `CREATE TABLE` block, not inline in `commerce_invite_tokens`'s own `CREATE TABLE` (avoids a forward-reference ordering problem).
   - RLS: `ENABLE`/`FORCE`. SELECT/INSERT/DELETE gated on `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')`. No general UPDATE policy — token redemption (setting `used_at`) must go through a `SECURITY DEFINER` function analogous to `accept_invitation()` in `000002_auth_tenancy.up.sql`, since the redeeming learner has no org membership yet at redemption time; note this expectation in a comment but the function itself is out of scope for this migration (built in the commerce-handlers task) — do not add an UPDATE policy that would let an authenticated-but-unrelated user flip `used_at`.

4. **`orders`**
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE` (denormalized from `offers.org_id`)
   - `offer_id UUID NOT NULL REFERENCES offers(id) ON DELETE CASCADE`
   - `learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE`
   - `currency TEXT NOT NULL CHECK (currency IN ('INR', 'USD'))`
   - `subtotal NUMERIC(12,2) NOT NULL`
   - `discount_amount NUMERIC(12,2) NOT NULL DEFAULT 0`
   - `tax_amount NUMERIC(12,2) NOT NULL DEFAULT 0`
   - `commission_amount NUMERIC(12,2) NOT NULL DEFAULT 0`
   - `total NUMERIC(12,2) NOT NULL`
   - `commission_rate_snapshot NUMERIC(5,2) NOT NULL` — copied from `platform_settings.commission_percent` at order-creation time so later commission-rate changes never retroactively alter historical orders
   - `status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'payment_initiated', 'succeeded', 'failed', 'abandoned'))`
   - `razorpay_order_id TEXT` — nullable
   - `discount_code_id UUID REFERENCES discount_codes(id)` — nullable
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `orders_org_idx (org_id)`, `orders_learner_idx (learner_id)`, `orders_offer_idx (offer_id)`, `orders_razorpay_order_id_idx (razorpay_order_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT: `learner_id = app_current_user_id() OR (is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher'))`. INSERT: `learner_id = app_current_user_id() AND is_org_member(org_id)` (a learner creates their own order at checkout start). UPDATE: state transitions (`pending` → `payment_initiated` → `succeeded`/`failed`/`abandoned`) are driven by the checkout handler and the webhook worker, both of which run with the learner's or a service-level session context respectively — allow `learner_id = app_current_user_id() OR (is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher'))` for UPDATE too, matching `learner_course_access_update`'s shape. No DELETE policy — orders are never deleted, only transitioned to a terminal status.

5. **`payments`**
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE` (denormalized from `orders.org_id`)
   - `order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE`
   - `razorpay_payment_id TEXT` — nullable
   - `status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'succeeded', 'failed'))`
   - `raw_provider_data JSONB` — nullable; the raw Razorpay payment payload for audit/debugging
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `payments_org_idx (org_id)`, `payments_order_idx (order_id)`, `payments_razorpay_payment_id_idx (razorpay_payment_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT: `(is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')) OR EXISTS (SELECT 1 FROM orders o WHERE o.id = payments.order_id AND o.learner_id = app_current_user_id())` — mirrors `learner_assignment_grade_select`'s join-back-to-owner pattern. No INSERT/UPDATE/DELETE policy for ordinary application requests — payment rows are only ever written by the webhook worker's privileged DB session (service-role connection, not subject to the learner-facing RLS session variables in the same way; document this in a comment), so no policy is needed to permit application-level writes here. If the worker runs under the same RLS session-variable convention as everything else, add this note but do not fabricate a policy shape without knowing the worker's actual auth context — flag it as a follow-up to confirm in the webhook-handler/worker-jobs tasks rather than guessing.

6. **`refunds`**
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE` (denormalized from `payments.org_id`)
   - `payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE`
   - `razorpay_refund_id TEXT` — nullable
   - `status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed'))`
   - `amount NUMERIC(12,2) NOT NULL`
   - `reason TEXT` — nullable
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `refunds_org_idx (org_id)`, `refunds_payment_idx (payment_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT: `(is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')) OR EXISTS (SELECT 1 FROM payments p JOIN orders o ON o.id = p.order_id WHERE p.id = refunds.payment_id AND o.learner_id = app_current_user_id())`. INSERT gated on `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')` — an owner/teacher initiates a refund from the admin UI; the row's `status` starts `pending` and is only flipped to `succeeded`/`failed` by the verified webhook.

7. **`chargebacks`**
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE` (denormalized from `payments.org_id`)
   - `payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE`
   - `status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'won', 'lost'))`
   - `amount NUMERIC(12,2) NOT NULL`
   - `reason TEXT` — nullable
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `chargebacks_org_idx (org_id)`, `chargebacks_payment_idx (payment_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT: `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')` (chargebacks are a staff/finance concern only, no learner-facing read). No INSERT/UPDATE policy for ordinary requests — chargebacks are only ever created/updated by the webhook worker from a verified Razorpay dispute event, same reasoning as `payments`.

8. **`entitlements`** — the actual "does this learner have access to this course" grant record that `learner_course_access.entitlement_id` points to.
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE` (denormalized from `courses.org_id`)
   - `order_id UUID REFERENCES orders(id)` — nullable (nullable to allow admin grants with no purchase)
   - `learner_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE`
   - `course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE`
   - `status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired'))`
   - `expires_at TIMESTAMPTZ` — nullable; set for fixed-term (subscription-type) passes, swept to `expired` by the periodic asynq job
   - `granted_by UUID REFERENCES profiles(id)` — nullable; set for admin grants (the granting owner/teacher), NULL for ordinary purchase-driven entitlements
   - `grant_reason TEXT` — nullable; required at the application layer when `granted_by` is set (an admin grant must always be audit-logged with a reason — enforce this in application code / the commerce-handlers task, not as a DB CHECK, since a CHECK tying two nullable columns together is brittle and the reason text itself belongs in `payment_audit_trail` too)
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `entitlements_org_idx (org_id)`, `entitlements_learner_idx (learner_id)`, `entitlements_course_idx (course_id)`, `entitlements_order_idx (order_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT: `learner_id = app_current_user_id() OR (is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher'))`. INSERT/UPDATE gated on `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')` for the admin-grant path — the purchase-driven path (webhook worker) is expected to run under a privileged/service context the same way `payments` writes do; note this as the same open point flagged under `payments` rather than fabricating a second policy shape.

9. **`payment_audit_trail`** — append-only ledger of every money-state transition.
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE` (denormalized so reads never need to join through orders→offers→courses)
   - `event_type TEXT NOT NULL`
   - `order_id UUID REFERENCES orders(id)` — nullable
   - `payment_id UUID REFERENCES payments(id)` — nullable
   - `old_state TEXT` — nullable
   - `new_state TEXT` — nullable
   - `reason TEXT` — nullable
   - `user_id UUID REFERENCES profiles(id)` — nullable (the acting user for admin actions; NULL for system/webhook-driven transitions)
   - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
   - Indexes: `payment_audit_trail_org_idx (org_id, created_at DESC)` (matches `audit_events_org_idx`'s `(org_id, created_at DESC)` shape from `000002_auth_tenancy.up.sql`), `payment_audit_trail_order_idx (order_id)`
   - RLS: `ENABLE`/`FORCE`. SELECT: `is_org_member(org_id) AND app_current_role() IN ('owner', 'teacher')`. INSERT: `is_org_member(org_id)` (any org member's authenticated request context, including the webhook worker if it runs with org context set, may append a row — this mirrors `audit_events_insert`'s permissive-insert/restricted-read shape from Task 3). **No UPDATE or DELETE policy at all** — omitting both policies is sufficient to make the table append-only under `FORCE ROW LEVEL SECURITY` (no policy for a command means no row satisfies it, so every UPDATE/DELETE is rejected outright, table owner included once FORCE is set). Add a comment explaining this is the enforcement mechanism, not a trigger — do not add a `BEFORE UPDATE/DELETE` trigger, since the RLS omission already achieves the "no update/delete allowed" requirement identically to how `learner_quiz_attempt`/`learner_quiz_score` in Task 5 have no UPDATE policy to keep submissions immutable.

10. **`webhook_events`** — idempotency dedup table for the Razorpay webhook handler.
    - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
    - `razorpay_event_id TEXT NOT NULL UNIQUE`
    - `event_type TEXT NOT NULL`
    - `payload JSONB NOT NULL` — the raw webhook body, stored for reprocessing/debugging (the handler that verified the signature has already read these bytes; persisting them here avoids ever needing to re-derive or trust an unverified re-delivery)
    - `processed_at TIMESTAMPTZ` — nullable; set once the async worker finishes processing the corresponding job
    - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
    - Index: `webhook_events_razorpay_event_id_idx` is implied by the `UNIQUE` constraint (Postgres auto-creates it) — no separate explicit index needed, following the same reasoning `orders.razorpay_order_id` does NOT need this (that one isn't unique, so it does get an explicit index above).
    - This table is not org-scoped (a webhook event isn't known to belong to one org until its payload is parsed) — no `org_id` column.
    - RLS: `ENABLE`/`FORCE` regardless of the lack of org scoping, per this codebase's blanket "every table gets RLS" convention. SELECT/INSERT restricted to `app_is_platform_owner()` only — this table is written exclusively by the webhook HTTP handler's own privileged DB session (documented in a comment, same open point as `payments`/`entitlements` above: confirm the actual write path's auth context in the webhook-handler task) and read only for platform-level debugging. The webhook HTTP handler's actual `INSERT ... ON CONFLICT (razorpay_event_id) DO NOTHING` dedup check (checking whether the insert affected 0 rows to detect a duplicate delivery and skip enqueueing) is implemented in the webhook-handler task, not this migration — this migration only needs to provide the table, the `UNIQUE` constraint, and RLS.

11. **`platform_settings`** — single-row, platform-wide (not org-scoped) commission configuration.
    - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
    - `commission_percent NUMERIC(5,2) NOT NULL DEFAULT 0`
    - `updated_by UUID REFERENCES profiles(id)` — nullable
    - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
    - No `org_id` column and no per-org RLS — this table is platform-wide by design.
    - RLS: `ENABLE`/`FORCE`. SELECT and UPDATE both gated on `app_is_platform_owner()` only (use `000002_auth_tenancy.up.sql`'s `app_is_platform_owner()` helper, not a new one). No INSERT/DELETE policy — the single row is seeded by this migration itself (which runs as the migration's own privileged role and so isn't subject to RLS) and is never re-created or removed by application code afterward.
    - Seed exactly one row via `INSERT INTO platform_settings (commission_percent) VALUES (10);` at the end of the table's DDL block in the `.up.sql` file (10% default commission).

### Additional changes in this migration

- Alter `learner_course_access` (from `000004_learner_journey.up.sql`, which
  left `entitlement_id` as a bare nullable `UUID` with no FK because Task 6
  hadn't built `entitlements` yet) to add the FK now that it exists:
  `ALTER TABLE learner_course_access ADD CONSTRAINT
  learner_course_access_entitlement_id_fkey FOREIGN KEY (entitlement_id)
  REFERENCES entitlements(id);` — no `ON DELETE` action is specified here
  deliberately (default `NO ACTION`): an entitlement should never be
  hard-deleted while a `learner_course_access` row still points at it; the
  revocation path is always the `entitlements.status = 'revoked'` update,
  not a delete.

### `.down.sql` requirements

Write `db/migrations/000006_commerce.down.sql` as the exact mirror image, in
reverse dependency order, matching the style of
`db/migrations/000004_learner_journey.down.sql` and
`db/migrations/000005_certificate_verification.down.sql` (plain
`DROP TABLE IF EXISTS` / `ALTER TABLE ... DROP CONSTRAINT IF EXISTS`
statements, no `CASCADE` needed if ordered correctly, one statement per
line, tables dropped child-before-parent):

1. `ALTER TABLE learner_course_access DROP CONSTRAINT IF EXISTS learner_course_access_entitlement_id_fkey;`
2. `DROP TABLE IF EXISTS platform_settings;`
3. `DROP TABLE IF EXISTS webhook_events;`
4. `DROP TABLE IF EXISTS payment_audit_trail;`
5. `DROP TABLE IF EXISTS entitlements;`
6. `DROP TABLE IF EXISTS chargebacks;`
7. `DROP TABLE IF EXISTS refunds;`
8. `DROP TABLE IF EXISTS payments;`
9. `DROP TABLE IF EXISTS orders;`
10. `DROP TABLE IF EXISTS commerce_invite_tokens;`
11. `DROP TABLE IF EXISTS discount_codes;`
12. `DROP TABLE IF EXISTS offers;`

### Money-state design notes to preserve in comments

Add a header comment block (like 000004/000005 have) that explains, briefly:
- `org_id` is denormalized onto every child table below `offers` so no RLS
  policy needs a multi-table join to enforce tenant isolation.
- `commission_rate_snapshot` on `orders` exists so that changing
  `platform_settings.commission_percent` later never retroactively changes
  historical order math.
- Entitlements are the single source of truth for course access grants; they
  are only ever created either by the (future) verified-webhook worker path
  or by an audit-logged admin grant — never directly from a browser request,
  per this repo's `CLAUDE.md` non-negotiable rule that "payment/enrollment
  access must only ever be granted after verified provider webhook events,
  never from browser return URLs." This migration only creates the schema
  that makes that rule enforceable; it does not implement the enforcement
  itself (that's the webhook-handler and commerce-handlers tasks).
- `payment_audit_trail` is append-only by omission of UPDATE/DELETE policies
  under `FORCE ROW LEVEL SECURITY`, not by trigger.

## Acceptance criteria

- [ ] `db/migrations/000006_commerce.up.sql` exists and creates all 11 tables listed above (`offers`, `discount_codes`, `commerce_invite_tokens`, `orders`, `payments`, `refunds`, `chargebacks`, `entitlements`, `payment_audit_trail`, `webhook_events`, `platform_settings`) with exactly the columns, types, constraints, and defaults specified.
- [ ] Every org-scoped table has `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE`, denormalized as specified (not requiring a multi-table join to resolve).
- [ ] Every table has `ALTER TABLE ... ENABLE ROW LEVEL SECURITY;` and `ALTER TABLE ... FORCE ROW LEVEL SECURITY;`, including the two platform-wide tables (`webhook_events`, `platform_settings`).
- [ ] All RLS policies use only the existing helper functions (`app_current_user_id()`, `app_current_org_id()`, `app_current_role()`, `app_is_platform_owner()`, `is_org_member()`, `is_org_owner()`) — no new helper functions are introduced unless explicitly called out above (none are).
- [ ] `payment_audit_trail` has no UPDATE or DELETE policy of any kind (append-only enforced via RLS policy omission under FORCE ROW LEVEL SECURITY, not a trigger).
- [ ] `platform_settings` SELECT/UPDATE are restricted to `app_is_platform_owner()`, and the migration seeds exactly one row with `commission_percent = 10`.
- [ ] `webhook_events.razorpay_event_id` has a `UNIQUE NOT NULL` constraint (the idempotency mechanism the webhook handler task will rely on).
- [ ] `learner_course_access` gets a new `FOREIGN KEY (entitlement_id) REFERENCES entitlements(id)` constraint added via `ALTER TABLE`.
- [ ] All money columns are `NUMERIC(12,2)`; all percentage columns are `NUMERIC(5,2)`, matching the existing `grade_percentage NUMERIC(5,2)` convention.
- [ ] `db/migrations/000006_commerce.down.sql` exists and fully reverses the up migration in correct dependency order (drops constraint first, then tables child-before-parent).
- [ ] Running `make migrate-up` followed by `make migrate-down` (or the equivalent `migrate -path db/migrations -database "$DATABASE_URL" up` / `down 1`) against a local Supabase/Postgres instance succeeds with no errors in both directions, and running `up` again after `down` is idempotent (no leftover objects from a partial down).
- [ ] The `.up.sql` file has a header comment block explaining the RLS/denormalization pattern, in the same style as the header comments in `000004_learner_journey.up.sql` and `000005_certificate_verification.up.sql`.
- [ ] No application code, repositories, or Go files are touched in this task — this task is migration-only. Repository/model wiring is Task 4 (models-repositories) in this same plan.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
