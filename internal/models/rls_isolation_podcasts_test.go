package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// TestRLS_PodcastShowsCrossOrgIsolation proves podcast_shows (migration
// 000011) is tenant-scoped: a member of one org cannot read or write another
// org's shows, and only teachers/owners can author them.
func TestRLS_PodcastShowsCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, orgA, "learner")

	// Seed a show in org A directly.
	var showA string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO podcast_shows (org_id, slug, title, is_published) VALUES ($1, 'show-a', 'Show A', true) RETURNING id`,
		orgA).Scan(&showA))

	// A learner in org A can read org A's show.
	txLA, err := dbctx.Begin(ctx, pool, learnerA, orgA, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txLA.Tx.QueryRow(ctx, `SELECT count(*) FROM podcast_shows WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 1, count, "an org member must read their org's podcast shows")
	// ...but a learner may NOT author a show (insert requires teacher).
	_, err = txLA.Tx.Exec(ctx, `INSERT INTO podcast_shows (org_id, slug, title) VALUES ($1, 'x', 'X')`, orgA)
	require.Error(t, err, "a learner must not be able to author a podcast show")
	require.NoError(t, txLA.Rollback(ctx))

	// The owner of org B must not see org A's show.
	txOB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	require.NoError(t, txOB.Tx.QueryRow(ctx, `SELECT count(*) FROM podcast_shows WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 0, count, "an org must not see another org's podcast shows")
	require.NoError(t, txOB.Rollback(ctx))

	// The owner of org A can author (teacher/owner privilege).
	txOA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	defer txOA.Rollback(ctx)
	_, err = txOA.Tx.Exec(ctx, `INSERT INTO podcast_shows (org_id, slug, title) VALUES ($1, 'owner-show', 'Owner Show')`, orgA)
	require.NoError(t, err, "an owner must be able to author a podcast show")
}

// TestRLS_PodcastProgressLearnerOwnership proves podcast_progress is
// learner-owned: a learner reads/writes only their own progress, another
// learner cannot see it, and an owner/teacher retains oversight visibility.
func TestRLS_PodcastProgressLearnerOwnership(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, org, "learner")
	seedMember(t, admin, learnerB, org, "learner")

	// Seed a published show + episode via admin.
	var showID, episodeID string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO podcast_shows (org_id, slug, title, is_published) VALUES ($1, 'show', 'Show', true) RETURNING id`,
		org).Scan(&showID))
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO podcast_episodes (show_id, org_id, title, audio_url, is_published, published_at)
		 VALUES ($1, $2, 'Ep 1', 'https://cdn/ep1.mp3', true, now()) RETURNING id`,
		showID, org).Scan(&episodeID))

	insert := `INSERT INTO podcast_progress (org_id, episode_id, learner_id, position_seconds) VALUES ($1, $2, $3, 42)`

	// Learner A records and reads back their own progress.
	txA, err := dbctx.Begin(ctx, pool, learnerA, org, "learner")
	require.NoError(t, err)
	_, err = txA.Tx.Exec(ctx, insert, org, episodeID, learnerA)
	require.NoError(t, err, "a learner must be able to record their own podcast progress")
	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM podcast_progress WHERE episode_id = $1`, episodeID).Scan(&count))
	require.Equal(t, 1, count, "a learner must read their own podcast progress")
	require.NoError(t, txA.Rollback(ctx))

	// Persist a learner-A progress row so others have something to (not) see.
	_, err = admin.Exec(ctx, insert, org, episodeID, learnerA)
	require.NoError(t, err)

	// Learner B must not see learner A's progress, and cannot insert on A's behalf.
	txB, err := dbctx.Begin(ctx, pool, learnerB, org, "learner")
	require.NoError(t, err)
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM podcast_progress WHERE episode_id = $1`, episodeID).Scan(&count))
	require.Equal(t, 0, count, "a learner must not read another learner's podcast progress")
	_, err = txB.Tx.Exec(ctx, insert, org, episodeID, learnerA)
	require.Error(t, err, "a learner must not record progress on another learner's behalf")
	require.NoError(t, txB.Rollback(ctx))

	// The owner sees learner A's progress for oversight.
	txO, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txO.Rollback(ctx)
	require.NoError(t, txO.Tx.QueryRow(ctx, `SELECT count(*) FROM podcast_progress WHERE episode_id = $1`, episodeID).Scan(&count))
	require.Equal(t, 1, count, "an owner must see learners' podcast progress for oversight")
}
