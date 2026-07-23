package quota_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/models"
	"growth-lms/internal/quota"
	"growth-lms/internal/testutil"
)

func ptr(v int64) *int64 { return &v }

// TestLimitUsageExceeded covers the pure cap comparison: uncapped and
// zero/negative limits are never exceeded; a positive limit is exceeded at or
// above the cap.
func TestLimitUsageExceeded(t *testing.T) {
	cases := []struct {
		name  string
		used  int64
		limit *int64
		want  bool
	}{
		{"uncapped nil", 1000, nil, false},
		{"uncapped zero", 1000, ptr(0), false},
		{"under cap", 4, ptr(5), false},
		{"at cap", 5, ptr(5), true},
		{"over cap", 6, ptr(5), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lu := quota.LimitUsage{Used: tc.used, Limit: tc.limit}
			require.Equal(t, tc.want, lu.Exceeded())
		})
	}
}

// TestQuotaReportAndCheck exercises the DB-backed path: an org on a custom
// 2-course plan with one course already created reports 1/2 used and allows one
// more but not two.
func TestQuotaReportAndCheck(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgID := uuid.NewString()
	slug := "org-" + uuid.NewString()
	_, err := admin.Exec(ctx, `INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES ($1, $2, $2, $3)`, orgID, slug, owner)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'owner')`, owner, orgID)
	require.NoError(t, err)

	// A plan capping courses at 2, assigned to the org.
	planID := uuid.NewString()
	_, err = admin.Exec(ctx, `
		INSERT INTO plans (id, code, name, max_courses, is_default, is_active)
		VALUES ($1, $2, 'Cap2', 2, false, true)`, planID, "cap2-"+uuid.NewString())
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `UPDATE organizations SET plan_id = $2 WHERE id = $1`, orgID, planID)
	require.NoError(t, err)

	// One existing course.
	_, err = admin.Exec(ctx, `INSERT INTO courses (org_id, title, created_by) VALUES ($1, 'C1', $2)`, orgID, owner)
	require.NoError(t, err)

	svc := quota.New(models.NewPlanRepo(), models.NewQuotaRepo(), models.NewAIUsageRepo())

	tx, err := dbctx.Begin(ctx, pool, owner, orgID, "owner")
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	report, err := svc.Report(ctx, tx.Tx, orgID, time.Now())
	require.NoError(t, err)
	require.Equal(t, planID, report.Plan.ID)
	var courses quota.LimitUsage
	for _, l := range report.Limits {
		if l.Dimension == quota.DimCourses {
			courses = l
		}
	}
	require.Equal(t, int64(1), courses.Used)
	require.Equal(t, int64(2), *courses.Limit)
	require.False(t, courses.Exceeded())

	// Adding one more course is allowed (1+1 <= 2)...
	ok, _, err := svc.Check(ctx, tx.Tx, orgID, quota.DimCourses, 1, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	// ...but adding two would exceed (1+2 > 2).
	ok, _, err = svc.Check(ctx, tx.Tx, orgID, quota.DimCourses, 2, time.Now())
	require.NoError(t, err)
	require.False(t, ok)
}

// seedUser mirrors the models_test helper (a fresh auth.users row triggers a
// profiles row) but is redeclared here since it lives in a different package.
func seedUser(t *testing.T, admin *pgxpool.Pool, email string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := admin.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1, $2)`, id, email)
	require.NoError(t, err)
	return id
}
