package upstream

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/service"
)

var initTokenizerOnce sync.Once

// ensureTokenizerInit lazily initializes the new-api token encoder on first use.
func ensureTokenizerInit() {
	initTokenizerOnce.Do(service.InitTokenEncoders)
}

// TokenSource indicates the origin of token counts in a usage log entry.
type TokenSource string

const (
	// TokenSourceProvider means the upstream provider reported both prompt and completion tokens.
	TokenSourceProvider TokenSource = "provider"
	// TokenSourceEstimated means the upstream did not report tokens; local estimation was used.
	TokenSourceEstimated TokenSource = "estimated"
	// TokenSourcePartialEstimated means one of prompt/completion came from the provider,
	// the other was locally estimated.
	TokenSourcePartialEstimated TokenSource = "partial_estimated"
	// TokenSourceProviderDivergent means the provider reported tokens but they diverge
	// significantly (>50%) from local estimates.
	TokenSourceProviderDivergent TokenSource = "provider_divergent"
	// TokenSourceNone means both provider and estimated values are zero.
	TokenSourceNone TokenSource = "none"
)

// mergeTokenCounts compares provider-reported token counts with local estimates,
// applies fallback when the provider reports zero, and determines the source label.
func mergeTokenCounts(providerPrompt, providerCompletion, estimatedPrompt, estimatedCompletion int) TokenCounts {
	finalPrompt := providerPrompt
	finalCompletion := providerCompletion
	promptEstimated := false
	completionEstimated := false

	if providerPrompt == 0 && estimatedPrompt > 0 {
		finalPrompt = estimatedPrompt
		promptEstimated = true
	}
	if providerCompletion == 0 && estimatedCompletion > 0 {
		finalCompletion = estimatedCompletion
		completionEstimated = true
	}

	var source TokenSource
	switch {
	case finalPrompt == 0 && finalCompletion == 0:
		source = TokenSourceNone
	case promptEstimated && completionEstimated:
		source = TokenSourceEstimated
	case promptEstimated || completionEstimated:
		source = TokenSourcePartialEstimated
	default:
		source = TokenSourceProvider
		if isDivergent(providerPrompt, estimatedPrompt) || isDivergent(providerCompletion, estimatedCompletion) {
			source = TokenSourceProviderDivergent
		}
	}

	return TokenCounts{
		PromptTokens:     finalPrompt,
		CompletionTokens: finalCompletion,
		Source:           source,
	}
}

// isDivergent returns true if the provider and estimated values differ by more than 50%.
// Returns false if either value is zero, since no meaningful comparison is possible
// (e.g., estimation failed or the field was not applicable).
func isDivergent(provider, estimated int) bool {
	if provider == 0 || estimated == 0 {
		return false
	}
	diff := provider - estimated
	if diff < 0 {
		diff = -diff
	}
	max := provider
	if estimated > max {
		max = estimated
	}
	return float64(diff)/float64(max) > 0.5
}

// messageForEstimate is a minimal struct to extract text from request messages.
type messageForEstimate struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// EstimatePromptTokens extracts message text from a request body and estimates
// the prompt token count using new-api's CountTextToken.
func EstimatePromptTokens(bodyBytes []byte, model string) int {
	if len(bodyBytes) == 0 {
		return 0
	}
	ensureTokenizerInit()

	var req struct {
		Messages []messageForEstimate `json:"messages"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil || len(req.Messages) == 0 {
		return 0
	}

	var totalTokens int
	for _, msg := range req.Messages {
		text := extractMessageText(msg.Content)
		if text != "" {
			totalTokens += service.CountTextToken(text, model)
		}
		// Per-message formatting overhead (matches new-api convention)
		totalTokens += 3
	}
	return totalTokens
}

// extractMessageText extracts plain text from a message content field.
// Handles both string content and structured content array.
func extractMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try array of content parts
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}

	return ""
}

// EstimateCompletionTokens estimates the completion token count from response text.
func EstimateCompletionTokens(responseText string, model string) int {
	if responseText == "" {
		return 0
	}
	ensureTokenizerInit()
	return service.CountTextToken(responseText, model)
}

// TokenCounts is the output of FinalizeTokenCounts — finalized prompt/completion + source.
type TokenCounts struct {
	PromptTokens     int
	CompletionTokens int
	Source           TokenSource
}

// FinalizeTokenCounts merges provider-reported counts with locally-estimated counts,
// picks the best values, and labels the source.
//
// Takes primitive parameters (promptTokens, completionTokens, responseText) instead
// of the relay-layer AttemptResult struct to keep this package free of upward
// dependencies on the relay package.
func FinalizeTokenCounts(body []byte, promptTokens, completionTokens int, responseText, model string) TokenCounts {
	estPrompt := EstimatePromptTokens(body, model)
	estCompletion := EstimateCompletionTokens(responseText, model)
	return mergeTokenCounts(promptTokens, completionTokens, estPrompt, estCompletion)
}
