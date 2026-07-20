package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Access statuses, matching the CHECK constraint in
// db/migrations/000004_learner_journey.up.sql.
const (
	AccessStatusActive  = "active"
	AccessStatusRevoked = "revoked"
	AccessStatusExpired = "expired"
)

// LearnerCourseAccess is the real entitlement row: canAccessCourse()
// queries this table rather than deriving access from payment state or
// any other signal (see the migration's header comment). entitlement_id
// is left NULL for free self-enrollment until Task 6 builds a real
// entitlements table.
type LearnerCourseAccess struct {
	ID            string
	OrgID         string
	LearnerID     string
	CourseID      string
	EntitlementID *string
	EnrolledAt    time.Time
	AccessStatus  string
}

type LearnerCourseAccessRepo struct{}

func NewLearnerCourseAccessRepo() *LearnerCourseAccessRepo { return &LearnerCourseAccessRepo{} }

const learnerCourseAccessColumns = `id, org_id, learner_id, course_id, entitlement_id, enrolled_at, access_status`

// Create enrolls a learner into a course with the given entitlement (NULL
// for free self-enrollment). The UNIQUE (learner_id, course_id) constraint
// means a second Create for an already-enrolled learner fails — callers
// (the enroll handler) are expected to check existence first if they need
// idempotent behavior.
func (r *LearnerCourseAccessRepo) Create(ctx context.Context, q Querier, orgID, learnerID, courseID string, entitlementID *string) (*LearnerCourseAccess, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_course_access (org_id, learner_id, course_id, entitlement_id)
		VALUES ($1, $2, $3, $4)
		RETURNING `+learnerCourseAccessColumns, orgID, learnerID, courseID, entitlementID)
	return scanLearnerCourseAccess(row)
}

// Get returns the entitlement row for a (learner, course) pair, or
// ErrNotFound if the learner has never been granted access — this is the
// canAccessCourse() lookup.
func (r *LearnerCourseAccessRepo) Get(ctx context.Context, q Querier, learnerID, courseID string) (*LearnerCourseAccess, error) {
	row := q.QueryRow(ctx, `SELECT `+learnerCourseAccessColumns+` FROM learner_course_access WHERE learner_id = $1 AND course_id = $2`, learnerID, courseID)
	return scanLearnerCourseAccess(row)
}

// SetStatus transitions access_status (e.g. active -> revoked on refund,
// active -> expired on subscription lapse). No side effects beyond the
// column itself — callers own any cascading behavior.
func (r *LearnerCourseAccessRepo) SetStatus(ctx context.Context, q Querier, id, status string) (*LearnerCourseAccess, error) {
	row := q.QueryRow(ctx, `
		UPDATE learner_course_access SET access_status = $2
		WHERE id = $1 RETURNING `+learnerCourseAccessColumns, id, status)
	return scanLearnerCourseAccess(row)
}

func scanLearnerCourseAccess(row pgx.Row) (*LearnerCourseAccess, error) {
	var a LearnerCourseAccess
	if err := row.Scan(&a.ID, &a.OrgID, &a.LearnerID, &a.CourseID, &a.EntitlementID, &a.EnrolledAt, &a.AccessStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner course access: %w", err)
	}
	return &a, nil
}
