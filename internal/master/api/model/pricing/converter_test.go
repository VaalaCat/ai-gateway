package pricing

import (
	"encoding/json"
	"math"
	"testing"
)

func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

func TestConvertBaseLLM(t *testing.T) {
	raw := baseLLMData{
		ModelRatio:      map[string]float64{"gpt-4o": 1.25, "claude-3-5-sonnet": 1.5},
		CompletionRatio: map[string]float64{"gpt-4o": 4.0},
		CacheRatio:      map[string]float64{"claude-3-5-sonnet": 0.1},
	}
	data, _ := json.Marshal(raw)
	result, err := ConvertBaseLLM(data)
	if err != nil {
		t.Fatalf("ConvertBaseLLM: %v", err)
	}
	gpt := result["gpt-4o"]
	if len(gpt) != 1 || gpt[0].Provider != "" {
		t.Fatalf("gpt-4o: want 1 candidate w/ empty provider, got %+v", gpt)
	}
	if gpt[0].Pricing.InputPrice != 2.5 || gpt[0].Pricing.OutputPrice != 10.0 {
		t.Errorf("gpt-4o price: got %+v", gpt[0].Pricing)
	}
	claude := result["claude-3-5-sonnet"][0].Pricing
	if claude.InputPrice != 3.0 || claude.OutputPrice != 3.0 {
		t.Errorf("claude price: got %+v", claude)
	}
	if claude.CacheReadPrice == nil || !approxEqual(*claude.CacheReadPrice, 0.3, 1e-9) {
		t.Errorf("claude cache read: got %v", claude.CacheReadPrice)
	}
}

func TestConvertBaseLLM_InvalidJSON(t *testing.T) {
	_, err := ConvertBaseLLM([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestConvertModelsDev(t *testing.T) {
	input := 5.0
	output := 15.0
	cacheRead := 0.5

	// models.dev format: {"provider": {"models": {"model_id": {"cost": {...}}}}}
	raw := map[string]modelsDevProvider{
		"openai": {
			Models: map[string]modelsDevModel{
				"gpt-4o": {
					Cost: &modelsDevCost{
						Input:      &input,
						Output:     &output,
						CacheRead:  &cacheRead,
						CacheWrite: nil,
					},
				},
				"no-cost-model": {
					Cost: nil,
				},
				"partial-model": {
					Cost: &modelsDevCost{
						Input:  &input,
						Output: nil,
					},
				},
			},
		},
	}

	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := ConvertModelsDev(data)
	if err != nil {
		t.Fatalf("ConvertModelsDev: %v", err)
	}

	// gpt-4o should be present with direct cost values
	gpt4o := result["gpt-4o"]
	if len(gpt4o) != 1 {
		t.Fatalf("gpt-4o: want 1 candidate, got %d", len(gpt4o))
	}
	if gpt4o[0].Pricing.InputPrice != 5.0 {
		t.Errorf("gpt-4o input: got %v, want 5.0", gpt4o[0].Pricing.InputPrice)
	}
	if gpt4o[0].Pricing.OutputPrice != 15.0 {
		t.Errorf("gpt-4o output: got %v, want 15.0", gpt4o[0].Pricing.OutputPrice)
	}
	if gpt4o[0].Pricing.CacheReadPrice == nil || *gpt4o[0].Pricing.CacheReadPrice != 0.5 {
		t.Errorf("gpt-4o cache read: got %v, want 0.5", gpt4o[0].Pricing.CacheReadPrice)
	}
	if gpt4o[0].Pricing.CacheWritePrice != nil {
		t.Errorf("gpt-4o cache write: got %v, want nil", gpt4o[0].Pricing.CacheWritePrice)
	}

	// no-cost-model and partial-model should be skipped
	if len(result["no-cost-model"]) != 0 {
		t.Error("no-cost-model should be skipped")
	}
	if len(result["partial-model"]) != 0 {
		t.Error("partial-model (missing output) should be skipped")
	}
}

func TestConvertModelsDev_MultiProvider(t *testing.T) {
	in1, out1, in2, out2 := 2.5, 10.0, 5.0, 20.0
	raw := map[string]modelsDevProvider{
		"openai": {Models: map[string]modelsDevModel{
			"gpt-4o": {Cost: &modelsDevCost{Input: &in1, Output: &out1}},
		}},
		"azure": {Models: map[string]modelsDevModel{
			"gpt-4o": {Cost: &modelsDevCost{Input: &in2, Output: &out2}},
		}},
	}
	data, _ := json.Marshal(raw)
	result, err := ConvertModelsDev(data)
	if err != nil {
		t.Fatalf("ConvertModelsDev: %v", err)
	}
	if len(result["gpt-4o"]) != 2 {
		t.Fatalf("gpt-4o: want 2 provider candidates, got %d", len(result["gpt-4o"]))
	}
}

func TestConvertModelsDev_InvalidJSON(t *testing.T) {
	_, err := ConvertModelsDev([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
