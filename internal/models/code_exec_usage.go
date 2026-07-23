package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CodeExecUsageCounter is an org's accumulated code-execution usage for one
// calendar day (plan.md Task 9 usage limits). The daily cap check reads this
// single row instead of scanning code_submissions. Mirrors AIUsageCounter,
// keyed by day instead of month and counting executions instead of tokens.
type CodeExecUsageCounter struct {
	OrgID          string
	Period         time.Time
	ExecutionCount int64
	CPUMillis      int64
}

type CodeExecUsageRepo struct{}

func NewCodeExecUsageRepo() *CodeExecUsageRepo { return &CodeExecUsageRepo{} }

// DayPeriod normalizes a timestamp to midnight of its UTC day — the period
// key used by code_exec_usage_counters.
func DayPeriod(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// GetForPeriod returns the counter for (org, period). A missing row is
// reported as a zero-valued counter, not an error — the first run of a day
// has no row yet.
func (r *CodeExecUsageRepo) GetForPeriod(ctx context.Context, q Querier, orgID string, period time.Time) (CodeExecUsageCounter, error) {
	c := CodeExecUsageCounter{OrgID: orgID, Period: period}
	err := q.QueryRow(ctx, `
		SELECT execution_count, cpu_millis
		FROM code_exec_usage_counters WHERE org_id = $1 AND period = $2`, orgID, period).
		Scan(&c.ExecutionCount, &c.CPUMillis)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c, nil
		}
		return c, fmt.Errorf("models: get code exec usage counter: %w", err)
	}
	return c, nil
}

// AddUsage atomically increments the (org, period) counter by one execution
// and the given CPU milliseconds, creating the row on first use. Called in
// the same request transaction as the run it accounts for.
func (r *CodeExecUsageRepo) AddUsage(ctx context.Context, q Querier, orgID string, period time.Time, cpuMillis int64) error {
	_, err := q.Exec(ctx, `
		INSERT INTO code_exec_usage_counters (org_id, period, execution_count, cpu_millis)
		VALUES ($1, $2, 1, $3)
		ON CONFLICT (org_id, period) DO UPDATE SET
			execution_count = code_exec_usage_counters.execution_count + 1,
			cpu_millis = code_exec_usage_counters.cpu_millis + EXCLUDED.cpu_millis,
			updated_at = now()`,
		orgID, period, cpuMillis)
	if err != nil {
		return fmt.Errorf("models: add code exec usage: %w", err)
	}
	return nil
}
