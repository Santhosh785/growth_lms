package codeexec

import (
	"context"
	"strconv"
	"strings"
)

// stubRunnerName is the runner ID reported by the stub. Recorded in the
// code_submissions.runner column like any other runner.
const stubRunnerName = "stub-noop"

// StubRunner is a hermetic, network-free Runner that does NOT execute any
// code. It is the default backend whenever the feature is dark (no real
// runner configured, or LMS_CODE_EXEC_ENABLED false) and the only backend
// used in tests — a CI box must never actually run untrusted learner source.
// Its output is deterministic and derived from the request, so tests can
// assert on it, and developers get a working end-to-end feature without a
// sandbox runtime.
//
// It reports a successful, zero-exit result whose stdout explains that
// execution is not configured, so a learner submitting against an exercise
// gets a plausibly-shaped (but clearly-labelled) result rather than an error.
type StubRunner struct{}

// NewStubRunner returns the no-op backend.
func NewStubRunner() *StubRunner { return &StubRunner{} }

func (r *StubRunner) Name() string { return stubRunnerName }

// Run validates the language and echoes a deterministic placeholder result.
// It never runs the source. An unsupported language is reported as
// StatusError, matching how a real runner rejects a language it lacks.
func (r *StubRunner) Run(_ context.Context, req Request) (Result, error) {
	if !supportedLanguages[req.Language] {
		return Result{
			Status: StatusError,
			Err:    "unsupported language: " + string(req.Language),
		}, nil
	}
	var b strings.Builder
	b.WriteString("[stub-codeexec] Code execution is not configured on this platform, ")
	b.WriteString("so no code was run. This is a placeholder result.\n")
	b.WriteString("language=")
	b.WriteString(string(req.Language))
	b.WriteString(" source_bytes=")
	b.WriteString(strconv.Itoa(len(req.Source)))
	b.WriteString("\nConfigure LMS_CODE_EXEC_RUNNER and a sandbox runtime to enable real execution.\n")
	out := truncate(b.String(), req.Limits.MaxOutputBytes)
	return Result{
		Stdout:         out,
		ExitCode:       0,
		DurationMillis: 0,
		MemoryKB:       0,
		Status:         StatusSucceeded,
	}, nil
}

// truncate caps s at max bytes (max <= 0 means no cap), appending an ellipsis
// marker when it cuts. A real runner truncates identically so callers never
// have to bound output themselves.
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
