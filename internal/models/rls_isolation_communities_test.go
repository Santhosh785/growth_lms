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

// seedMember adds a membership row with an explicit role, for exercising the
// Task 7 moderator/learner RLS branches (is_org_moderator).
func seedMember(t *testing.T, admin *pgxpool.Pool, userID, orgID, role string) {
	t.Helper()
	_, err := admin.Exec(context.Background(),
		`INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, $3)`, userID, orgID, role)
	require.NoError(t, err)
}

func seedThread(t *testing.T, admin *pgxpool.Pool, orgID string, courseID *string, createdBy string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := admin.Exec(context.Background(),
		`INSERT INTO discussion_threads (id, org_id, course_id, title, created_by) VALUES ($1, $2, $3, 'T', $4)`,
		id, orgID, courseID, createdBy)
	require.NoError(t, err)
	return id
}

func seedPost(t *testing.T, admin *pgxpool.Pool, orgID, threadID, authorID string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := admin.Exec(context.Background(),
		`INSERT INTO discussion_posts (id, org_id, thread_id, author_id, body) VALUES ($1, $2, $3, $4, 'hi')`,
		id, orgID, threadID, authorID)
	require.NoError(t, err)
	return id
}

// TestRLS_DiscussionIsolation proves org A cannot see or mutate org B's
// threads and posts, even by direct ID.
func TestRLS_DiscussionIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	userA := seedUser(t, admin, uuid.NewString()+"@example.com")
	userB := seedUser(t, admin, uuid.NewString()+"@example.com")
	_ = seedOrgWithOwner(t, admin, userA, "org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, userB, "org-b-"+uuid.NewString())

	threadB := seedThread(t, admin, orgB, nil, userB)
	postB := seedPost(t, admin, orgB, threadB, userB)

	txA, err := dbctx.Begin(ctx, pool, userA, "", "")
	require.NoError(t, err)
	defer txA.Rollback(ctx)

	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM discussion_threads WHERE id = $1`, threadB).Scan(&count))
	require.Equal(t, 0, count, "user A must not see org B's thread")

	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM discussion_posts WHERE id = $1`, postB).Scan(&count))
	require.Equal(t, 0, count, "user A must not see org B's post")

	tag, err := txA.Tx.Exec(ctx, `UPDATE discussion_posts SET body = 'x' WHERE id = $1`, postB)
	require.NoError(t, err)
	require.Equal(t, int64(0), tag.RowsAffected(), "cross-org post UPDATE must affect zero rows")

	tag, err = txA.Tx.Exec(ctx, `DELETE FROM discussion_threads WHERE id = $1`, threadB)
	require.NoError(t, err)
	require.Equal(t, int64(0), tag.RowsAffected(), "cross-org thread DELETE must affect zero rows")
}

// TestRLS_ModeratorCanEditOthersPost is the core moderator-power assertion:
// a plain learner cannot edit/delete another member's post, but a moderator
// of the same org can — enforced in-DB by is_org_moderator, not middleware.
func TestRLS_ModeratorCanEditOthersPost(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	author := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner := seedUser(t, admin, uuid.NewString()+"@example.com")
	moderator := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, author, org, "learner")
	seedMember(t, admin, learner, org, "learner")
	seedMember(t, admin, moderator, org, "moderator")

	thread := seedThread(t, admin, org, nil, author)
	post := seedPost(t, admin, org, thread, author)

	// A non-author learner cannot modify the post.
	txL, err := dbctx.Begin(ctx, pool, learner, org, "learner")
	require.NoError(t, err)
	tag, err := txL.Tx.Exec(ctx, `UPDATE discussion_posts SET status = 'hidden' WHERE id = $1`, post)
	require.NoError(t, err)
	require.Equal(t, int64(0), tag.RowsAffected(), "a non-author learner must not edit another's post")
	require.NoError(t, txL.Rollback(ctx))

	// A moderator of the same org can.
	txM, err := dbctx.Begin(ctx, pool, moderator, org, "moderator")
	require.NoError(t, err)
	tag, err = txM.Tx.Exec(ctx, `UPDATE discussion_posts SET status = 'hidden' WHERE id = $1`, post)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected(), "a moderator must be able to hide another's post (is_org_moderator)")
	require.NoError(t, txM.Rollback(ctx))

	// The author can modify their own post.
	txA, err := dbctx.Begin(ctx, pool, author, org, "learner")
	require.NoError(t, err)
	tag, err = txA.Tx.Exec(ctx, `UPDATE discussion_posts SET body = 'edited' WHERE id = $1`, post)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected(), "an author must be able to edit their own post")
	require.NoError(t, txA.Rollback(ctx))
}

// TestRLS_NotificationRecipientScoping proves a user cannot read another
// user's notifications, even within the same org.
func TestRLS_NotificationRecipientScoping(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	other := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, other, org, "learner")

	_, err := admin.Exec(ctx,
		`INSERT INTO notifications (org_id, recipient_id, type, title) VALUES ($1, $2, 'mention', 'hi')`, org, other)
	require.NoError(t, err)

	txOwner, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txOwner.Rollback(ctx)

	var count int
	require.NoError(t, txOwner.Tx.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE recipient_id = $1`, other).Scan(&count))
	require.Equal(t, 0, count, "even an org owner must not read another user's notifications")
}

// TestRLS_ReportVisibility proves a plain member sees only their own reports
// while a moderator sees the whole open queue.
func TestRLS_ReportVisibility(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	reporter := seedUser(t, admin, uuid.NewString()+"@example.com")
	other := seedUser(t, admin, uuid.NewString()+"@example.com")
	moderator := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, reporter, org, "learner")
	seedMember(t, admin, other, org, "learner")
	seedMember(t, admin, moderator, org, "moderator")

	thread := seedThread(t, admin, org, nil, reporter)
	post := seedPost(t, admin, org, thread, other)
	_, err := admin.Exec(ctx,
		`INSERT INTO content_reports (org_id, post_id, reporter_id, reason) VALUES ($1, $2, $3, 'spam')`, org, post, reporter)
	require.NoError(t, err)

	// A different plain member cannot see the report.
	txOther, err := dbctx.Begin(ctx, pool, other, org, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txOther.Tx.QueryRow(ctx, `SELECT count(*) FROM content_reports WHERE post_id = $1`, post).Scan(&count))
	require.Equal(t, 0, count, "an uninvolved member must not see others' reports")
	require.NoError(t, txOther.Rollback(ctx))

	// The moderator sees the open queue.
	txMod, err := dbctx.Begin(ctx, pool, moderator, org, "moderator")
	require.NoError(t, err)
	defer txMod.Rollback(ctx)
	require.NoError(t, txMod.Tx.QueryRow(ctx, `SELECT count(*) FROM content_reports WHERE post_id = $1`, post).Scan(&count))
	require.Equal(t, 1, count, "a moderator must see the report queue")
}

// TestRLS_PreferenceSelfScoping proves preferences are private to their owner.
func TestRLS_PreferenceSelfScoping(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	other := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, other, org, "learner")

	_, err := admin.Exec(ctx,
		`INSERT INTO notification_preferences (user_id, org_id, category, email_enabled) VALUES ($1, $2, 'mentions', false)`, other, org)
	require.NoError(t, err)

	txOwner, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txOwner.Rollback(ctx)

	var count int
	require.NoError(t, txOwner.Tx.QueryRow(ctx, `SELECT count(*) FROM notification_preferences WHERE user_id = $1`, other).Scan(&count))
	require.Equal(t, 0, count, "a user must not read another user's notification preferences")
}
