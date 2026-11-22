package models

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
		if rule.Mode != "level" && rule.Mode != "budget" {
			return nil, fmt.Errorf("model_gemini_generation rule %d: select level or budget mode", i+1)
		}
		seen := map[string]bool{}
		for _, pair := range rule.Mappings {
			key := strings.ToLower(strings.TrimSpace(pair.From))
			if strings.TrimSpace(pair.From) != pair.From || strings.TrimSpace(pair.To) != pair.To || key == "" || strings.TrimSpace(pair.To) == "" || seen[key] {
				return nil, fmt.Errorf("model_gemini_generation rule %d: enter a unique input effort and a target value", i+1)
			}
			seen[key] = true
			if rule.Mode == "budget" {
				n, err := strconv.Atoi(pair.To)
				if err != nil || n < -1 {
					return nil, fmt.Errorf("model_gemini_generation rule %d: budget must be an integer of -1 or greater", i+1)
				}
			}
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
