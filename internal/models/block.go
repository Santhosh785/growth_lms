package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Block types, matching the CHECK constraint in
// db/migrations/000003_course_domain.up.sql, widened by
// db/migrations/000004_learner_journey.up.sql to add "assignment" as a
// 6th type (grilling-record.md Q1). Exactly these six — no others are
// ever valid, per the spec.
const (
	BlockTypeText       = "text"
	BlockTypeImage      = "image"
	BlockTypeVideo      = "video"
	BlockTypeFile       = "file"
	BlockTypeQuiz       = "quiz"
	BlockTypeAssignment = "assignment"
)

type Block struct {
	ID        string
	LessonID  string
	CourseID  string
	OrgID     string
	Type      string
	Content   json.RawMessage
	SortOrder float64
	CreatedBy string
	UpdatedAt time.Time
}

// TextBlockContent is the JSONB shape for a "text" block. HTML must
// already be sanitized (internal/sanitize) before it reaches this struct.
type TextBlockContent struct {
	HTML string `json:"html"`
}

// ImageBlockContent is the JSONB shape for an "image" block.
type ImageBlockContent struct {
	AssetID string `json:"asset_id"`
	AltText string `json:"alt_text"`
}

// VideoBlockContent is the JSONB shape for a "video" block. Duration and
// ThumbnailURL are filled in once Bunny's transcode-complete webhook
// fires — they're zero-valued at creation time.
type VideoBlockContent struct {
	AssetID      string `json:"asset_id"`
	Duration     int    `json:"duration"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// FileBlockContent is the JSONB shape for a "file" block.
type FileBlockContent struct {
	AssetID  string `json:"asset_id"`
	Filename string `json:"filename"`
}

// QuizQuestion is one question within a "quiz" block's content.questions
// array. Type is one of mcq/true_false/short_answer: mcq/true_false use
// Answers + CorrectAnswerIndex; short_answer uses AcceptedAnswers instead
// (supports multiple accepted phrasings, no single correct index).
// Authoring only — no scoring logic here (Task 5's job).
type QuizQuestion struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	Question           string   `json:"question"`
	Answers            []string `json:"answers,omitempty"`
	CorrectAnswerIndex *int     `json:"correct_answer_index,omitempty"`
	AcceptedAnswers    []string `json:"accepted_answers,omitempty"`
}

// QuizBlockContent is the JSONB shape for a "quiz" block.
type QuizBlockContent struct {
	Questions []QuizQuestion `json:"questions"`
}

// AssignmentBlockContent is the JSONB shape for an "assignment" block
// (grilling-record.md Q1). DueDate is optional (nil means no deadline, so
// every submission is on_time); AllowResubmission governs whether a
// learner may submit more than once (Task 5 Stage 5).
type AssignmentBlockContent struct {
	Instructions      string     `json:"instructions"`
	DueDate           *time.Time `json:"due_date,omitempty"`
	AllowResubmission bool       `json:"allow_resubmission"`
}

type BlockRepo struct{}

func NewBlockRepo() *BlockRepo { return &BlockRepo{} }

const blockColumns = `id, lesson_id, course_id, org_id, type, content, sort_order, created_by, updated_at`

func (r *BlockRepo) Create(ctx context.Context, q Querier, lessonID, courseID, orgID, createdBy, blockType string, content json.RawMessage, sortOrder float64) (*Block, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO blocks (lesson_id, course_id, org_id, type, content, sort_order, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+blockColumns, lessonID, courseID, orgID, blockType, content, sortOrder, createdBy)
	return scanBlock(row)
}

func (r *BlockRepo) Get(ctx context.Context, q Querier, id string) (*Block, error) {
	row := q.QueryRow(ctx, `SELECT `+blockColumns+` FROM blocks WHERE id = $1`, id)
	return scanBlock(row)
}

func (r *BlockRepo) ListByLesson(ctx context.Context, q Querier, lessonID string) ([]*Block, error) {
	rows, err := q.Query(ctx, `SELECT `+blockColumns+` FROM blocks WHERE lesson_id = $1 ORDER BY sort_order`, lessonID)
	if err != nil {
		return nil, fmt.Errorf("models: list blocks: %w", err)
	}
	defer rows.Close()

	var out []*Block
	for rows.Next() {
		b, err := scanBlockRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListVideoBlocksByCourse returns every video-type block in a course,
// used by the publish handler to check every video asset is
// processing_status='ready' before allowing publish.
func (r *BlockRepo) ListVideoBlocksByCourse(ctx context.Context, q Querier, courseID string) ([]*Block, error) {
	rows, err := q.Query(ctx, `SELECT `+blockColumns+` FROM blocks WHERE course_id = $1 AND type = 'video' ORDER BY sort_order`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list video blocks: %w", err)
	}
	defer rows.Close()

	var out []*Block
	for rows.Next() {
		b, err := scanBlockRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Update sets both content and sort_order, and (unlike Autosave) is meant
// for explicit editor saves — semantically identical to Autosave at the
// DB layer (both only touch content/sort_order/updated_at, never course
// status), kept as a separate method so callers' intent stays legible.
func (r *BlockRepo) Update(ctx context.Context, q Querier, id string, content json.RawMessage, sortOrder *float64) (*Block, error) {
	var row pgx.Row
	if sortOrder != nil {
		row = q.QueryRow(ctx, `
			UPDATE blocks SET content = $2, sort_order = $3, updated_at = now()
			WHERE id = $1 RETURNING `+blockColumns, id, content, *sortOrder)
	} else {
		row = q.QueryRow(ctx, `
			UPDATE blocks SET content = $2, updated_at = now()
			WHERE id = $1 RETURNING `+blockColumns, id, content)
	}
	return scanBlock(row)
}

// Autosave updates content only, never sort_order — matching the spec's
// "save block content without changing course status" autosave endpoint.
func (r *BlockRepo) Autosave(ctx context.Context, q Querier, id string, content json.RawMessage) (*Block, error) {
	row := q.QueryRow(ctx, `
		UPDATE blocks SET content = $2, updated_at = now()
		WHERE id = $1 RETURNING `+blockColumns, id, content)
	return scanBlock(row)
}

func (r *BlockRepo) SetSortOrder(ctx context.Context, q Querier, id string, sortOrder float64) error {
	tag, err := q.Exec(ctx, `UPDATE blocks SET sort_order = $2, updated_at = now() WHERE id = $1`, id, sortOrder)
	if err != nil {
		return fmt.Errorf("models: set block sort_order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *BlockRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM blocks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete block: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanBlock(row pgx.Row) (*Block, error) {
	var b Block
	if err := row.Scan(&b.ID, &b.LessonID, &b.CourseID, &b.OrgID, &b.Type, &b.Content, &b.SortOrder, &b.CreatedBy, &b.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan block: %w", err)
	}
	return &b, nil
}

func scanBlockRows(rows pgx.Rows) (*Block, error) {
	var b Block
	if err := rows.Scan(&b.ID, &b.LessonID, &b.CourseID, &b.OrgID, &b.Type, &b.Content, &b.SortOrder, &b.CreatedBy, &b.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan block: %w", err)
	}
	return &b, nil
}
