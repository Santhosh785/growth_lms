package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CollabBoardTemplate is an org-level reusable starting point for a
// collaborative board (Task 9 "improved collaborative boards"). Snapshot is the
// seed state a new board copies. Title is unique per org so a template has a
// stable identity. Teacher-authored; readable by any org member so anyone
// creating a board can seed from one.
type CollabBoardTemplate struct {
	ID          string
	OrgID       string
	Title       string
	Description string
	Snapshot    json.RawMessage
	CreatedBy   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CollabBoardTemplateRepo struct{}

func NewCollabBoardTemplateRepo() *CollabBoardTemplateRepo { return &CollabBoardTemplateRepo{} }

const collabBoardTemplateColumns = `id, org_id, title, description, snapshot, created_by, created_at, updated_at`

// Create inserts a template. A nil snapshot is stored as an empty JSON object.
func (r *CollabBoardTemplateRepo) Create(ctx context.Context, q Querier, orgID, title, description string, snapshot json.RawMessage, createdBy string) (*CollabBoardTemplate, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO collab_board_templates (org_id, title, description, snapshot, created_by)
		VALUES ($1, $2, $3, COALESCE($4, '{}')::jsonb, $5)
		RETURNING `+collabBoardTemplateColumns,
		orgID, title, description, jsonOrNil(snapshot), createdBy)
	t, err := scanCollabBoardTemplate(row)
	if err != nil {
		return nil, fmt.Errorf("models: create board template: %w", err)
	}
	return t, nil
}

func (r *CollabBoardTemplateRepo) Get(ctx context.Context, q Querier, id string) (*CollabBoardTemplate, error) {
	row := q.QueryRow(ctx, `SELECT `+collabBoardTemplateColumns+` FROM collab_board_templates WHERE id = $1`, id)
	t, err := scanCollabBoardTemplate(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get board template: %w", err)
	}
	return t, nil
}

// ListByOrg returns an org's templates, newest first.
func (r *CollabBoardTemplateRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*CollabBoardTemplate, error) {
	rows, err := q.Query(ctx, `SELECT `+collabBoardTemplateColumns+`
		FROM collab_board_templates WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list board templates: %w", err)
	}
	defer rows.Close()
	var out []*CollabBoardTemplate
	for rows.Next() {
		t, err := scanCollabBoardTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Update overwrites a template's editable fields.
func (r *CollabBoardTemplateRepo) Update(ctx context.Context, q Querier, id, title, description string, snapshot json.RawMessage) (*CollabBoardTemplate, error) {
	row := q.QueryRow(ctx, `
		UPDATE collab_board_templates
		SET title = $2, description = $3, snapshot = COALESCE($4, '{}')::jsonb, updated_at = now()
		WHERE id = $1
		RETURNING `+collabBoardTemplateColumns,
		id, title, description, jsonOrNil(snapshot))
	t, err := scanCollabBoardTemplate(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update board template: %w", err)
	}
	return t, nil
}

func (r *CollabBoardTemplateRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM collab_board_templates WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete board template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCollabBoardTemplate(row pgx.Row) (*CollabBoardTemplate, error) {
	var t CollabBoardTemplate
	if err := row.Scan(&t.ID, &t.OrgID, &t.Title, &t.Description, &t.Snapshot, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan board template: %w", err)
	}
	return &t, nil
}
