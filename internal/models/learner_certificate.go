package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LearnerCertificate is auto-issued when course_completion_rules
// evaluation passes (never learner-triggerable directly — see the
// migration's header comment and grilling-record.md Q6). certificate_id
// is a separate, publicly-shareable identifier (TEXT, UNIQUE) distinct
// from the row's internal id — it's what appears in the public
// verification URL so the internal UUID never needs to be exposed.
type LearnerCertificate struct {
	ID             string
	OrgID          string
	LearnerID      string
	CourseID       string
	CertificateID  string
	IssuedAt       time.Time
	PDFStoragePath string
}

type LearnerCertificateRepo struct{}

func NewLearnerCertificateRepo() *LearnerCertificateRepo { return &LearnerCertificateRepo{} }

const learnerCertificateColumns = `id, org_id, learner_id, course_id, certificate_id, issued_at, pdf_storage_path`

// Create issues a certificate. UNIQUE (learner_id, course_id) means this
// fails if the learner already has one for the course — callers should
// check existence first so re-evaluation of completion rules doesn't
// attempt a duplicate issuance.
func (r *LearnerCertificateRepo) Create(ctx context.Context, q Querier, orgID, learnerID, courseID, certificateID, pdfStoragePath string) (*LearnerCertificate, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO learner_certificate (org_id, learner_id, course_id, certificate_id, pdf_storage_path)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+learnerCertificateColumns, orgID, learnerID, courseID, certificateID, pdfStoragePath)
	return scanLearnerCertificate(row)
}

// Get looks up a certificate by its public certificate_id, for the public
// unauthenticated verification endpoint. Note: since app_current_user_id()
// is NULL for anonymous requests, callers on that path must go through a
// SECURITY DEFINER lookup function (see the migration's header comment)
// rather than this method directly against a normal RLS-scoped connection.
func (r *LearnerCertificateRepo) Get(ctx context.Context, q Querier, certificateID string) (*LearnerCertificate, error) {
	row := q.QueryRow(ctx, `SELECT `+learnerCertificateColumns+` FROM learner_certificate WHERE certificate_id = $1`, certificateID)
	return scanLearnerCertificate(row)
}

// GetByLearnerAndCourse checks whether a learner already holds a
// certificate for a course, used to make certificate issuance idempotent.
func (r *LearnerCertificateRepo) GetByLearnerAndCourse(ctx context.Context, q Querier, learnerID, courseID string) (*LearnerCertificate, error) {
	row := q.QueryRow(ctx, `SELECT `+learnerCertificateColumns+` FROM learner_certificate WHERE learner_id = $1 AND course_id = $2`, learnerID, courseID)
	return scanLearnerCertificate(row)
}

// ListByLearner returns every certificate a learner has earned, for their
// dashboard.
func (r *LearnerCertificateRepo) ListByLearner(ctx context.Context, q Querier, learnerID string) ([]*LearnerCertificate, error) {
	rows, err := q.Query(ctx, `SELECT `+learnerCertificateColumns+` FROM learner_certificate WHERE learner_id = $1 ORDER BY issued_at DESC`, learnerID)
	if err != nil {
		return nil, fmt.Errorf("models: list learner certificates: %w", err)
	}
	defer rows.Close()

	var out []*LearnerCertificate
	for rows.Next() {
		c, err := scanLearnerCertificateRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanLearnerCertificate(row pgx.Row) (*LearnerCertificate, error) {
	var c LearnerCertificate
	if err := row.Scan(&c.ID, &c.OrgID, &c.LearnerID, &c.CourseID, &c.CertificateID, &c.IssuedAt, &c.PDFStoragePath); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan learner certificate: %w", err)
	}
	return &c, nil
}

func scanLearnerCertificateRows(rows pgx.Rows) (*LearnerCertificate, error) {
	var c LearnerCertificate
	if err := rows.Scan(&c.ID, &c.OrgID, &c.LearnerID, &c.CourseID, &c.CertificateID, &c.IssuedAt, &c.PDFStoragePath); err != nil {
		return nil, fmt.Errorf("models: scan learner certificate: %w", err)
	}
	return &c, nil
}
