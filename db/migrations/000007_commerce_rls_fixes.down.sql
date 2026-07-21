DROP POLICY learner_course_access_select ON learner_course_access;
CREATE POLICY learner_course_access_select ON learner_course_access FOR SELECT
  USING (
    learner_id = app_current_user_id()
    OR (is_org_member(learner_course_access.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );

DROP POLICY orders_select ON orders;
CREATE POLICY orders_select ON orders FOR SELECT
  USING (
    orders.learner_id = app_current_user_id()
    OR (is_org_member(orders.org_id) AND app_current_role() IN ('owner', 'teacher'))
  );

DROP POLICY courses_select ON courses;
CREATE POLICY courses_select ON courses FOR SELECT
  USING (is_org_member(courses.org_id));

DROP POLICY entitlements_insert ON entitlements;
CREATE POLICY entitlements_insert ON entitlements FOR INSERT
  WITH CHECK (is_org_member(entitlements.org_id) AND app_current_role() IN ('owner', 'teacher'));
