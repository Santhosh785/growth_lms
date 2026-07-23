package codeexec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func platformDefaults() Limits {
	return Limits{CPUMillis: 5000, MemoryBytes: 256 << 20, WallTimeMillis: 10000, MaxOutputBytes: 1024}
}

func TestSupportedLanguage(t *testing.T) {
	if !SupportedLanguage("python") || !SupportedLanguage("  Python  ") {
		t.Fatal("python should be supported (and case/space-insensitive)")
	}
	if SupportedLanguage("brainfuck") {
		t.Fatal("unsupported language should report false")
	}
}

func TestExecuteValidatesInput(t *testing.T) {
	svc := NewService(NewStubRunner(), platformDefaults())

	if _, err := svc.Execute(context.Background(), Request{Language: "cobol", Source: "x"}); !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatalf("expected ErrUnsupportedLanguage, got %v", err)
	}
	if _, err := svc.Execute(context.Background(), Request{Language: LangPython, Source: "   "}); !errors.Is(err, ErrEmptySource) {
		t.Fatalf("expected ErrEmptySource, got %v", err)
	}
}

func TestExecuteStubSucceedsAndAccounts(t *testing.T) {
	svc := NewService(NewStubRunner(), platformDefaults())
	out, err := svc.Execute(context.Background(), Request{Language: "Python", Source: "print(1)"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", out.Status)
	}
	if out.Runner != stubRunnerName {
		t.Fatalf("runner = %q, want %q", out.Runner, stubRunnerName)
	}
	// The language is normalized to lower-case before dispatch.
	if out.Language != LangPython {
		t.Fatalf("language = %q, want python", out.Language)
	}
	if out.Stdout == "" {
		t.Fatal("expected non-empty stub stdout")
	}
}

func TestExecuteClampsLimits(t *testing.T) {
	svc := NewService(NewStubRunner(), platformDefaults())
	// Request wildly-oversized limits; they must be clamped to the platform
	// maxima so a caller can never ask for more than the operator allows.
	out, err := svc.Execute(context.Background(), Request{
		Language: LangPython,
		Source:   "print(1)",
		Limits:   Limits{CPUMillis: 1 << 30, MemoryBytes: 1 << 60, WallTimeMillis: 1 << 30, MaxOutputBytes: 1 << 30},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	def := platformDefaults()
	if out.Limits.CPUMillis != def.CPUMillis || out.Limits.MemoryBytes != def.MemoryBytes ||
		out.Limits.WallTimeMillis != def.WallTimeMillis || out.Limits.MaxOutputBytes != def.MaxOutputBytes {
		t.Fatalf("over-large limits not clamped: %+v", out.Limits)
	}
}

func TestExecuteTruncatesOutput(t *testing.T) {
	// A tiny output ceiling forces truncation of the stub's placeholder text.
	svc := NewService(NewStubRunner(), Limits{CPUMillis: 100, MemoryBytes: 1 << 20, WallTimeMillis: 100, MaxOutputBytes: 20})
	out, err := svc.Execute(context.Background(), Request{Language: LangPython, Source: "print(1)"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out.Stdout) > 20 {
		t.Fatalf("stdout not truncated to ceiling: %d bytes", len(out.Stdout))
	}
	if !strings.HasSuffix(out.Stdout, "...") {
		t.Fatalf("truncated output should end with ellipsis marker: %q", out.Stdout)
	}
}

func TestStubReportsUnsupportedLanguageAsError(t *testing.T) {
	// The Service rejects unsupported languages before the runner, but the
	// runner must also defensively report StatusError if reached directly.
	res, err := NewStubRunner().Run(context.Background(), Request{Language: "cobol", Source: "x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
}

func TestServiceUsesFallbackDefaults(t *testing.T) {
	// A zero-valued defaults struct must be backfilled with the conservative
	// built-ins rather than leaving a 0 (unlimited) envelope.
	svc := NewService(NewStubRunner(), Limits{})
	got := svc.DefaultLimits()
	if got.CPUMillis != defaultLimits.CPUMillis || got.MemoryBytes != defaultLimits.MemoryBytes ||
		got.WallTimeMillis != defaultLimits.WallTimeMillis || got.MaxOutputBytes != defaultLimits.MaxOutputBytes {
		t.Fatalf("zero defaults not backfilled: %+v", got)
	}
}

func TestNewServiceFromSettingsAlwaysStubForNow(t *testing.T) {
	// No real runner is compiled in, so every configuration yields the stub —
	// the feature stays safely dark even if an operator flips the flag.
	for _, enabled := range []bool{false, true} {
		if svc := NewServiceFromSettings(enabled, "gvisor", platformDefaults()); svc.RunnerName() != stubRunnerName {
			t.Fatalf("enabled=%v: runner = %q, want stub", enabled, svc.RunnerName())
		}
	}
}
