package convert

import "github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"

// OpenAIUsageTokens is an IR usage projected onto OpenAI wire semantics.
type OpenAIUsageTokens struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
}

// ProjectUsageOpenAI converts IR usage to OpenAI conventions.
//
// IR keeps cache tokens in disjoint buckets (Claude convention: PromptTokens
// excludes cache; CacheReadTokens/CacheWriteTokens are separate counters),
// while OpenAI reports prompt/input tokens as the FULL input including cached
// tokens and carries the cached subset in *_tokens_details.cached_tokens.
// CachedTokens is only set by OpenAI decoders, where PromptTokens already
// includes the cached subset.
func ProjectUsageOpenAI(u *ir.Usage) OpenAIUsageTokens {
	if u == nil {
		return OpenAIUsageTokens{}
	}
	out := OpenAIUsageTokens{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.CachedTokens,
	}
	if u.CachedTokens == 0 && u.CacheReadTokens > 0 {
		// Claude-style disjoint buckets: fold cache reads back into the input
		// total. Cache creation has no OpenAI wire field and is not part of this
		// input_tokens/cache-details mapping.
		out.PromptTokens = u.PromptTokens + u.CacheReadTokens
		out.CachedTokens = u.CacheReadTokens
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
		return out
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	return out
}
