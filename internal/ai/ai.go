// Package ai is Task 9's provider-abstracted AI authoring & tutor layer.
// It has two responsibilities and deliberately no others:
//
//   - a narrow Provider interface (Generate + token accounting) with two
//     implementations — a real Anthropic-backed provider and a hermetic
//     stub used in development, tests, and whenever the feature is dark;
//   - a Service that turns the four product operations (course outline,
//     lesson draft, quiz generation, course-scoped tutor reply) into
//     versioned prompts, calls the provider, and reports token usage and
//     cost so callers can log and meter every request.
//
// Everything database-facing (feature-flag gating, usage-limit enforcement,
// the ai_generations ledger, tutor persistence) lives in the handler and
// model layers, not here — this package never touches Postgres, which keeps
// it unit-testable with the stub provider alone.
package ai

import (
	"context"
	"fmt"
)

// Role is a chat turn author. Only user/assistant cross the Provider
// boundary; the system prompt is a separate Request field.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one conversation turn.
type Message struct {
	Role    Role
	Content string
}

// Request is a single model call: a system prompt, the running
// conversation, and an output ceiling.
type Request struct {
	System    string
	Messages  []Message
	MaxTokens int
}

// Result is what a Provider returns: the generated text plus the token
// counts needed for cost accounting and usage metering.
type Result struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// Provider is the single seam every AI backend implements. Name/Model are
// recorded verbatim into the ai_generations ledger.
type Provider interface {
	Name() string
	Model() string
	Generate(ctx context.Context, req Request) (Result, error)
}

// Kind identifies which product operation a generation is — mirrors the
// ai_generations.kind CHECK constraint in migration 000010.
type Kind string

const (
	KindOutline Kind = "outline"
	KindLesson  Kind = "lesson"
	KindQuiz    Kind = "quiz"
	KindTutor   Kind = "tutor"
)

// modelPricing is per-token cost in micro-USD (1 USD = 1_000_000 micros),
// keyed by model ID. Claude Opus 4.8 is $5/1M input and $25/1M output —
// i.e. 5 and 25 micros per token respectively (see the claude-api pricing
// table). The stub model has zero cost. Unknown models fall back to the
// Opus rate so an untabulated model over-reports rather than under-reports.
type modelPricing struct {
	inputMicrosPerToken  int64
	outputMicrosPerToken int64
}

var pricing = map[string]modelPricing{
	"claude-opus-4-8":  {inputMicrosPerToken: 5, outputMicrosPerToken: 25},
	"claude-opus-4-7":  {inputMicrosPerToken: 5, outputMicrosPerToken: 25},
	"claude-sonnet-5":  {inputMicrosPerToken: 3, outputMicrosPerToken: 15},
	"claude-haiku-4-5": {inputMicrosPerToken: 1, outputMicrosPerToken: 5},
	stubModel:          {inputMicrosPerToken: 0, outputMicrosPerToken: 0},
}

// CostMicros returns the micro-USD cost of a generation on the given model.
func CostMicros(model string, inputTokens, outputTokens int) int64 {
	p, ok := pricing[model]
	if !ok {
		p = pricing["claude-opus-4-8"]
	}
	return int64(inputTokens)*p.inputMicrosPerToken + int64(outputTokens)*p.outputMicrosPerToken
}

// estimateTokens is a coarse len/4 heuristic used only by the stub provider
// (the real Anthropic provider reports exact usage from the API response).
func estimateTokens(s string) int {
	n := len([]rune(s)) / 4
	if n < 1 {
		return 1
	}
	return n
}

func (r Request) inputTokenEstimate() int {
	total := estimateTokens(r.System)
	for _, m := range r.Messages {
		total += estimateTokens(m.Content)
	}
	return total
}

// wrapProviderErr gives provider errors a stable prefix for logs.
func wrapProviderErr(name string, err error) error {
	return fmt.Errorf("ai: provider %s generate: %w", name, err)
}
