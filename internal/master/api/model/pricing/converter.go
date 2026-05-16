package pricing

import "encoding/json"

type ModelPricing struct {
	InputPrice      float64
	OutputPrice     float64
	CacheReadPrice  *float64
	CacheWritePrice *float64
}

const (
	BaseLLMURL   = "https://basellm.github.io/llm-metadata/api/newapi/ratio_config-v1-base.json"
	ModelsDevURL = "https://models.dev/api.json"
)

type baseLLMData struct {
	ModelRatio      map[string]float64 `json:"model_ratio"`      // LLM input ratio (1840 models)
	ModelPrice      map[string]float64 `json:"model_price"`      // Non-LLM fixed-price models (54 models)
	CompletionRatio map[string]float64 `json:"completion_ratio"` // Output/input ratio
	CacheRatio      map[string]float64 `json:"cache_ratio"`      // Cache read/input ratio
}

type baseLLMWrapper struct {
	Data    baseLLMData `json:"data"`
	Success bool        `json:"success"`
}

func ConvertBaseLLM(data []byte) (map[string]ModelPricing, error) {
	// Try wrapped format first: {"data": {...}, "success": true}
	var wrapper baseLLMWrapper
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Success {
		return convertBaseLLMInner(wrapper.Data), nil
	}
	// Fallback: direct format (for tests)
	var raw baseLLMData
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return convertBaseLLMInner(raw), nil
}

func convertBaseLLMInner(raw baseLLMData) map[string]ModelPricing {
	result := make(map[string]ModelPricing, len(raw.ModelRatio)+len(raw.ModelPrice))

	// Process LLM models from model_ratio (primary source, ~1840 models)
	// Formula: input_price = model_ratio × 2, output_price = input_price × completion_ratio
	for name, ratio := range raw.ModelRatio {
		inputPrice := ratio * 2.0
		completionRatio := 1.0
		if cr, ok := raw.CompletionRatio[name]; ok {
			completionRatio = cr
		}
		outputPrice := inputPrice * completionRatio
		p := ModelPricing{InputPrice: inputPrice, OutputPrice: outputPrice}
		if cacheRatio, ok := raw.CacheRatio[name]; ok {
			cr := inputPrice * cacheRatio
			p.CacheReadPrice = &cr
		}
		result[name] = p
	}

	// Process non-LLM models from model_price (fixed-price models, ~54 models)
	// Same formula applies
	for name, ratio := range raw.ModelPrice {
		if _, exists := result[name]; exists {
			continue // model_ratio takes precedence
		}
		inputPrice := ratio * 2.0
		completionRatio := 1.0
		if cr, ok := raw.CompletionRatio[name]; ok {
			completionRatio = cr
		}
		outputPrice := inputPrice * completionRatio
		p := ModelPricing{InputPrice: inputPrice, OutputPrice: outputPrice}
		if cacheRatio, ok := raw.CacheRatio[name]; ok {
			cr := inputPrice * cacheRatio
			p.CacheReadPrice = &cr
		}
		result[name] = p
	}

	return result
}

// models.dev structure: {"provider": {"models": {"model_id": {"cost": {...}}}}}
type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	Cost *modelsDevCost `json:"cost"`
}

type modelsDevCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

func ConvertModelsDev(data []byte) (map[string]ModelPricing, error) {
	var raw map[string]modelsDevProvider
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	result := make(map[string]ModelPricing)
	for _, provider := range raw {
		for modelID, model := range provider.Models {
			if model.Cost == nil || model.Cost.Input == nil || model.Cost.Output == nil {
				continue
			}
			result[modelID] = ModelPricing{
				InputPrice:      *model.Cost.Input,
				OutputPrice:     *model.Cost.Output,
				CacheReadPrice:  model.Cost.CacheRead,
				CacheWritePrice: model.Cost.CacheWrite,
			}
		}
	}
	return result, nil
}
