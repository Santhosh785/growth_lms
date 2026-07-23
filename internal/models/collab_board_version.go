package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CollabBoardVersion is an immutable saved checkpoint of a collaborative
// board's state (Task 9 "improved collaborative boards"). Snapshot is a verbatim
// copy of collab_boards.snapshot at save time; Label is an optional human name.
// A member restores a board by copying a version's Snapshot back onto the board.
// OrgID is denormalized from the parent board for flat RLS.
type CollabBoardVersion struct {
	ID        string
	OrgID     string
	BoardID   string
	Label     string
	Snapshot  json.RawMessage
	CreatedBy *string
	CreatedAt time.Time
}

type CollabBoardVersionRepo struct{}

func NewCollabBoardVersionRepo() *CollabBoardVersionRepo { return &CollabBoardVersionRepo{} }

const collabBoardVersionColumns = `id, org_id, board_id, label, snapshot, created_by, created_at`

// Create saves a checkpoint of a board's current snapshot. A nil snapshot is
// stored as an empty JSON object.
func (r *CollabBoardVersionRepo) Create(ctx context.Context, q Querier, orgID, boardID, label string, snapshot json.RawMessage, createdBy string) (*CollabBoardVersion, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO collab_board_versions (org_id, board_id, label, snapshot, created_by)
		VALUES ($1, $2, $3, COALESCE($4, '{}')::jsonb, $5)
		RETURNING `+collabBoardVersionColumns,
		orgID, boardID, label, jsonOrNil(snapshot), createdBy)
	v, err := scanCollabBoardVersion(row)
	if err != nil {
		return nil, fmt.Errorf("models: create board version: %w", err)
	}
	return v, nil
}

// Get returns one version by id (RLS scopes visibility to org members).
func (r *CollabBoardVersionRepo) Get(ctx context.Context, q Querier, id string) (*CollabBoardVersion, error) {
	row := q.QueryRow(ctx, `SELECT `+collabBoardVersionColumns+` FROM collab_board_versions WHERE id = $1`, id)
	v, err := scanCollabBoardVersion(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get board version: %w", err)
	}
	return v, nil
}

// ListByBoard returns a board's checkpoints, newest first.
func (r *CollabBoardVersionRepo) ListByBoard(ctx context.Context, q Querier, boardID string) ([]*CollabBoardVersion, error) {
	rows, err := q.Query(ctx, `SELECT `+collabBoardVersionColumns+`
		FROM collab_board_versions WHERE board_id = $1 ORDER BY created_at DESC`, boardID)
	if err != nil {
		return nil, fmt.Errorf("models: list board versions: %w", err)
	}
	defer rows.Close()
	var out []*CollabBoardVersion
	for rows.Next() {
		v, err := scanCollabBoardVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Delete prunes a checkpoint (RLS restricts this to its creator or a
// moderator/owner).
func (r *CollabBoardVersionRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM collab_board_versions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete board version: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCollabBoardVersion(row pgx.Row) (*CollabBoardVersion, error) {
	var v CollabBoardVersion
	if err := row.Scan(&v.ID, &v.OrgID, &v.BoardID, &v.Label, &v.Snapshot, &v.CreatedBy, &v.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan board version: %w", err)
	}
	return &v, nil
}
