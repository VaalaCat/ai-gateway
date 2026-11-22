package models

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

type GeminiGenerationRule struct {
	ModelPattern *regexp.Regexp `json:"model_pattern"`
	llmkit.GeminiGenerationConfig
}

type GeminiGenerationRules []GeminiGenerationRule

func ParseGeminiGenerationRules(settings string) (GeminiGenerationRules, error) {
	var other map[string]json.RawMessage
	if json.Unmarshal([]byte(settings), &other) != nil {
		return nil, nil
	}
	raw, ok := other["model_gemini_generation"]
	if !ok {
		return nil, nil
	}
	var rules GeminiGenerationRules
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("model_gemini_generation: %w", err)
	}
	for i, rule := range rules {
		if rule.ModelPattern == nil || rule.ModelPattern.String() == "" {
			return nil, fmt.Errorf("model_gemini_generation rule %d: model_pattern is required", i+1)
		}
	}
	return rules, nil
}

func (rules GeminiGenerationRules) Match(modelNames ...string) llmkit.GeminiGenerationConfig {
	for i := range rules {
		for _, name := range modelNames {
			if rules[i].ModelPattern.MatchString(name) {
				return rules[i].GeminiGenerationConfig
			}
		}
	}
	return llmkit.GeminiGenerationConfig{}
}
