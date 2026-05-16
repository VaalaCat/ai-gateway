package upstream

import "testing"

func TestMergeTokenCounts_BothProviderValues(t *testing.T) {
	r := mergeTokenCounts(100, 200, 90, 180)
	if r.PromptTokens != 100 || r.CompletionTokens != 200 {
		t.Errorf("expected provider values, got prompt=%d completion=%d", r.PromptTokens, r.CompletionTokens)
	}
	if r.Source != TokenSourceProvider {
		t.Errorf("expected source %q, got %q", TokenSourceProvider, r.Source)
	}
}

func TestMergeTokenCounts_BothZero_EstimatedAvailable(t *testing.T) {
	r := mergeTokenCounts(0, 0, 50, 100)
	if r.PromptTokens != 50 || r.CompletionTokens != 100 {
		t.Errorf("expected estimated values, got prompt=%d completion=%d", r.PromptTokens, r.CompletionTokens)
	}
	if r.Source != TokenSourceEstimated {
		t.Errorf("expected source %q, got %q", TokenSourceEstimated, r.Source)
	}
}

func TestMergeTokenCounts_PartialEstimated_PromptZero(t *testing.T) {
	r := mergeTokenCounts(0, 200, 50, 180)
	if r.PromptTokens != 50 || r.CompletionTokens != 200 {
		t.Errorf("expected partial, got prompt=%d completion=%d", r.PromptTokens, r.CompletionTokens)
	}
	if r.Source != TokenSourcePartialEstimated {
		t.Errorf("expected source %q, got %q", TokenSourcePartialEstimated, r.Source)
	}
}

func TestMergeTokenCounts_PartialEstimated_CompletionZero(t *testing.T) {
	r := mergeTokenCounts(100, 0, 90, 50)
	if r.PromptTokens != 100 || r.CompletionTokens != 50 {
		t.Errorf("expected partial, got prompt=%d completion=%d", r.PromptTokens, r.CompletionTokens)
	}
	if r.Source != TokenSourcePartialEstimated {
		t.Errorf("expected source %q, got %q", TokenSourcePartialEstimated, r.Source)
	}
}

func TestMergeTokenCounts_ProviderDivergent(t *testing.T) {
	// Provider=100, estimated=300 → divergence 66% > 50%
	r := mergeTokenCounts(100, 200, 300, 200)
	if r.PromptTokens != 100 {
		t.Errorf("divergent should use provider value, got %d", r.PromptTokens)
	}
	if r.Source != TokenSourceProviderDivergent {
		t.Errorf("expected source %q, got %q", TokenSourceProviderDivergent, r.Source)
	}
}

func TestMergeTokenCounts_AllZero(t *testing.T) {
	r := mergeTokenCounts(0, 0, 0, 0)
	if r.Source != TokenSourceNone {
		t.Errorf("expected source %q, got %q", TokenSourceNone, r.Source)
	}
}

func TestMergeTokenCounts_ProviderZero_EstimatedAlsoZero_OneField(t *testing.T) {
	// Prompt: provider=0, estimated=0. Completion: provider=200, estimated=180.
	r := mergeTokenCounts(0, 200, 0, 180)
	if r.PromptTokens != 0 || r.CompletionTokens != 200 {
		t.Errorf("got prompt=%d completion=%d", r.PromptTokens, r.CompletionTokens)
	}
	// Both have provider value or both are 0 — provider is the source
	if r.Source != TokenSourceProvider {
		t.Errorf("expected source %q, got %q", TokenSourceProvider, r.Source)
	}
}

func TestEstimatePromptTokens_SimpleMessages(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hello world"}]}`)
	tokens := EstimatePromptTokens(body, "gpt-4o")
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestEstimatePromptTokens_StructuredContent(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"Hello"},{"type":"text","text":"World"}]}]}`)
	tokens := EstimatePromptTokens(body, "gpt-4o")
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestEstimatePromptTokens_EmptyBody(t *testing.T) {
	tokens := EstimatePromptTokens(nil, "gpt-4o")
	if tokens != 0 {
		t.Errorf("expected 0 for nil body, got %d", tokens)
	}
}

func TestEstimatePromptTokens_EmptyMessages(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[]}`)
	tokens := EstimatePromptTokens(body, "gpt-4o")
	if tokens != 0 {
		t.Errorf("expected 0 for empty messages, got %d", tokens)
	}
}

func TestEstimatePromptTokens_GeminiModel(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"请问今天天气怎么样？"}]}`)
	tokens := EstimatePromptTokens(body, "gemini-2.5-flash")
	if tokens <= 0 {
		t.Errorf("expected positive token count for Gemini model, got %d", tokens)
	}
}

func TestEstimateCompletionTokens(t *testing.T) {
	tokens := EstimateCompletionTokens("Hello, how can I help you today?", "gpt-4o")
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestEstimateCompletionTokens_Empty(t *testing.T) {
	tokens := EstimateCompletionTokens("", "gpt-4o")
	if tokens != 0 {
		t.Errorf("expected 0 for empty text, got %d", tokens)
	}
}

func TestFinalizeTokenCountsProviderReported(t *testing.T) {
	// success: provider reported both → use provider numbers
	body := []byte(`{"model":"gpt-4","messages":[]}`)
	tc := FinalizeTokenCounts(body, 10, 20, "", "gpt-4")
	if tc.PromptTokens != 10 || tc.CompletionTokens != 20 {
		t.Errorf("provider numbers should win, got prompt=%d completion=%d", tc.PromptTokens, tc.CompletionTokens)
	}
	if tc.Source == "" {
		t.Error("Source should be set")
	}
}

func TestFinalizeTokenCountsEstimatedOnly(t *testing.T) {
	// failure/fallback: provider returned 0 → estimate from body+response
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	tc := FinalizeTokenCounts(body, 0, 0, "Hello, world!", "gpt-4")
	if tc.PromptTokens == 0 && tc.CompletionTokens == 0 {
		t.Errorf("estimation should give nonzero counts when body+response non-empty")
	}
}

func TestFinalizeTokenCountsBoundaryEmptyInputs(t *testing.T) {
	// boundary: nil body + empty result → all zeros, Source=None
	tc := FinalizeTokenCounts(nil, 0, 0, "", "gpt-4")
	if tc.PromptTokens != 0 || tc.CompletionTokens != 0 {
		t.Errorf("nil inputs → zero counts, got %+v", tc)
	}
	if tc.Source != TokenSourceNone {
		t.Errorf("Source should be None, got %v", tc.Source)
	}
}

func TestFinalizeTokenCountsBoundaryPartialEstimated(t *testing.T) {
	// boundary: provider reported prompt only, no completion → PartialEstimated
	body := []byte(`{"model":"gpt-4"}`)
	tc := FinalizeTokenCounts(body, 15, 0, "response text", "gpt-4")
	if tc.PromptTokens != 15 {
		t.Errorf("prompt should remain 15, got %d", tc.PromptTokens)
	}
	if tc.CompletionTokens == 0 {
		t.Errorf("completion should be estimated, got 0")
	}
	if tc.Source != TokenSourcePartialEstimated {
		t.Errorf("Source should be PartialEstimated, got %v", tc.Source)
	}
}
