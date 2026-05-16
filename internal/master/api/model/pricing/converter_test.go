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
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := ConvertBaseLLM(data)
	if err != nil {
		t.Fatalf("ConvertBaseLLM: %v", err)
	}

	// gpt-4o: ratio=1.25 → input=2.5, completion_ratio=4 → output=10.0
	gpt4o, ok := result["gpt-4o"]
	if !ok {
		t.Fatal("gpt-4o not found in result")
	}
	if gpt4o.InputPrice != 2.5 {
		t.Errorf("gpt-4o input price: got %v, want 2.5", gpt4o.InputPrice)
	}
	if gpt4o.OutputPrice != 10.0 {
		t.Errorf("gpt-4o output price: got %v, want 10.0", gpt4o.OutputPrice)
	}
	if gpt4o.CacheReadPrice != nil {
		t.Errorf("gpt-4o cache read price: got %v, want nil", gpt4o.CacheReadPrice)
	}

	// claude: ratio=1.5 → input=3.0, no completion_ratio → output=3.0, cache_ratio=0.1 → cache_read=0.3
	claude, ok := result["claude-3-5-sonnet"]
	if !ok {
		t.Fatal("claude-3-5-sonnet not found in result")
	}
	if claude.InputPrice != 3.0 {
		t.Errorf("claude input price: got %v, want 3.0", claude.InputPrice)
	}
	if claude.OutputPrice != 3.0 {
		t.Errorf("claude output price: got %v, want 3.0", claude.OutputPrice)
	}
	if claude.CacheReadPrice == nil {
		t.Fatal("claude cache read price: got nil, want non-nil")
	}
	// cache_read = 3.0 * 0.1 = 0.3 (approximate due to float64 arithmetic)
	if !approxEqual(*claude.CacheReadPrice, 0.3, 1e-9) {
		t.Errorf("claude cache read price: got %v, want ~0.3", *claude.CacheReadPrice)
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
	gpt4o, ok := result["gpt-4o"]
	if !ok {
		t.Fatal("gpt-4o not found")
	}
	if gpt4o.InputPrice != 5.0 {
		t.Errorf("gpt-4o input: got %v, want 5.0", gpt4o.InputPrice)
	}
	if gpt4o.OutputPrice != 15.0 {
		t.Errorf("gpt-4o output: got %v, want 15.0", gpt4o.OutputPrice)
	}
	if gpt4o.CacheReadPrice == nil || *gpt4o.CacheReadPrice != 0.5 {
		t.Errorf("gpt-4o cache read: got %v, want 0.5", gpt4o.CacheReadPrice)
	}
	if gpt4o.CacheWritePrice != nil {
		t.Errorf("gpt-4o cache write: got %v, want nil", gpt4o.CacheWritePrice)
	}

	// no-cost-model and partial-model should be skipped
	if _, ok := result["no-cost-model"]; ok {
		t.Error("no-cost-model should be skipped")
	}
	if _, ok := result["partial-model"]; ok {
		t.Error("partial-model (missing output) should be skipped")
	}
}

func TestConvertModelsDev_InvalidJSON(t *testing.T) {
	_, err := ConvertModelsDev([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
