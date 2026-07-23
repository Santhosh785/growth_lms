package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AITutorSession is a course-scoped tutor conversation owned by one learner
// (plan.md Task 9 "course-scoped tutors"). Messages hang off it.
type AITutorSession struct {
	ID        string
	OrgID     string
	CourseID  string
	LearnerID string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AITutorMessage is one turn (user or assistant) in a tutor session.
type AITutorMessage struct {
	ID        string
	SessionID string
	OrgID     string
	LearnerID string
	Role      string
	Content   string
	CreatedAt time.Time
}

type AITutorRepo struct{}

func NewAITutorRepo() *AITutorRepo { return &AITutorRepo{} }

const aiTutorSessionColumns = `id, org_id, course_id, learner_id, title, created_at, updated_at`

func scanAITutorSession(row pgx.Row) (*AITutorSession, error) {
	var s AITutorSession
	if err := row.Scan(&s.ID, &s.OrgID, &s.CourseID, &s.LearnerID, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// CreateSession starts a new tutor conversation for the learner.
func (r *AITutorRepo) CreateSession(ctx context.Context, q Querier, orgID, courseID, learnerID, title string) (*AITutorSession, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO ai_tutor_sessions (org_id, course_id, learner_id, title)
		VALUES ($1, $2, $3, $4)
		RETURNING `+aiTutorSessionColumns, orgID, courseID, learnerID, title)
	s, err := scanAITutorSession(row)
	if err != nil {
		return nil, fmt.Errorf("models: create tutor session: %w", err)
	}
	return s, nil
}

// GetSession fetches one session by id (RLS restricts visibility to its
// owner learner or an org teacher/owner). Returns ErrNotFound if absent or
// invisible.
func (r *AITutorRepo) GetSession(ctx context.Context, q Querier, sessionID string) (*AITutorSession, error) {
	row := q.QueryRow(ctx, `SELECT `+aiTutorSessionColumns+` FROM ai_tutor_sessions WHERE id = $1`, sessionID)
	s, err := scanAITutorSession(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get tutor session: %w", err)
	}
	return s, nil
}

// ListSessionsForLearner returns a learner's tutor sessions for a course,
// most-recently-used first.
func (r *AITutorRepo) ListSessionsForLearner(ctx context.Context, q Querier, learnerID, courseID string) ([]*AITutorSession, error) {
	rows, err := q.Query(ctx, `SELECT `+aiTutorSessionColumns+`
		FROM ai_tutor_sessions WHERE learner_id = $1 AND course_id = $2
		ORDER BY updated_at DESC`, learnerID, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list tutor sessions: %w", err)
	}
	defer rows.Close()

	var out []*AITutorSession
	for rows.Next() {
		var s AITutorSession
		if err := rows.Scan(&s.ID, &s.OrgID, &s.CourseID, &s.LearnerID, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("models: scan tutor session: %w", err)
		}
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list tutor sessions: %w", err)
	}
	return out, nil
}

// AppendMessage adds a turn and bumps the session's updated_at.
func (r *AITutorRepo) AppendMessage(ctx context.Context, q Querier, sessionID, orgID, learnerID, role, content string) (*AITutorMessage, error) {
	var m AITutorMessage
	err := q.QueryRow(ctx, `
		INSERT INTO ai_tutor_messages (session_id, org_id, learner_id, role, content)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, session_id, org_id, learner_id, role, content, created_at`,
		sessionID, orgID, learnerID, role, content).
		Scan(&m.ID, &m.SessionID, &m.OrgID, &m.LearnerID, &m.Role, &m.Content, &m.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("models: append tutor message: %w", err)
	}
	if _, err := q.Exec(ctx, `UPDATE ai_tutor_sessions SET updated_at = now() WHERE id = $1`, sessionID); err != nil {
		return nil, fmt.Errorf("models: touch tutor session: %w", err)
	}
	return &m, nil
}

// ListMessages returns a session's turns in chronological order.
func (r *AITutorRepo) ListMessages(ctx context.Context, q Querier, sessionID string) ([]*AITutorMessage, error) {
	rows, err := q.Query(ctx, `
		SELECT id, session_id, org_id, learner_id, role, content, created_at
		FROM ai_tutor_messages WHERE session_id = $1 ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("models: list tutor messages: %w", err)
	}
	defer rows.Close()

	var out []*AITutorMessage
	for rows.Next() {
		var m AITutorMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.OrgID, &m.LearnerID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("models: scan tutor message: %w", err)
		}
		out = append(out, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list tutor messages: %w", err)
	}
	return out, nil
}
