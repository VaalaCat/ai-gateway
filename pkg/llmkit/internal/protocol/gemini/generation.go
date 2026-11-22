package gemini

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type generationCapabilities convert.GeminiGenerationConfig

func (capabilities generationCapabilities) thinkingConfig(req *ir.Request) map[string]any {
	thinking := map[string]any{}
	disabled := req.ThinkingEnabled != nil && !*req.ThinkingEnabled
	effort := req.ReasoningEffort
	if disabled {
		effort = "none"
	}
	switch capabilities.Mode {
	case "level":
		if effort == "" && req.ThinkingBudget != nil && *req.ThinkingBudget == 0 {
			effort = "none"
		}
		if effort != "" {
			level := strings.ToUpper(effort)
			if mapped, ok := capabilities.lookup(effort); ok {
				level = mapped
			}
			thinking["thinkingLevel"] = level
		}
	case "budget":
		switch {
		case req.ThinkingBudget != nil:
			thinking["thinkingBudget"] = *req.ThinkingBudget
		default:
			if mapped, ok := capabilities.lookup(effort); ok {
				if budget, err := strconv.Atoi(mapped); err == nil && budget >= -1 {
					thinking["thinkingBudget"] = budget
				}
			} else if effort == "none" {
				thinking["thinkingBudget"] = 0
			} else if req.ThinkingEnabled != nil && *req.ThinkingEnabled {
				thinking["thinkingBudget"] = -1
			}
		}
	default:
		switch {
		case req.ThinkingBudget != nil:
			thinking["thinkingBudget"] = *req.ThinkingBudget
		case effort == "none":
			thinking["thinkingBudget"] = 0
		case effort != "":
			thinking["thinkingLevel"] = effort
		case req.ThinkingEnabled != nil && *req.ThinkingEnabled:
			thinking["thinkingBudget"] = -1
		}
	}
	if req.IncludeThoughts != nil {
		thinking["includeThoughts"] = *req.IncludeThoughts
	}
	return thinking
}

func (capabilities generationCapabilities) lookup(effort string) (string, bool) {
	for _, mapping := range capabilities.Mappings {
		if strings.EqualFold(mapping.From, effort) {
			return mapping.To, true
		}
	}
	return "", false
}

func (capabilities generationCapabilities) generationConfig(req *ir.Request) map[string]any {
	generation := map[string]any{}
	if req.MaxTokens != 0 {
		generation["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		generation["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		generation["topP"] = *req.TopP
	}
	if req.TopK != nil {
		generation["topK"] = *req.TopK
	}
	if req.Seed != nil {
		generation["seed"] = *req.Seed
	}
	if req.N != 0 {
		generation["candidateCount"] = req.N
	}
	if len(req.StopWords) > 0 {
		generation["stopSequences"] = req.StopWords
	}
	if req.FrequencyPenalty != nil {
		generation["frequencyPenalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		generation["presencePenalty"] = *req.PresencePenalty
	}
	if req.Logprobs != nil {
		generation["responseLogprobs"] = *req.Logprobs
	}
	if req.TopLogprobs != nil {
		generation["logprobs"] = *req.TopLogprobs
	}
	thinking := capabilities.thinkingConfig(req)
	if len(thinking) > 0 {
		generation["thinkingConfig"] = thinking
	}
	format := object(req.ResponseFormat)
	if len(format) == 0 {
		format = object(object(req.Extras["text"])["format"])
	}
	if format["type"] == "json_object" {
		generation["responseMimeType"] = "application/json"
	}
	if format["type"] == "json_schema" {
		generation["responseMimeType"] = "application/json"
		schema := object(format["json_schema"])
		if schema["schema"] != nil {
			generation["responseJsonSchema"] = schema["schema"]
		} else if format["schema"] != nil {
			generation["responseJsonSchema"] = format["schema"]
		}
	}
	return generation
}

func (capabilities generationCapabilities) mergeNativeConfig(generation, extras map[string]any) error {
	for _, key := range []string{"generation_config", "generationConfig"} {
		value, ok := extras[key]
		if !ok {
			continue
		}
		native := object(value)
		if count, ok := native["candidate_count"]; ok {
			generation["candidateCount"] = count
			delete(native, "candidate_count")
		}
		for name, value := range native {
			generation[name] = value
		}
	}
	if count, ok := generation["candidateCount"]; ok {
		// The decoder represents a single candidate. Check the effective wire value.
		n, err := strconv.ParseFloat(fmt.Sprint(count), 64)
		if err == nil && n > 1 {
			return fmt.Errorf("Gemini conversion supports one candidate per request")
		}
		if n == 1 {
			delete(generation, "candidateCount")
		}
	}
	return nil
}
