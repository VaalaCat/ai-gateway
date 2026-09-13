package convert

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestProjectUsageOpenAIFromClaudeBuckets(t *testing.T) {
	got := ProjectUsageOpenAI(&ir.Usage{
		PromptTokens:     19,
		CompletionTokens: 4096,
		CacheReadTokens:  51648,
		TotalTokens:      4115,
	})

	if got.PromptTokens != 51667 {
		t.Errorf("PromptTokens = %d, want 51667", got.PromptTokens)
	}
	if got.CompletionTokens != 4096 {
		t.Errorf("CompletionTokens = %d, want 4096", got.CompletionTokens)
	}
	if got.TotalTokens != 55763 {
		t.Errorf("TotalTokens = %d, want 55763", got.TotalTokens)
	}
	if got.CachedTokens != 51648 {
		t.Errorf("CachedTokens = %d, want 51648", got.CachedTokens)
	}
}

func TestProjectUsageOpenAIKeepsOpenAIBuckets(t *testing.T) {
	got := ProjectUsageOpenAI(&ir.Usage{
		PromptTokens:     200,
		CompletionTokens: 50,
		TotalTokens:      250,
		CachedTokens:     80,
	})

	if got.PromptTokens != 200 || got.TotalTokens != 250 || got.CachedTokens != 80 {
		t.Fatalf("unexpected projection: %+v", got)
	}
}
