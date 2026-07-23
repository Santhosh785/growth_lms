package ai

import (
	"context"
	"strings"
	"testing"
)

func TestCostMicros(t *testing.T) {
	// Opus 4.8: 5 micros/input token, 25 micros/output token.
	if got := CostMicros("claude-opus-4-8", 1000, 200); got != 1000*5+200*25 {
		t.Fatalf("opus cost = %d, want %d", got, 1000*5+200*25)
	}
	// Unknown model falls back to the Opus rate (over-report, never under).
	if got := CostMicros("some-unknown-model", 10, 10); got != 10*5+10*25 {
		t.Fatalf("unknown-model cost = %d, want opus fallback %d", got, 10*5+10*25)
	}
	// Stub model is free.
	if got := CostMicros(stubModel, 100, 100); got != 0 {
		t.Fatalf("stub cost = %d, want 0", got)
	}
}

func TestStubProviderDeterministicAndAccounted(t *testing.T) {
	svc := NewService(NewStubProvider())
	if svc.ProviderName() != "stub" {
		t.Fatalf("provider name = %q, want stub", svc.ProviderName())
	}

	out, err := svc.Outline(context.Background(), OutlineInput{Topic: "Photosynthesis", Modules: 4})
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if out.PromptVersion != PromptOutlineV1 {
		t.Fatalf("prompt version = %q, want %q", out.PromptVersion, PromptOutlineV1)
	}
	if out.Text == "" {
		t.Fatal("expected non-empty stub output")
	}
	if out.InputTokens <= 0 || out.OutputTokens <= 0 {
		t.Fatalf("expected positive token accounting, got in=%d out=%d", out.InputTokens, out.OutputTokens)
	}
	// Stub is priced at zero.
	if out.CostMicros != 0 {
		t.Fatalf("stub cost = %d, want 0", out.CostMicros)
	}
}

func TestTutorRepliesReferenceQuestion(t *testing.T) {
	svc := NewService(NewStubProvider())
	out, err := svc.Tutor(context.Background(), TutorInput{
		CourseTitle: "Biology 101",
		Question:    "What is a chloroplast?",
	})
	if err != nil {
		t.Fatalf("Tutor: %v", err)
	}
	// The stub echoes the latest user question, proving the question reached
	// the provider through the Service's prompt assembly.
	if !strings.Contains(out.Text, "chloroplast") {
		t.Fatalf("tutor reply did not reference the question: %q", out.Text)
	}
	if out.PromptVersion != PromptTutorV1 {
		t.Fatalf("prompt version = %q, want %q", out.PromptVersion, PromptTutorV1)
	}
}

func TestNewServiceFromSettingsSelectsStubWhenUnconfigured(t *testing.T) {
	// Disabled → stub, regardless of key.
	if svc := NewServiceFromSettings(false, "anthropic", "sk-key", ""); svc.ProviderName() != "stub" {
		t.Fatalf("disabled feature should select stub, got %q", svc.ProviderName())
	}
	// Enabled but no key → stub.
	if svc := NewServiceFromSettings(true, "anthropic", "", ""); svc.ProviderName() != "stub" {
		t.Fatalf("missing key should select stub, got %q", svc.ProviderName())
	}
	// Enabled + anthropic + key → real provider.
	if svc := NewServiceFromSettings(true, "anthropic", "sk-key", ""); svc.ProviderName() != "anthropic" {
		t.Fatalf("configured feature should select anthropic, got %q", svc.ProviderName())
	}
}
