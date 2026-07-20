package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// seedMembership adds a membership row for an already-created user/org
// pair with an arbitrary role, bypassing create_organization()/invitation
// acceptance the same way seedOrgWithOwner does.
func seedMembership(t *testing.T, admin *pgxpool.Pool, userID, orgID, role string) {
	t.Helper()
	_, err := admin.Exec(context.Background(),
		`INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, $3)`,
		userID, orgID, role)
	require.NoError(t, err)
}

// learnerCourseFixture is a minimal course/chapter/lesson/quiz-block/
// assignment-block tree, seeded as admin, for tests that need real FK
// targets for the learner-journey tables.
type learnerCourseFixture struct {
	courseID          string
	lessonID          string
	quizBlockID       string
	assignmentBlockID string
}

func seedLearnerCourseFixture(t *testing.T, admin *pgxpool.Pool, orgID, createdBy string) learnerCourseFixture {
	t.Helper()
	ctx := context.Background()

	courseID := uuid.NewString()
	chapterID := uuid.NewString()
	lessonID := uuid.NewString()
	quizBlockID := uuid.NewString()
	assignmentBlockID := uuid.NewString()

	_, err := admin.Exec(ctx, `INSERT INTO courses (id, org_id, title, created_by) VALUES ($1, $2, 'Course', $3)`, courseID, orgID, createdBy)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO chapters (id, course_id, org_id, title, sort_order, created_by) VALUES ($1, $2, $3, 'Chapter', 1.0, $4)`, chapterID, courseID, orgID, createdBy)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO lessons (id, chapter_id, course_id, org_id, title, sort_order, created_by) VALUES ($1, $2, $3, $4, 'Lesson', 1.0, $5)`, lessonID, chapterID, courseID, orgID, createdBy)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO blocks (id, lesson_id, course_id, org_id, type, content, sort_order, created_by) VALUES ($1, $2, $3, $4, 'quiz', '{"questions":[]}', 1.0, $5)`, quizBlockID, lessonID, courseID, orgID, createdBy)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO blocks (id, lesson_id, course_id, org_id, type, content, sort_order, created_by) VALUES ($1, $2, $3, $4, 'assignment', '{}', 2.0, $5)`, assignmentBlockID, lessonID, courseID, orgID, createdBy)
	require.NoError(t, err)

	return learnerCourseFixture{
		courseID:          courseID,
		lessonID:          lessonID,
		quizBlockID:       quizBlockID,
		assignmentBlockID: assignmentBlockID,
	}
}

// countAs opens a fresh RLS-scoped transaction for (userID, orgID, role)
// and returns COUNT(*) FROM table WHERE id = id — rolling the transaction
// back afterwards so it never leaks state between assertions.
func countAs(t *testing.T, pool *pgxpool.Pool, userID, orgID, role, table, idColumn, id string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := dbctx.Begin(ctx, pool, userID, orgID, role)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	var count int
	require.NoError(t, tx.Tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+idColumn+` = $1`, id).Scan(&count))
	return count
}

// TestRLS_LearnerToLearnerIsolation_SameOrg proves the learner-journey
// RLS policies are learner_id-scoped, not just org_id-scoped: two
// learners in the SAME organization must not be able to read or write
// each other's rows, even though both pass the is_org_member(org_id)
// check that governs cross-org isolation.
func TestRLS_LearnerToLearnerIsolation_SameOrg(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner1 := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner2 := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, owner, "org-a-"+uuid.NewString())
	seedMembership(t, admin, learner1, orgA, "learner")
	seedMembership(t, admin, learner2, orgA, "learner")

	fx := seedLearnerCourseFixture(t, admin, orgA, owner)

	// Seed learner2's own progress and quiz-attempt rows directly (admin
	// bypass) so learner1 has something to fail to see.
	_, err := admin.Exec(ctx, `INSERT INTO learner_lesson_progress (org_id, learner_id, lesson_id, course_id, watch_percentage) VALUES ($1, $2, $3, $4, 42)`,
		orgA, learner2, fx.lessonID, fx.courseID)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO learner_quiz_attempt (org_id, learner_id, quiz_block_id, attempt_number, answers_json) VALUES ($1, $2, $3, 1, '{}')`,
		orgA, learner2, fx.quizBlockID)
	require.NoError(t, err)

	txL1, err := dbctx.Begin(ctx, pool, learner1, orgA, "learner")
	require.NoError(t, err)

	var count int
	require.NoError(t, txL1.Tx.QueryRow(ctx, `SELECT count(*) FROM learner_lesson_progress WHERE learner_id = $1`, learner2).Scan(&count))
	require.Equal(t, 0, count, "learner1 must not see learner2's progress row in the same org")

	require.NoError(t, txL1.Tx.QueryRow(ctx, `SELECT count(*) FROM learner_quiz_attempt WHERE learner_id = $1`, learner2).Scan(&count))
	require.Equal(t, 0, count, "learner1 must not see learner2's quiz attempt in the same org")

	// Learner1 cannot UPDATE learner2's progress row.
	tag, err := txL1.Tx.Exec(ctx, `UPDATE learner_lesson_progress SET watch_percentage = 99 WHERE learner_id = $1 AND lesson_id = $2`, learner2, fx.lessonID)
	require.NoError(t, err)
	require.Equal(t, int64(0), tag.RowsAffected(), "learner1 must not be able to update learner2's progress row")
	require.NoError(t, txL1.Rollback(ctx))

	// Learner1 cannot insert a progress row impersonating learner2 — checked
	// in its own transaction, since a failed statement aborts the rest of
	// whatever Postgres transaction it ran in.
	txL1Insert, err := dbctx.Begin(ctx, pool, learner1, orgA, "learner")
	require.NoError(t, err)
	_, err = txL1Insert.Tx.Exec(ctx, `INSERT INTO learner_lesson_progress (org_id, learner_id, lesson_id, course_id, watch_percentage) VALUES ($1, $2, $3, $4, 1)`,
		orgA, learner2, fx.lessonID, fx.courseID)
	require.Error(t, err, "learner1 must not be able to insert a progress row as learner2")
	require.NoError(t, txL1Insert.Rollback(ctx))

	// Sanity: learner1 CAN see/write their own row, proving the failures
	// above are learner-scoping, not a broken table/policy.
	txL1Own, err := dbctx.Begin(ctx, pool, learner1, orgA, "learner")
	require.NoError(t, err)
	defer txL1Own.Rollback(ctx)
	_, err = txL1Own.Tx.Exec(ctx, `INSERT INTO learner_lesson_progress (org_id, learner_id, lesson_id, course_id, watch_percentage) VALUES ($1, $2, $3, $4, 10)`,
		orgA, learner1, fx.lessonID, fx.courseID)
	require.NoError(t, err, "learner1 must be able to insert their own progress row")
	require.NoError(t, txL1Own.Tx.QueryRow(ctx, `SELECT count(*) FROM learner_lesson_progress WHERE learner_id = $1`, learner1).Scan(&count))
	require.Equal(t, 1, count, "learner1 must see their own progress row")
}

// TestRLS_LearnerJourneyCrossOrgIsolation proves org B cannot read org A's
// rows in any of the five learner-scoped learner-journey tables, even by
// direct ID, mirroring TestRLS_CourseDomainIsolation's pattern for Task 4.
func TestRLS_LearnerJourneyCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerB := seedUser(t, admin, uuid.NewString()+"@example.com")

	orgA := seedOrgWithOwner(t, admin, ownerA, "org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-b-"+uuid.NewString())
	seedMembership(t, admin, learnerA, orgA, "learner")
	seedMembership(t, admin, learnerB, orgB, "learner")

	fx := seedLearnerCourseFixture(t, admin, orgA, ownerA)

	accessID := uuid.NewString()
	_, err := admin.Exec(ctx, `INSERT INTO learner_course_access (id, org_id, learner_id, course_id) VALUES ($1, $2, $3, $4)`,
		accessID, orgA, learnerA, fx.courseID)
	require.NoError(t, err)

	_, err = admin.Exec(ctx, `INSERT INTO learner_lesson_progress (org_id, learner_id, lesson_id, course_id, watch_percentage) VALUES ($1, $2, $3, $4, 50)`,
		orgA, learnerA, fx.lessonID, fx.courseID)
	require.NoError(t, err)

	scoreID := uuid.NewString()
	_, err = admin.Exec(ctx, `INSERT INTO learner_quiz_score (id, org_id, learner_id, quiz_block_id, attempt_number, score_earned, score_max, percentage, passed) VALUES ($1, $2, $3, $4, 1, 1, 1, 100, true)`,
		scoreID, orgA, learnerA, fx.quizBlockID)
	require.NoError(t, err)

	submissionID := uuid.NewString()
	_, err = admin.Exec(ctx, `INSERT INTO learner_assignment_submission (id, org_id, learner_id, assignment_block_id, submission_number, file_path) VALUES ($1, $2, $3, $4, 1, 'f.pdf')`,
		submissionID, orgA, learnerA, fx.assignmentBlockID)
	require.NoError(t, err)

	certID := uuid.NewString()
	_, err = admin.Exec(ctx, `INSERT INTO learner_certificate (id, org_id, learner_id, course_id, certificate_id, pdf_storage_path) VALUES ($1, $2, $3, $4, $5, 'cert.pdf')`,
		certID, orgA, learnerA, fx.courseID, "CERT-"+uuid.NewString())
	require.NoError(t, err)

	for _, role := range []string{"learner", "owner"} {
		userB := learnerB
		if role == "owner" {
			userB = ownerB
		}

		require.Equal(t, 0, countAs(t, pool, userB, orgB, role, "learner_course_access", "id", accessID),
			"org B (%s) must not see org A's learner_course_access row", role)
		require.Equal(t, 0, countAs(t, pool, userB, orgB, role, "learner_lesson_progress", "lesson_id", fx.lessonID),
			"org B (%s) must not see org A's learner_lesson_progress row", role)
		require.Equal(t, 0, countAs(t, pool, userB, orgB, role, "learner_quiz_score", "id", scoreID),
			"org B (%s) must not see org A's learner_quiz_score row", role)
		require.Equal(t, 0, countAs(t, pool, userB, orgB, role, "learner_assignment_submission", "id", submissionID),
			"org B (%s) must not see org A's learner_assignment_submission row", role)
		require.Equal(t, 0, countAs(t, pool, userB, orgB, role, "learner_certificate", "id", certID),
			"org B (%s) must not see org A's learner_certificate row", role)
	}
}

// TestRLS_LearnerJourney_TeacherOrgWideRead proves the org-wide read
// policy actually works (not just that it's absent for other users): a
// teacher inside org A can read every learner's progress/score/submission
// rows in their own org, which grading queues and progress dashboards
// depend on.
func TestRLS_LearnerJourney_TeacherOrgWideRead(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	teacher := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner1 := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner2 := seedUser(t, admin, uuid.NewString()+"@example.com")

	orgA := seedOrgWithOwner(t, admin, owner, "org-a-"+uuid.NewString())
	seedMembership(t, admin, teacher, orgA, "teacher")
	seedMembership(t, admin, learner1, orgA, "learner")
	seedMembership(t, admin, learner2, orgA, "learner")

	fx := seedLearnerCourseFixture(t, admin, orgA, owner)

	for _, learnerID := range []string{learner1, learner2} {
		_, err := admin.Exec(ctx, `INSERT INTO learner_lesson_progress (org_id, learner_id, lesson_id, course_id, watch_percentage) VALUES ($1, $2, $3, $4, 77)`,
			orgA, learnerID, fx.lessonID, fx.courseID)
		require.NoError(t, err)

		_, err = admin.Exec(ctx, `INSERT INTO learner_quiz_score (org_id, learner_id, quiz_block_id, attempt_number, score_earned, score_max, percentage, passed) VALUES ($1, $2, $3, 1, 1, 1, 100, true)`,
			orgA, learnerID, fx.quizBlockID)
		require.NoError(t, err)

		_, err = admin.Exec(ctx, `INSERT INTO learner_assignment_submission (org_id, learner_id, assignment_block_id, submission_number, file_path) VALUES ($1, $2, $3, 1, 'f.pdf')`,
			orgA, learnerID, fx.assignmentBlockID)
		require.NoError(t, err)
	}

	txTeacher, err := dbctx.Begin(ctx, pool, teacher, orgA, "teacher")
	require.NoError(t, err)
	defer txTeacher.Rollback(ctx)

	var count int
	require.NoError(t, txTeacher.Tx.QueryRow(ctx, `SELECT count(*) FROM learner_lesson_progress WHERE course_id = $1`, fx.courseID).Scan(&count))
	require.Equal(t, 2, count, "teacher must see both learners' progress rows in their own org")

	require.NoError(t, txTeacher.Tx.QueryRow(ctx, `SELECT count(*) FROM learner_quiz_score WHERE quiz_block_id = $1`, fx.quizBlockID).Scan(&count))
	require.Equal(t, 2, count, "teacher must see both learners' quiz scores in their own org")

	require.NoError(t, txTeacher.Tx.QueryRow(ctx, `SELECT count(*) FROM learner_assignment_submission WHERE assignment_block_id = $1`, fx.assignmentBlockID).Scan(&count))
	require.Equal(t, 2, count, "teacher must see both learners' assignment submissions in their own org")
}

// TestRLS_LearnerAssignmentGrade_ExistsJoin exercises the one policy
// shape that has no learner_id column of its own: learner_assignment_grade
// expresses learner read-access via an EXISTS join back to the owning
// submission's learner_id, and this must correctly scope reads to "my
// grade" rather than "any grade in my org".
func TestRLS_LearnerAssignmentGrade_ExistsJoin(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	teacher := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner1 := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner2 := seedUser(t, admin, uuid.NewString()+"@example.com")

	orgA := seedOrgWithOwner(t, admin, owner, "org-a-"+uuid.NewString())
	seedMembership(t, admin, teacher, orgA, "teacher")
	seedMembership(t, admin, learner1, orgA, "learner")
	seedMembership(t, admin, learner2, orgA, "learner")

	fx := seedLearnerCourseFixture(t, admin, orgA, owner)

	submission1 := uuid.NewString()
	submission2 := uuid.NewString()
	_, err := admin.Exec(ctx, `INSERT INTO learner_assignment_submission (id, org_id, learner_id, assignment_block_id, submission_number, file_path) VALUES ($1, $2, $3, $4, 1, 'f1.pdf')`,
		submission1, orgA, learner1, fx.assignmentBlockID)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO learner_assignment_submission (id, org_id, learner_id, assignment_block_id, submission_number, file_path) VALUES ($1, $2, $3, $4, 1, 'f2.pdf')`,
		submission2, orgA, learner2, fx.assignmentBlockID)
	require.NoError(t, err)

	grade1 := uuid.NewString()
	grade2 := uuid.NewString()
	_, err = admin.Exec(ctx, `INSERT INTO learner_assignment_grade (id, org_id, submission_id, grade_percentage, graded_by_teacher_id) VALUES ($1, $2, $3, 90, $4)`,
		grade1, orgA, submission1, teacher)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO learner_assignment_grade (id, org_id, submission_id, grade_percentage, graded_by_teacher_id) VALUES ($1, $2, $3, 60, $4)`,
		grade2, orgA, submission2, teacher)
	require.NoError(t, err)

	// Learner1 can read their own grade (joined via their submission)...
	require.Equal(t, 1, countAs(t, pool, learner1, orgA, "learner", "learner_assignment_grade", "id", grade1),
		"learner1 must be able to read their own grade")
	// ...but not learner2's grade in the same org.
	require.Equal(t, 0, countAs(t, pool, learner1, orgA, "learner", "learner_assignment_grade", "id", grade2),
		"learner1 must not be able to read learner2's grade")

	// Teacher can read both (org-wide read policy).
	require.Equal(t, 1, countAs(t, pool, teacher, orgA, "teacher", "learner_assignment_grade", "id", grade1),
		"teacher must be able to read learner1's grade")
	require.Equal(t, 1, countAs(t, pool, teacher, orgA, "teacher", "learner_assignment_grade", "id", grade2),
		"teacher must be able to read learner2's grade")
}

// TestRLS_CourseAnnouncement_ReadAllWriteTeacherOnly exercises
// course_announcement's divergent shape: no learner_id column at all,
// readable by every org member, but only teacher/owner may create one.
// This second half isn't covered by any HTTP-level test (Stage 7's
// handler only tests the notification fan-out, not the RLS-enforced
// create permission), so it's verified here at the model/RLS level.
func TestRLS_CourseAnnouncement_ReadAllWriteTeacherOnly(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	teacher := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner := seedUser(t, admin, uuid.NewString()+"@example.com")

	orgA := seedOrgWithOwner(t, admin, owner, "org-a-"+uuid.NewString())
	seedMembership(t, admin, teacher, orgA, "teacher")
	seedMembership(t, admin, learner, orgA, "learner")

	fx := seedLearnerCourseFixture(t, admin, orgA, owner)

	annID := uuid.NewString()
	_, err := admin.Exec(ctx, `INSERT INTO course_announcement (id, org_id, course_id, title, body, created_by) VALUES ($1, $2, $3, 'Hi', 'body', $4)`,
		annID, orgA, fx.courseID, owner)
	require.NoError(t, err)

	// Any org member, including a learner, can read it.
	require.Equal(t, 1, countAs(t, pool, learner, orgA, "learner", "course_announcement", "id", annID),
		"a learner must be able to read a course announcement in their own org")
	require.Equal(t, 1, countAs(t, pool, teacher, orgA, "teacher", "course_announcement", "id", annID),
		"a teacher must be able to read a course announcement in their own org")

	// A learner cannot create one.
	txLearner, err := dbctx.Begin(ctx, pool, learner, orgA, "learner")
	require.NoError(t, err)
	defer txLearner.Rollback(ctx)
	_, err = txLearner.Tx.Exec(ctx, `INSERT INTO course_announcement (org_id, course_id, title, body, created_by) VALUES ($1, $2, 'Nope', 'body', $3)`,
		orgA, fx.courseID, learner)
	require.Error(t, err, "a learner must not be able to create a course announcement")

	// A teacher can create one.
	txTeacher, err := dbctx.Begin(ctx, pool, teacher, orgA, "teacher")
	require.NoError(t, err)
	defer txTeacher.Rollback(ctx)
	_, err = txTeacher.Tx.Exec(ctx, `INSERT INTO course_announcement (org_id, course_id, title, body, created_by) VALUES ($1, $2, 'Yes', 'body', $3)`,
		orgA, fx.courseID, teacher)
	require.NoError(t, err, "a teacher must be able to create a course announcement")
}
