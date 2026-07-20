package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CourseAnnouncement has no learner scope: teacher/owner authored,
// readable by every org member (matches Task 4's shared-content RLS
// convention, e.g. courses_select).
type CourseAnnouncement struct {
	ID          string
	OrgID       string
	CourseID    string
	Title       string
	Body        string
	CreatedBy   string
	PublishedAt time.Time
}

type CourseAnnouncementRepo struct{}

func NewCourseAnnouncementRepo() *CourseAnnouncementRepo { return &CourseAnnouncementRepo{} }

const courseAnnouncementColumns = `id, org_id, course_id, title, body, created_by, published_at`

func (r *CourseAnnouncementRepo) Create(ctx context.Context, q Querier, orgID, courseID, title, body, createdBy string) (*CourseAnnouncement, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO course_announcement (org_id, course_id, title, body, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+courseAnnouncementColumns, orgID, courseID, title, body, createdBy)
	return scanCourseAnnouncement(row)
}

func (r *CourseAnnouncementRepo) Get(ctx context.Context, q Querier, id string) (*CourseAnnouncement, error) {
	row := q.QueryRow(ctx, `SELECT `+courseAnnouncementColumns+` FROM course_announcement WHERE id = $1`, id)
	return scanCourseAnnouncement(row)
}

// ListByCourse returns a course's announcements newest first, matching
// the "continue learning" dashboard's reverse-chronological feed.
func (r *CourseAnnouncementRepo) ListByCourse(ctx context.Context, q Querier, courseID string) ([]*CourseAnnouncement, error) {
	rows, err := q.Query(ctx, `SELECT `+courseAnnouncementColumns+` FROM course_announcement WHERE course_id = $1 ORDER BY published_at DESC`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list course announcements: %w", err)
	}
	defer rows.Close()

	var out []*CourseAnnouncement
	for rows.Next() {
		a, err := scanCourseAnnouncementRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *CourseAnnouncementRepo) Update(ctx context.Context, q Querier, id, title, body string) (*CourseAnnouncement, error) {
	row := q.QueryRow(ctx, `
		UPDATE course_announcement SET title = $2, body = $3
		WHERE id = $1 RETURNING `+courseAnnouncementColumns, id, title, body)
	return scanCourseAnnouncement(row)
}

func (r *CourseAnnouncementRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM course_announcement WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete course announcement: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCourseAnnouncement(row pgx.Row) (*CourseAnnouncement, error) {
	var a CourseAnnouncement
	if err := row.Scan(&a.ID, &a.OrgID, &a.CourseID, &a.Title, &a.Body, &a.CreatedBy, &a.PublishedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan course announcement: %w", err)
	}
	return &a, nil
}

func scanCourseAnnouncementRows(rows pgx.Rows) (*CourseAnnouncement, error) {
	var a CourseAnnouncement
	if err := rows.Scan(&a.ID, &a.OrgID, &a.CourseID, &a.Title, &a.Body, &a.CreatedBy, &a.PublishedAt); err != nil {
		return nil, fmt.Errorf("models: scan course announcement: %w", err)
	}
	return &a, nil
}
