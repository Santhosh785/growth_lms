package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CollabBoard is a course-scoped collaborative whiteboard. Snapshot is the
// board's JSON state (element_id -> element), debounce-persisted by the
// in-process realtime hub with last-write-wins per element.
type CollabBoard struct {
	ID        string
	OrgID     string
	CourseID  string
	Title     string
	Snapshot  json.RawMessage
	CreatedBy string
	UpdatedBy *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CollabBoardRepo struct{}

func NewCollabBoardRepo() *CollabBoardRepo { return &CollabBoardRepo{} }

const collabBoardColumns = `id, org_id, course_id, title, snapshot, created_by, updated_by, created_at, updated_at`

func (r *CollabBoardRepo) Create(ctx context.Context, q Querier, orgID, courseID, title, createdBy string) (*CollabBoard, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO collab_boards (org_id, course_id, title, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING `+collabBoardColumns, orgID, courseID, title, createdBy)
	return scanCollabBoard(row)
}

func (r *CollabBoardRepo) Get(ctx context.Context, q Querier, id string) (*CollabBoard, error) {
	row := q.QueryRow(ctx, `SELECT `+collabBoardColumns+` FROM collab_boards WHERE id = $1`, id)
	return scanCollabBoard(row)
}

func (r *CollabBoardRepo) ListByCourse(ctx context.Context, q Querier, courseID string) ([]*CollabBoard, error) {
	rows, err := q.Query(ctx, `
		SELECT `+collabBoardColumns+` FROM collab_boards
		WHERE course_id = $1 ORDER BY updated_at DESC`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list boards: %w", err)
	}
	defer rows.Close()
	var out []*CollabBoard
	for rows.Next() {
		b, err := scanCollabBoardRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SaveSnapshot persists the board's current JSON state. Called by the hub at
// pool privilege on a debounced timer, so it also records who last touched it.
func (r *CollabBoardRepo) SaveSnapshot(ctx context.Context, q Querier, id string, snapshot json.RawMessage, updatedBy string) error {
	tag, err := q.Exec(ctx, `
		UPDATE collab_boards SET snapshot = $2, updated_by = $3, updated_at = now()
		WHERE id = $1`, id, snapshot, updatedBy)
	if err != nil {
		return fmt.Errorf("models: save board snapshot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *CollabBoardRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM collab_boards WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete board: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCollabBoard(row pgx.Row) (*CollabBoard, error) {
	var b CollabBoard
	if err := row.Scan(&b.ID, &b.OrgID, &b.CourseID, &b.Title, &b.Snapshot, &b.CreatedBy, &b.UpdatedBy, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan board: %w", err)
	}
	return &b, nil
}

func scanCollabBoardRows(rows pgx.Rows) (*CollabBoard, error) {
	var b CollabBoard
	if err := rows.Scan(&b.ID, &b.OrgID, &b.CourseID, &b.Title, &b.Snapshot, &b.CreatedBy, &b.UpdatedBy, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan board: %w", err)
	}
	return &b, nil
}
