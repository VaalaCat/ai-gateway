package pricing

import (
	"sort"
	"strings"
)

type MatchResult struct {
	Pricing     ModelPricing
	MatchType   string // "exact" or "fuzzy"
	MatchedName string
}

func Match(modelName string, source map[string]ModelPricing) *MatchResult {
	// Phase 1: Exact match
	if p, ok := source[modelName]; ok {
		return &MatchResult{Pricing: p, MatchType: "exact", MatchedName: modelName}
	}

	// Phase 2: Fuzzy match — collect ALL candidates, pick best deterministically
	normalizedTarget := normalize(modelName)
	type candidate struct {
		name    string
		pricing ModelPricing
	}
	var candidates []candidate
	for sourceName, p := range source {
		if normalize(sourceName) == normalizedTarget {
			candidates = append(candidates, candidate{sourceName, p})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Deterministic selection: prefer shorter name (less provider prefix), then alphabetical
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].name) != len(candidates[j].name) {
			return len(candidates[i].name) < len(candidates[j].name)
		}
		return candidates[i].name < candidates[j].name
	})

	best := candidates[0]
	return &MatchResult{Pricing: best.pricing, MatchType: "fuzzy", MatchedName: best.name}
}

func normalize(name string) string {
	if idx := strings.Index(name, "--"); idx >= 0 {
		name = name[idx+2:]
	} else if idx := strings.Index(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.ReplaceAll(name, "--", "-")
	return strings.ToLower(name)
}
