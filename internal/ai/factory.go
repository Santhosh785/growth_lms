package ai

import "strings"

// NewServiceFromSettings selects a Provider from configuration and wraps it
// in a Service. It returns the stub provider unless the feature is enabled,
// the provider is "anthropic", AND an API key is present — so a
// misconfiguration degrades to the harmless stub rather than failing at
// startup or leaking a placeholder into real generations.
func NewServiceFromSettings(enabled bool, provider, apiKey, model string) *Service {
	if enabled && strings.EqualFold(strings.TrimSpace(provider), "anthropic") && strings.TrimSpace(apiKey) != "" {
		return NewService(NewAnthropicProvider(apiKey, model))
	}
	return NewService(NewStubProvider())
}
