package codeexec

import (
	"context"
	"errors"
	"strings"
)

// ErrUnsupportedLanguage is returned by Execute before any runner call when
// the request names a language outside the supported set. The handler maps it
// to a 400 rather than logging a runner error.
var ErrUnsupportedLanguage = errors.New("codeexec: unsupported language")

// ErrEmptySource is returned when there is nothing to run.
var ErrEmptySource = errors.New("codeexec: empty source")

// Service validates a run, clamps its resource limits to the platform maxima,
// and dispatches to the configured Runner. It holds no database handle by
// design; feature-gating, the daily cap, and the code_submissions ledger are
// the handler layer's job.
type Service struct {
	runner   Runner
	defaults Limits // platform default AND maximum resource envelope
}

// NewService wraps a Runner with the platform default/maximum limits. Any
// zero field in defaults falls back to a conservative built-in.
func NewService(r Runner, defaults Limits) *Service {
	return &Service{runner: r, defaults: withFallback(defaults)}
}

// RunnerName exposes the backing runner's identity for callers that record or
// surface it (the submission ledger, the usage dashboard).
func (s *Service) RunnerName() string { return s.runner.Name() }

// DefaultLimits returns the platform default/maximum resource envelope, so a
// handler can seed a new exercise's limits or show them to an author.
func (s *Service) DefaultLimits() Limits { return s.defaults }

// Output is the result of one execution together with the effective limits it
// ran under, so the caller can persist exactly what was requested.
type Output struct {
	Result
	Runner   string
	Language Language
	Limits   Limits
}

// Execute validates the request, clamps its limits to the platform maxima,
// runs it, and returns the normalized outcome. A validation failure returns a
// sentinel error and never reaches the runner. A runner transport error is
// returned wrapped; a run that completes (even failing/timing-out) returns a
// nil error with the outcome carried in Output.Result.Status.
func (s *Service) Execute(ctx context.Context, req Request) (Output, error) {
	req.Language = Language(strings.ToLower(strings.TrimSpace(string(req.Language))))
	if !supportedLanguages[req.Language] {
		return Output{}, ErrUnsupportedLanguage
	}
	if strings.TrimSpace(req.Source) == "" {
		return Output{}, ErrEmptySource
	}
	req.Limits = s.clamp(req.Limits)

	res, err := s.runner.Run(ctx, req)
	if err != nil {
		return Output{}, wrapRunnerErr(s.runner.Name(), err)
	}
	// Defense in depth: a misbehaving runner might over-report output; the
	// Service enforces the ceiling regardless.
	res.Stdout = truncate(res.Stdout, req.Limits.MaxOutputBytes)
	res.Stderr = truncate(res.Stderr, req.Limits.MaxOutputBytes)
	if res.Status == "" {
		res.Status = StatusError
		res.Err = "runner returned empty status"
	}
	return Output{
		Result:   res,
		Runner:   s.runner.Name(),
		Language: req.Language,
		Limits:   req.Limits,
	}, nil
}

// clamp bounds each requested limit into (0, platform-max]. A non-positive
// request means "use the platform default"; an over-large request is capped
// to the platform maximum, so an author or learner can only ever ask for
// equal-or-less than the operator allows.
func (s *Service) clamp(l Limits) Limits {
	return Limits{
		CPUMillis:      clampInt(l.CPUMillis, s.defaults.CPUMillis),
		MemoryBytes:    clampInt64(l.MemoryBytes, s.defaults.MemoryBytes),
		WallTimeMillis: clampInt(l.WallTimeMillis, s.defaults.WallTimeMillis),
		MaxOutputBytes: clampInt(l.MaxOutputBytes, s.defaults.MaxOutputBytes),
	}
}

func clampInt(req, max int) int {
	if req <= 0 || req > max {
		return max
	}
	return req
}

func clampInt64(req, max int64) int64 {
	if req <= 0 || req > max {
		return max
	}
	return req
}

// defaultLimits is the conservative built-in envelope used for any platform
// default field left unset by configuration.
var defaultLimits = Limits{
	CPUMillis:      5000,
	MemoryBytes:    256 << 20, // 256 MiB
	WallTimeMillis: 10000,
	MaxOutputBytes: 64 << 10, // 64 KiB
}

func withFallback(l Limits) Limits {
	if l.CPUMillis <= 0 {
		l.CPUMillis = defaultLimits.CPUMillis
	}
	if l.MemoryBytes <= 0 {
		l.MemoryBytes = defaultLimits.MemoryBytes
	}
	if l.WallTimeMillis <= 0 {
		l.WallTimeMillis = defaultLimits.WallTimeMillis
	}
	if l.MaxOutputBytes <= 0 {
		l.MaxOutputBytes = defaultLimits.MaxOutputBytes
	}
	return l
}
