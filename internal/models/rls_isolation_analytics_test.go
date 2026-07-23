package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// TestRLS_AnalyticsEventsVisibility proves a plain learner can emit
// analytics events (INSERT) but cannot read the raw event stream — only
// an owner/teacher (is_org_teacher) can, per migration 000009's
// analytics_events_select policy.
func TestRLS_AnalyticsEventsVisibility(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learner := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, learner, org, "learner")

	txL, err := dbctx.Begin(ctx, pool, learner, org, "learner")
	require.NoError(t, err)
	_, err = txL.Tx.Exec(ctx, `INSERT INTO analytics_events (org_id, event_type, actor_user_id) VALUES ($1, 'course_view', $2)`, org, learner)
	require.NoError(t, err, "a learner must be able to emit their own analytics event")

	var count int
	require.NoError(t, txL.Tx.QueryRow(ctx, `SELECT count(*) FROM analytics_events WHERE org_id = $1`, org).Scan(&count))
	require.Equal(t, 0, count, "a plain learner must not read the raw analytics event stream")
	require.NoError(t, txL.Rollback(ctx))

	txOwner, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txOwner.Rollback(ctx)

	_, err = txOwner.Tx.Exec(ctx, `INSERT INTO analytics_events (org_id, event_type, actor_user_id) VALUES ($1, 'course_view', $2)`, org, learner)
	require.NoError(t, err)

	require.NoError(t, txOwner.Tx.QueryRow(ctx, `SELECT count(*) FROM analytics_events WHERE org_id = $1`, org).Scan(&count))
	require.Equal(t, 1, count, "an owner must see the org's analytics event stream")
}

// TestRLS_AnalyticsEventsCrossOrgIsolation proves org A cannot read org
// B's analytics events even as an owner.
func TestRLS_AnalyticsEventsCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-b-"+uuid.NewString())

	_, err := admin.Exec(ctx, `INSERT INTO analytics_events (org_id, event_type, actor_user_id) VALUES ($1, 'course_view', $2)`, orgB, ownerB)
	require.NoError(t, err)

	txA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	defer txA.Rollback(ctx)

	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM analytics_events WHERE org_id = $1`, orgB).Scan(&count))
	require.Equal(t, 0, count, "org A's owner must not see org B's analytics events")
}

// TestRLS_OrgPagesPublicVisibility proves a published org_pages row is
// readable with NO session context at all (the anonymous public-site
// path), while an unpublished draft is not.
func TestRLS_OrgPagesPublicVisibility(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())

	_, err := admin.Exec(ctx,
		`INSERT INTO org_pages (org_id, slug, title, content_html, is_published, created_by) VALUES ($1, 'home', 'Home', '<p>hi</p>', true, $2)`,
		org, owner)
	require.NoError(t, err)
	_, err = admin.Exec(ctx,
		`INSERT INTO org_pages (org_id, slug, title, content_html, is_published, created_by) VALUES ($1, 'draft', 'Draft', '<p>wip</p>', false, $2)`,
		org, owner)
	require.NoError(t, err)

	// pool here has no app.current_user_id/app.current_org_id set at all —
	// exactly the anonymous public-site request path.
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM org_pages WHERE org_id = $1 AND slug = 'home'`, org).Scan(&count))
	require.Equal(t, 1, count, "a published org page must be visible with no session context")

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM org_pages WHERE org_id = $1 AND slug = 'draft'`, org).Scan(&count))
	require.Equal(t, 0, count, "an unpublished draft page must not be visible anonymously")
}

// TestRLS_ResolveOrgByDomainOnlyReturnsVerified proves
// resolve_org_by_domain() only ever resolves a domain that has actually
// passed verification, even to an anonymous caller with no session.
func TestRLS_ResolveOrgByDomainOnlyReturnsVerified(t *testing.T) {
	admin := testutil.AdminDB(t)
	pool := testutil.DB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	domain := uuid.NewString() + ".example.com"

	_, err := admin.Exec(ctx, `UPDATE organizations SET custom_domain = $1, domain_verification_token = 'tok' WHERE id = $2`, domain, org)
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM resolve_org_by_domain($1)`, domain).Scan(&count))
	require.Equal(t, 0, count, "an unverified domain must not resolve")

	_, err = admin.Exec(ctx, `UPDATE organizations SET domain_verified_at = now() WHERE id = $1`, org)
	require.NoError(t, err)

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM resolve_org_by_domain($1)`, domain).Scan(&count))
	require.Equal(t, 1, count, "a verified domain must resolve")
}
