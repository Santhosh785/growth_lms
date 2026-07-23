// Package quota computes an organization's resource usage against its plan's
// limits (Task 10 usage & quota management) and answers "may this org create
// one more of resource X?" for enforcement at resource-creation time.
//
// The comparison logic here is deliberately DB-free and testable: the service
// is handed the plan, the usage, and (for enforcement) the dimension being
// grown, and it decides. Loading the plan/usage is delegated to the model
// repos passed in, so quota never talks to Postgres directly except through
// them. A nil or non-positive limit means "unlimited" for that dimension,
// matching how the codebase already treats AIConfig.MonthlyTokenLimit <= 0.
package quota

import (
	"context"
	"time"

	"growth-lms/internal/models"
)

// Dimension names a limited resource. Each maps to one plan limit + one usage
// gauge.
type Dimension string

const (
	DimCourses          Dimension = "courses"
	DimPublishedCourses Dimension = "published_courses"
	DimMembers          Dimension = "members"
	DimStorageBytes     Dimension = "storage_bytes"
	DimAITokensMonth    Dimension = "ai_tokens_month"
)

// LimitUsage is one dimension's current usage against its cap. Limit is nil
// when the dimension is uncapped on the org's plan.
type LimitUsage struct {
	Dimension Dimension `json:"dimension"`
	Used      int64     `json:"used"`
	Limit     *int64    `json:"limit"`
}

// Exceeded reports whether usage is at or over the cap. An uncapped dimension
// is never exceeded.
func (l LimitUsage) Exceeded() bool {
	return l.Limit != nil && *l.Limit > 0 && l.Used >= *l.Limit
}

// Report is an org's full usage picture: which plan it's on and every
// dimension's usage-vs-limit.
type Report struct {
	Plan   *models.Plan `json:"plan"`
	Limits []LimitUsage `json:"limits"`
}

// Service composes the repos needed to build a Report and enforce limits.
type Service struct {
	plans   *models.PlanRepo
	quota   *models.QuotaRepo
	aiUsage *models.AIUsageRepo
}

// New builds a quota Service. aiUsage may be nil in tests that don't exercise
// the AI-token dimension; it is only consulted for DimAITokensMonth.
func New(plans *models.PlanRepo, quota *models.QuotaRepo, aiUsage *models.AIUsageRepo) *Service {
	return &Service{plans: plans, quota: quota, aiUsage: aiUsage}
}

// limitFor returns the plan's cap for a dimension (nil = uncapped).
func limitFor(p *models.Plan, d Dimension) *int64 {
	switch d {
	case DimCourses:
		return p.MaxCourses
	case DimPublishedCourses:
		return p.MaxPublishedCourses
	case DimMembers:
		return p.MaxMembers
	case DimStorageBytes:
		return p.MaxStorageBytes
	case DimAITokensMonth:
		return p.MaxAITokensMonth
	default:
		return nil
	}
}

// usageFor returns the org's current usage for a dimension.
func usageFor(u models.OrgUsage, d Dimension) int64 {
	switch d {
	case DimCourses:
		return u.Courses
	case DimPublishedCourses:
		return u.PublishedCourses
	case DimMembers:
		return u.Members
	case DimStorageBytes:
		return u.StorageBytes
	case DimAITokensMonth:
		return u.AITokensMonth
	default:
		return 0
	}
}

// allDimensions is the fixed order dimensions appear in a Report.
var allDimensions = []Dimension{
	DimCourses, DimPublishedCourses, DimMembers, DimStorageBytes, DimAITokensMonth,
}

// load fetches the org's resolved plan and current usage (including this
// month's AI tokens). Shared by Report and Check.
func (s *Service) load(ctx context.Context, q models.Querier, orgID string, now time.Time) (*models.Plan, models.OrgUsage, error) {
	plan, err := s.plans.ResolveForOrg(ctx, q, orgID)
	if err != nil {
		return nil, models.OrgUsage{}, err
	}
	usage, err := s.quota.CurrentUsage(ctx, q, orgID)
	if err != nil {
		return nil, models.OrgUsage{}, err
	}
	if s.aiUsage != nil {
		counter, err := s.aiUsage.GetForPeriod(ctx, q, orgID, models.MonthPeriod(now))
		if err != nil {
			return nil, models.OrgUsage{}, err
		}
		usage.AITokensMonth = counter.TotalTokens()
	}
	return plan, usage, nil
}

// Report builds an org's full usage-vs-limits picture for the admin console.
func (s *Service) Report(ctx context.Context, q models.Querier, orgID string, now time.Time) (*Report, error) {
	plan, usage, err := s.load(ctx, q, orgID, now)
	if err != nil {
		return nil, err
	}
	limits := make([]LimitUsage, 0, len(allDimensions))
	for _, d := range allDimensions {
		limits = append(limits, LimitUsage{Dimension: d, Used: usageFor(usage, d), Limit: limitFor(plan, d)})
	}
	return &Report{Plan: plan, Limits: limits}, nil
}

// Check reports whether the org may add `delta` more of dimension d without
// exceeding its plan cap. ok is false when the addition would cross the cap;
// lu carries the dimension's current usage/limit for messaging. An uncapped
// dimension always returns ok=true.
func (s *Service) Check(ctx context.Context, q models.Querier, orgID string, d Dimension, delta int64, now time.Time) (ok bool, lu LimitUsage, err error) {
	plan, usage, err := s.load(ctx, q, orgID, now)
	if err != nil {
		return false, LimitUsage{}, err
	}
	lu = LimitUsage{Dimension: d, Used: usageFor(usage, d), Limit: limitFor(plan, d)}
	if lu.Limit == nil || *lu.Limit <= 0 {
		return true, lu, nil
	}
	return lu.Used+delta <= *lu.Limit, lu, nil
}
