package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/notify"
	"growth-lms/internal/notify/notifytest"
	"growth-lms/internal/testutil"
)

// buildCommunityDeps wires a communityDeps over the admin pool (worker trust
// boundary) and a fake email client.
func buildCommunityDeps(pool *pgxpool.Pool, email notify.EmailClient) *communityDeps {
	return &communityDeps{
		pool:          pool,
		profiles:      models.NewProfileRepo(),
		memberships:   models.NewMembershipRepo(),
		notifications: models.NewNotificationRepo(),
		prefs:         models.NewNotificationPreferenceRepo(),
		unsub:         models.NewUnsubscribeTokenRepo(),
		email:         email,
		baseURL:       "http://localhost:8080",
	}
}

func seedUserRow(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1, $2)`, id, id+"@example.com")
	require.NoError(t, err)
}

func seedOrgRow(t *testing.T, pool *pgxpool.Pool, ownerID string) string {
	t.Helper()
	orgID := uuid.NewString()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES ($1, $2, $2, $3)`, orgID, "org-"+orgID, ownerID)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		`INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'owner')`, ownerID, orgID)
	require.NoError(t, err)
	return orgID
}

func unreadCount(t *testing.T, pool *pgxpool.Pool, recipientID string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE recipient_id = $1`, recipientID).Scan(&n))
	return n
}

func mentionTask(t *testing.T, orgID, recipientID, actorID string) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(NotifyMentionPayload{
		OrgID: orgID, RecipientID: recipientID, ActorID: actorID, ActorName: "Alice",
		ThreadID: uuid.NewString(), ThreadTitle: "Welcome", PostID: uuid.NewString(),
		Preview: "hello", LinkPath: "/community/threads/x",
	})
	require.NoError(t, err)
	return asynq.NewTask(TypeNotifyMention, data)
}

// TestCommunityDispatch_InAppAlways_EmailGated is the core acceptance test:
// the in-app row is ALWAYS written; email is sent only when both the master
// opt-out and the per-category preference allow it.
func TestCommunityDispatch_InAppAlways_EmailGated(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	t.Run("opted in: in-app + email", func(t *testing.T) {
		owner := uuid.NewString()
		recipient := uuid.NewString()
		seedUserRow(t, pool, owner)
		seedUserRow(t, pool, recipient)
		org := seedOrgRow(t, pool, owner)

		fake := &notifytest.FakeEmailClient{}
		cd := buildCommunityDeps(pool, fake)
		require.NoError(t, handleNotifyMention(cd)(ctx, mentionTask(t, org, recipient, owner)))

		require.Equal(t, 1, unreadCount(t, pool, recipient), "in-app row must be written")
		require.Equal(t, 1, fake.Count(), "opted-in recipient must be emailed")
	})

	t.Run("master opt-out: in-app only", func(t *testing.T) {
		owner := uuid.NewString()
		recipient := uuid.NewString()
		seedUserRow(t, pool, owner)
		seedUserRow(t, pool, recipient)
		org := seedOrgRow(t, pool, owner)
		_, err := pool.Exec(ctx, `UPDATE profiles SET notification_opt_out = true WHERE id = $1`, recipient)
		require.NoError(t, err)

		fake := &notifytest.FakeEmailClient{}
		cd := buildCommunityDeps(pool, fake)
		require.NoError(t, handleNotifyMention(cd)(ctx, mentionTask(t, org, recipient, owner)))

		require.Equal(t, 1, unreadCount(t, pool, recipient), "in-app row still written on opt-out")
		require.Equal(t, 0, fake.Count(), "master opt-out must suppress email")
	})

	t.Run("per-category disabled: in-app only", func(t *testing.T) {
		owner := uuid.NewString()
		recipient := uuid.NewString()
		seedUserRow(t, pool, owner)
		seedUserRow(t, pool, recipient)
		org := seedOrgRow(t, pool, owner)
		_, err := pool.Exec(ctx,
			`INSERT INTO notification_preferences (user_id, org_id, category, email_enabled) VALUES ($1, $2, 'mentions', false)`, recipient, org)
		require.NoError(t, err)

		fake := &notifytest.FakeEmailClient{}
		cd := buildCommunityDeps(pool, fake)
		require.NoError(t, handleNotifyMention(cd)(ctx, mentionTask(t, org, recipient, owner)))

		require.Equal(t, 1, unreadCount(t, pool, recipient), "in-app row still written when email category off")
		require.Equal(t, 0, fake.Count(), "per-category disable must suppress email")
	})
}

// TestBroadcastFanOut proves a broadcast writes one in-app row per member.
func TestBroadcastFanOut(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	owner := uuid.NewString()
	m1 := uuid.NewString()
	m2 := uuid.NewString()
	seedUserRow(t, pool, owner)
	seedUserRow(t, pool, m1)
	seedUserRow(t, pool, m2)
	org := seedOrgRow(t, pool, owner)
	_, err := pool.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'learner'), ($3, $2, 'learner')`, m1, org, m2)
	require.NoError(t, err)

	fake := &notifytest.FakeEmailClient{}
	cd := buildCommunityDeps(pool, fake)
	data, err := json.Marshal(NotifyBroadcastPayload{OrgID: org, ActorID: owner, Title: "Notice", Body: "Read this"})
	require.NoError(t, err)
	require.NoError(t, handleNotifyBroadcast(cd)(ctx, asynq.NewTask(TypeNotifyBroadcast, data)))

	// owner + 2 learners = 3 in-app rows.
	require.Equal(t, 1, unreadCount(t, pool, owner))
	require.Equal(t, 1, unreadCount(t, pool, m1))
	require.Equal(t, 1, unreadCount(t, pool, m2))
	require.Equal(t, 3, fake.Count(), "broadcast emails every opted-in member")
}

// TestUnsubscribeResolution proves a token flips the per-category preference
// and is idempotent.
func TestUnsubscribeResolution(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	owner := uuid.NewString()
	user := uuid.NewString()
	seedUserRow(t, pool, owner)
	seedUserRow(t, pool, user)
	org := seedOrgRow(t, pool, owner)

	unsub := models.NewUnsubscribeTokenRepo()
	prefs := models.NewNotificationPreferenceRepo()
	token, err := unsub.NewToken()
	require.NoError(t, err)
	cat := "mentions"
	require.NoError(t, unsub.Create(ctx, pool, token, user, &org, &cat))

	// Before: opted in by default.
	enabled, err := prefs.IsEmailEnabled(ctx, pool, user, org, "mentions")
	require.NoError(t, err)
	require.True(t, enabled)

	// Resolve flips it off and returns the user.
	got, err := unsub.Resolve(ctx, pool, token)
	require.NoError(t, err)
	require.Equal(t, user, got)

	enabled, err = prefs.IsEmailEnabled(ctx, pool, user, org, "mentions")
	require.NoError(t, err)
	require.False(t, enabled, "unsubscribe must disable the category email")

	// Idempotent: a second resolve of the used token is a no-op (ErrNotFound).
	_, err = unsub.Resolve(ctx, pool, token)
	require.ErrorIs(t, err, models.ErrNotFound)
}
