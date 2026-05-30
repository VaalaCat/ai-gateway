package pricing

import "encoding/json"

type ModelPricing struct {
	InputPrice      float64
	OutputPrice     float64
	CacheReadPrice  *float64
	CacheWritePrice *float64
}

// SourceModelPrice 是某个 modelID 在某个 provider 下的一条候选价。
// basellm 没有 provider 概念，Provider 留空。
type SourceModelPrice struct {
	Provider string
	Pricing  ModelPricing
}

// SourceData: modelID -> 该 modelID 在各 provider 下的全部候选。
// basellm 每个 modelID 一条；models.dev 每个 modelID 可能多条（多 provider 托管）。
type SourceData map[string][]SourceModelPrice

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

func ConvertBaseLLM(data []byte) (SourceData, error) {
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

func convertBaseLLMInner(raw baseLLMData) SourceData {
	result := make(SourceData, len(raw.ModelRatio)+len(raw.ModelPrice))
	put := func(name string, p ModelPricing) {
		result[name] = []SourceModelPrice{{Provider: "", Pricing: p}}
	}

	// Process LLM models from model_ratio (primary source, ~1840 models)
	for name, ratio := range raw.ModelRatio {
		put(name, baseLLMPricing(ratio, raw, name))
	}

	// Process non-LLM models from model_price (fixed-price models, ~54 models)
	for name, ratio := range raw.ModelPrice {
		if _, exists := result[name]; exists {
			continue // model_ratio takes precedence
		}
		put(name, baseLLMPricing(ratio, raw, name))
	}

	return result
}

func baseLLMPricing(ratio float64, raw baseLLMData, name string) ModelPricing {
	inputPrice := ratio * 2.0
	completionRatio := 1.0
	if cr, ok := raw.CompletionRatio[name]; ok {
		completionRatio = cr
	}
	p := ModelPricing{InputPrice: inputPrice, OutputPrice: inputPrice * completionRatio}
	if cacheRatio, ok := raw.CacheRatio[name]; ok {
		cr := inputPrice * cacheRatio
		p.CacheReadPrice = &cr
	}
	return p
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

func ConvertModelsDev(data []byte) (SourceData, error) {
	var raw map[string]modelsDevProvider
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	result := make(SourceData)
	for provName, provider := range raw {
		for modelID, model := range provider.Models {
			if model.Cost == nil || model.Cost.Input == nil || model.Cost.Output == nil {
				continue
			}
			result[modelID] = append(result[modelID], SourceModelPrice{
				Provider: provName,
				Pricing: ModelPricing{
					InputPrice:      *model.Cost.Input,
					OutputPrice:     *model.Cost.Output,
					CacheReadPrice:  model.Cost.CacheRead,
					CacheWritePrice: model.Cost.CacheWrite,
				},
			})
		}
	}
	return result, nil
}
