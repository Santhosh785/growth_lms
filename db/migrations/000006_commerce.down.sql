ALTER TABLE learner_course_access DROP CONSTRAINT IF EXISTS learner_course_access_entitlement_id_fkey;

DROP TABLE IF EXISTS platform_settings;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS payment_audit_trail;
DROP TABLE IF EXISTS entitlements;
DROP TABLE IF EXISTS chargebacks;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS payments;

-- commerce_invite_tokens.used_by_order_id_fkey (added via a trailing ALTER
-- in the .up.sql, after orders was created) must be dropped before orders
-- itself, or Postgres refuses to drop orders while still referenced.
ALTER TABLE commerce_invite_tokens DROP CONSTRAINT IF EXISTS commerce_invite_tokens_used_by_order_id_fkey;

DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS commerce_invite_tokens;
DROP TABLE IF EXISTS discount_codes;
DROP TABLE IF EXISTS offers;
