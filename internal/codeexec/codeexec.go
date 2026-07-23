// Package codeexec is Task 9's provider-abstracted sandboxed code-execution
// layer. It has two responsibilities and deliberately no others:
//
//   - a narrow Runner interface (Run source under resource limits, report the
//     result) with two implementations — a real sandbox-backed runner and a
//     hermetic stub used in development, tests, and whenever the feature is
//     dark;
//   - a Service that validates the language, clamps the requested resource
//     limits to the platform maxima, calls the runner, and normalizes the
//     result so callers can record and meter every run.
//
// Everything database-facing (feature-flag gating, daily-cap enforcement, the
// code_submissions ledger, exercise persistence) lives in the handler and
// model layers, not here — this package never touches Postgres, which keeps
// it unit-testable with the stub runner alone. The actual OS-level sandboxing
// (CPU, memory, wall-time, network, and filesystem limits) is the concern of
// a real Runner implementation; the interface below is the seam a container/
// gVisor/nsjail-backed runner plugs into.
package codeexec

import (
	"context"
	"fmt"
	"strings"
)

// Language identifies a supported source language. The value is stored
// verbatim in code_submissions.language and code_exercises.language.
type Language string

const (
	LangPython     Language = "python"
	LangJavaScript Language = "javascript"
	LangGo         Language = "go"
	LangRuby       Language = "ruby"
	LangBash       Language = "bash"
	LangC          Language = "c"
	LangCPP        Language = "cpp"
	LangJava       Language = "java"
)

// supportedLanguages is the closed set the Service accepts. A runner may
// support fewer; it reports Status "error" for anything it cannot run.
var supportedLanguages = map[Language]bool{
	LangPython: true, LangJavaScript: true, LangGo: true, LangRuby: true,
	LangBash: true, LangC: true, LangCPP: true, LangJava: true,
}

// SupportedLanguage reports whether lang is one the Service will accept.
func SupportedLanguage(lang string) bool {
	return supportedLanguages[Language(strings.ToLower(strings.TrimSpace(lang)))]
}

// Limits is the resource envelope a single run is executed under. Zero/negative
// fields are treated as "use the platform default" by the Service. A real
// Runner MUST enforce all of these; the sandbox denies network access and
// gives the program an ephemeral, discardable filesystem unconditionally
// (they are not tunable per-run, so they are documented here rather than
// exposed as fields).
type Limits struct {
	CPUMillis      int   // CPU time ceiling
	MemoryBytes    int64 // address-space / RSS ceiling
	WallTimeMillis int   // real-time ceiling (guards against sleeps/deadlocks)
	MaxOutputBytes int   // stdout+stderr are truncated past this
}

// Request is a single execution: the language, the program source, optional
// stdin, and the resource envelope.
type Request struct {
	Language Language
	Source   string
	Stdin    string
	Limits   Limits
}

// Status classifies the terminal outcome of a run. It mirrors the
// code_submissions.status CHECK constraint (minus blocked_limit, which is a
// handler-layer verdict that never reaches a runner).
type Status string

const (
	StatusSucceeded Status = "succeeded" // ran to completion, exit code 0
	StatusFailed    Status = "failed"    // ran to completion, non-zero exit
	StatusTimeout   Status = "timeout"   // killed by the wall-time/CPU limit
	StatusOOM       Status = "oom"       // killed by the memory limit
	StatusError     Status = "error"     // the runner itself failed to execute
)

// Result is what a Runner returns: captured output, the exit code, the
// measured resource usage, and the terminal status. Stdout/Stderr are already
// truncated to Limits.MaxOutputBytes by the runner.
type Result struct {
	Stdout         string
	Stderr         string
	ExitCode       int
	DurationMillis int
	MemoryKB       int
	Status         Status
	// Err carries a runner-internal failure message when Status is
	// StatusError (e.g. "unsupported language"); empty otherwise.
	Err string
}

// Runner is the single seam every execution backend implements. Name is
// recorded verbatim into the code_submissions.runner column. Run must never
// panic on hostile input and must honor ctx cancellation.
type Runner interface {
	Name() string
	Run(ctx context.Context, req Request) (Result, error)
}

// wrapRunnerErr gives runner errors a stable prefix for logs.
func wrapRunnerErr(name string, err error) error {
	return fmt.Errorf("codeexec: runner %s run: %w", name, err)
}
