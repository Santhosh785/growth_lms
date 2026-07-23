package ai

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider calls Claude via the official Go SDK. Thinking is left
// off (the default on Opus 4.8): these are bounded content-generation and
// tutor-reply tasks where the final text is what we want, and skipping
// thinking keeps latency and token spend predictable for an interactive,
// metered feature. Prompts are written to ask for a final answer directly.
type AnthropicProvider struct {
	client anthropic.Client
	model  string
}

// NewAnthropicProvider builds a Claude-backed provider. apiKey must be
// non-empty; model defaults to claude-opus-4-8 when blank.
func NewAnthropicProvider(apiKey, model string) *AnthropicProvider {
	if strings.TrimSpace(model) == "" {
		model = "claude-opus-4-8"
	}
	return &AnthropicProvider{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

func (p *AnthropicProvider) Name() string  { return "anthropic" }
func (p *AnthropicProvider) Model() string { return p.model }

// Generate issues one Messages API call and returns the concatenated text
// blocks plus exact token usage from the response.
func (p *AnthropicProvider) Generate(ctx context.Context, req Request) (Result, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		block := anthropic.NewTextBlock(m.Content)
		if m.Role == RoleAssistant {
			msgs = append(msgs, anthropic.NewAssistantMessage(block))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(block))
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
	}
	if strings.TrimSpace(req.System) != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return Result{}, wrapProviderErr(p.Name(), err)
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return Result{}, wrapProviderErr(p.Name(), errors.New("request was declined by safety classifiers"))
	}

	return Result{
		Text:         text.String(),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}, nil
}
