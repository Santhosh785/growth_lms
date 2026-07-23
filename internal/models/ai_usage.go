package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AIUsageCounter is an org's accumulated AI token/cost usage for one
// calendar month (plan.md Task 9 usage limits + cost tracking). The monthly
// limit check reads this single row instead of scanning ai_generations.
type AIUsageCounter struct {
	OrgID        string
	Period       time.Time
	InputTokens  int64
	OutputTokens int64
	CostMicros   int64
}

// TotalTokens is the metered quantity the monthly limit is expressed in.
func (c AIUsageCounter) TotalTokens() int64 { return c.InputTokens + c.OutputTokens }

type AIUsageRepo struct{}

func NewAIUsageRepo() *AIUsageRepo { return &AIUsageRepo{} }

// MonthPeriod normalizes a timestamp to the first day of its UTC month —
// the period key used by ai_usage_counters.
func MonthPeriod(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// GetForPeriod returns the counter for (org, period). A missing row is
// reported as a zero-valued counter, not an error — the first call of a
// month has no row yet.
func (r *AIUsageRepo) GetForPeriod(ctx context.Context, q Querier, orgID string, period time.Time) (AIUsageCounter, error) {
	c := AIUsageCounter{OrgID: orgID, Period: period}
	err := q.QueryRow(ctx, `
		SELECT input_tokens, output_tokens, cost_micros
		FROM ai_usage_counters WHERE org_id = $1 AND period = $2`, orgID, period).
		Scan(&c.InputTokens, &c.OutputTokens, &c.CostMicros)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c, nil
		}
		return c, fmt.Errorf("models: get ai usage counter: %w", err)
	}
	return c, nil
}

// AddUsage atomically increments the (org, period) counter, creating the
// row on first use. Called in the same request transaction as the
// generation it accounts for.
func (r *AIUsageRepo) AddUsage(ctx context.Context, q Querier, orgID string, period time.Time, inputTokens, outputTokens, costMicros int64) error {
	_, err := q.Exec(ctx, `
		INSERT INTO ai_usage_counters (org_id, period, input_tokens, output_tokens, cost_micros)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id, period) DO UPDATE SET
			input_tokens = ai_usage_counters.input_tokens + EXCLUDED.input_tokens,
			output_tokens = ai_usage_counters.output_tokens + EXCLUDED.output_tokens,
			cost_micros = ai_usage_counters.cost_micros + EXCLUDED.cost_micros,
			updated_at = now()`,
		orgID, period, inputTokens, outputTokens, costMicros)
	if err != nil {
		return fmt.Errorf("models: add ai usage: %w", err)
	}
	return nil
}
