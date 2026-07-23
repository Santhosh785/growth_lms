package ai

import (
	"context"
	"strings"
)

// stubModel is the model ID reported by the stub provider. Recorded in the
// ledger like any other model, and priced at zero.
const stubModel = "stub-echo"

// StubProvider is a hermetic, network-free Provider. It is the default
// backend whenever the AI feature is dark (no API key, provider != anthropic,
// or LMS_AI_ENABLED false) and the only backend used in tests. Its output is
// deterministic and derived from the request, so tests can assert on it, and
// developers get a working end-to-end feature without an API key or spend.
type StubProvider struct{}

// NewStubProvider returns the stub backend.
func NewStubProvider() *StubProvider { return &StubProvider{} }

func (p *StubProvider) Name() string  { return "stub" }
func (p *StubProvider) Model() string { return stubModel }

// Generate echoes a short, deterministic response that names the system
// role and quotes the latest user turn, so callers see a plausibly-shaped
// reply and tests can assert the request reached the provider intact.
func (p *StubProvider) Generate(_ context.Context, req Request) (Result, error) {
	last := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == RoleUser {
			last = req.Messages[i].Content
			break
		}
	}
	var b strings.Builder
	b.WriteString("[stub-ai] AI is not configured on this platform, so this is a placeholder response.\n\n")
	if q := firstLine(last); q != "" {
		b.WriteString("You asked: ")
		b.WriteString(q)
		b.WriteString("\n\n")
	}
	b.WriteString("Configure LMS_AI_PROVIDER=anthropic and an API key to enable real generations.")
	text := b.String()
	return Result{
		Text:         text,
		InputTokens:  req.inputTokenEstimate(),
		OutputTokens: estimateTokens(text),
	}, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 200 {
		s = string([]rune(s)[:200]) + "…"
	}
	return s
}
