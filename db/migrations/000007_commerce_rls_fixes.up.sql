-- Task 6 follow-up: fix two RLS gaps discovered while implementing the
-- commerce-handlers and admin-dashboard tasks (see
-- plans/task-6-implementation/task-6-commerce-handlers.md and
-- task-9-admin-dashboard.md, both of which flagged these in code comments
-- rather than silently working around them at the application layer).
--
-- 1. entitlements_insert (000006_commerce.up.sql) only allowed
--    owner/teacher writes, but the free-offer checkout path
--    (commerce_checkout.go's CreateOrder) runs under the PURCHASING
--    LEARNER's own request context and needs to insert its own
--    zero-payment-order entitlement directly (no webhook involved, since
--    no money moved). Without this, every free-offer purchase fails
--    outright under real RLS. Fix: allow a learner to insert an
--    entitlement row for themselves when it references their own order
--    (order_id IS NOT NULL distinguishes this from an admin grant, which
--    has no order_id and must still go through the owner/teacher branch).
--
-- 2. courses_select, orders_select, and learner_course_access_select
--    (000003/000004/000006) had no app_is_platform_owner() bypass, unlike
--    organizations_select and memberships_select which already have one.
--    This meant the platform-owner cross-org admin dashboard
--    (GET /admin/organizations/:org_slug) would render zero courses,
--    zero orders, and zero enrollments for any org the viewing platform
--    owner isn't personally a member of — defeating the entire purpose of
--    that page. Fix: add the same OR app_is_platform_owner() bypass
--    already used elsewhere in this schema.

DROP POLICY entitlements_insert ON entitlements;
CREATE POLICY entitlements_insert ON entitlements FOR INSERT
  WITH CHECK (
    (is_org_member(entitlements.org_id) AND app_current_role() IN ('owner', 'teacher'))
    OR (entitlements.learner_id = app_current_user_id() AND entitlements.order_id IS NOT NULL)
  );

DROP POLICY courses_select ON courses;
CREATE POLICY courses_select ON courses FOR SELECT
  USING (is_org_member(courses.org_id) OR app_is_platform_owner());

DROP POLICY orders_select ON orders;
CREATE POLICY orders_select ON orders FOR SELECT
  USING (
    orders.learner_id = app_current_user_id()
    OR (is_org_member(orders.org_id) AND app_current_role() IN ('owner', 'teacher'))
    OR app_is_platform_owner()
  );

DROP POLICY learner_course_access_select ON learner_course_access;
CREATE POLICY learner_course_access_select ON learner_course_access FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_course_access.org_id) AND app_current_role() IN ('owner', 'teacher'))
    OR app_is_platform_owner()
  );
