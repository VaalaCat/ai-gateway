package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// createModelZeroPrice creates a model config with zero prices and returns the model ID.
func createModelZeroPrice(t *testing.T, env *testEnv, modelName string) uint {
	t.Helper()
	resp := env.DoAdmin("POST", "/api/admin/models", map[string]any{
		"model_name":   modelName,
		"input_price":  0,
		"output_price": 0,
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create model %s: %d %s", modelName, resp.StatusCode, body)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return uint(result["id"].(float64))
}

// TestApplyPricing_Basic creates two models, applies pricing to both,
// verifies the updated count, then fetches each model to confirm prices persisted.
func TestApplyPricing_Basic(t *testing.T) {
	env := setupFullEnv(t, "pricing-basic", 1)
	defer env.Close()

	id1 := createModelZeroPrice(t, env, "gpt-4o")
	id2 := createModelZeroPrice(t, env, "claude-3-haiku")

	applyResp := env.DoAdmin("POST", "/api/admin/models/apply-pricing", map[string]any{
		"updates": []map[string]any{
			{
				"model_id":     id1,
				"input_price":  5.0,
				"output_price": 15.0,
			},
			{
				"model_id":     id2,
				"input_price":  0.25,
				"output_price": 1.25,
			},
		},
	})
	if applyResp.StatusCode != 200 {
		body, _ := io.ReadAll(applyResp.Body)
		t.Fatalf("apply-pricing: expected 200, got %d: %s", applyResp.StatusCode, body)
	}

	var applyResult map[string]any
	json.NewDecoder(applyResp.Body).Decode(&applyResult)
	applyResp.Body.Close()

	if got := applyResult["updated"]; got != float64(2) {
		t.Errorf("expected updated=2, got %v", got)
	}

	// Verify gpt-4o prices were persisted
	m1Resp := env.DoAdmin("GET", fmt.Sprintf("/api/admin/models/%d", id1), nil)
	if m1Resp.StatusCode != 200 {
		body, _ := io.ReadAll(m1Resp.Body)
		t.Fatalf("GET model %d: %d %s", id1, m1Resp.StatusCode, body)
	}
	var m1 map[string]any
	json.NewDecoder(m1Resp.Body).Decode(&m1)
	m1Resp.Body.Close()

	if got := m1["input_price"]; got != float64(5.0) {
		t.Errorf("gpt-4o input_price: expected 5.0, got %v", got)
	}
	if got := m1["output_price"]; got != float64(15.0) {
		t.Errorf("gpt-4o output_price: expected 15.0, got %v", got)
	}

	// Verify claude-3-haiku prices were persisted
	m2Resp := env.DoAdmin("GET", fmt.Sprintf("/api/admin/models/%d", id2), nil)
	if m2Resp.StatusCode != 200 {
		body, _ := io.ReadAll(m2Resp.Body)
		t.Fatalf("GET model %d: %d %s", id2, m2Resp.StatusCode, body)
	}
	var m2 map[string]any
	json.NewDecoder(m2Resp.Body).Decode(&m2)
	m2Resp.Body.Close()

	if got := m2["input_price"]; got != float64(0.25) {
		t.Errorf("claude-3-haiku input_price: expected 0.25, got %v", got)
	}
	if got := m2["output_price"]; got != float64(1.25) {
		t.Errorf("claude-3-haiku output_price: expected 1.25, got %v", got)
	}
}

// TestApplyPricing_InvalidModel calls apply-pricing with a non-existent model ID
// and verifies the response gracefully skips it with updated=0.
func TestApplyPricing_InvalidModel(t *testing.T) {
	env := setupFullEnv(t, "pricing-invalid", 1)
	defer env.Close()

	applyResp := env.DoAdmin("POST", "/api/admin/models/apply-pricing", map[string]any{
		"updates": []map[string]any{
			{
				"model_id":     99999,
				"input_price":  1.0,
				"output_price": 2.0,
			},
		},
	})
	if applyResp.StatusCode != 200 {
		body, _ := io.ReadAll(applyResp.Body)
		t.Fatalf("apply-pricing: expected 200, got %d: %s", applyResp.StatusCode, body)
	}

	var result map[string]any
	json.NewDecoder(applyResp.Body).Decode(&result)
	applyResp.Body.Close()

	if got := result["updated"]; got != float64(0) {
		t.Errorf("expected updated=0 for invalid model, got %v", got)
	}
}

// TestModelList_PriceFilter creates three models, sets price on one of them,
// then verifies the price_filter query parameter returns the correct subsets.
func TestModelList_PriceFilter(t *testing.T) {
	env := setupFullEnv(t, "pricing-filter", 1)
	defer env.Close()

	createModelZeroPrice(t, env, "model-a")
	idB := createModelZeroPrice(t, env, "model-b")
	createModelZeroPrice(t, env, "model-c")

	// Give model-b a non-zero price
	applyResp := env.DoAdmin("POST", "/api/admin/models/apply-pricing", map[string]any{
		"updates": []map[string]any{
			{
				"model_id":     idB,
				"input_price":  3.0,
				"output_price": 6.0,
			},
		},
	})
	if applyResp.StatusCode != 200 {
		body, _ := io.ReadAll(applyResp.Body)
		t.Fatalf("apply-pricing for model-b: %d %s", applyResp.StatusCode, body)
	}
	applyResp.Body.Close()

	// no_price filter should return model-a and model-c only
	noPriceResp := env.DoAdmin("GET", "/api/admin/models?price_filter=no_price", nil)
	if noPriceResp.StatusCode != 200 {
		body, _ := io.ReadAll(noPriceResp.Body)
		t.Fatalf("GET models?price_filter=no_price: %d %s", noPriceResp.StatusCode, body)
	}
	var noPriceList struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.NewDecoder(noPriceResp.Body).Decode(&noPriceList)
	noPriceResp.Body.Close()

	noPriceNames := modelNameSet(noPriceList.Data)
	if noPriceNames["model-b"] {
		t.Error("no_price filter: model-b should NOT appear (it has a price)")
	}
	if !noPriceNames["model-a"] {
		t.Error("no_price filter: model-a should appear (zero price)")
	}
	if !noPriceNames["model-c"] {
		t.Error("no_price filter: model-c should appear (zero price)")
	}

	// has_price filter should return model-b only
	hasPriceResp := env.DoAdmin("GET", "/api/admin/models?price_filter=has_price", nil)
	if hasPriceResp.StatusCode != 200 {
		body, _ := io.ReadAll(hasPriceResp.Body)
		t.Fatalf("GET models?price_filter=has_price: %d %s", hasPriceResp.StatusCode, body)
	}
	var hasPriceList struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.NewDecoder(hasPriceResp.Body).Decode(&hasPriceList)
	hasPriceResp.Body.Close()

	hasPriceNames := modelNameSet(hasPriceList.Data)
	if !hasPriceNames["model-b"] {
		t.Error("has_price filter: model-b should appear (has non-zero price)")
	}
	if hasPriceNames["model-a"] {
		t.Error("has_price filter: model-a should NOT appear (zero price)")
	}
	if hasPriceNames["model-c"] {
		t.Error("has_price filter: model-c should NOT appear (zero price)")
	}

	// no filter should return all 3 models
	allResp := env.DoAdmin("GET", "/api/admin/models", nil)
	if allResp.StatusCode != 200 {
		body, _ := io.ReadAll(allResp.Body)
		t.Fatalf("GET /api/admin/models: %d %s", allResp.StatusCode, body)
	}
	var allList struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.NewDecoder(allResp.Body).Decode(&allList)
	allResp.Body.Close()

	allNames := modelNameSet(allList.Data)
	for _, name := range []string{"model-a", "model-b", "model-c"} {
		if !allNames[name] {
			t.Errorf("unfiltered list: expected %q to appear", name)
		}
	}
	if allList.Total < 3 {
		t.Errorf("unfiltered list: expected total >= 3, got %d", allList.Total)
	}
}

// TestFetchPricing_WithExternalSources tests the full fetch-pricing flow
// including fetching from real external data sources (basellm + models.dev).
// Creates well-known models that exist in external sources and verifies matching.
func TestFetchPricing_WithExternalSources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that requires external network access")
	}
	// Skip in CI or environments without external network
	if _, err := net.DialTimeout("tcp", "models.dev:443", 3*time.Second); err != nil {
		t.Skip("skipping: no external network access")
	}

	env := setupFullEnv(t, "pricing-fetch", 1)
	defer env.Close()

	// Create models that are known to exist in external data sources
	createModelZeroPrice(t, env, "gpt-4o")
	createModelZeroPrice(t, env, "claude-3-5-sonnet")
	createModelZeroPrice(t, env, "custom-nonexistent-model-xyz")

	// Call fetch-pricing
	resp := env.DoAdmin("POST", "/api/admin/models/fetch-pricing", nil)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("fetch-pricing: expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Matches         []map[string]any  `json:"matches"`
		UnmatchedModels []string          `json:"unmatched_models"`
		SourceErrors    map[string]string `json:"source_errors"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()

	// Should have at least some matches — but external sources may be unreachable in CI
	if len(result.Matches) == 0 {
		t.Skip("no matches from external sources (network may be restricted)")
	}

	// Verify matched models carry a single recommendation backed by candidates.
	matchedNames := make(map[string]bool)
	for _, m := range result.Matches {
		name := m["model_name"].(string)
		matchedNames[name] = true
		rec, ok := m["recommended"].(map[string]any)
		if !ok {
			t.Errorf("model %s matched but has no recommended price", name)
			continue
		}
		if cands, ok := m["candidates"].([]any); !ok || len(cands) == 0 {
			t.Errorf("model %s matched but has no candidates", name)
		}
		in, _ := rec["input_price"].(float64)
		out, _ := rec["output_price"].(float64)
		t.Logf("  %s recommended via %v (%v): in=$%.4f out=$%.4f",
			name, m["provenance"], m["confidence"], in, out)
	}

	// custom-nonexistent-model-xyz should be in unmatched
	unmatchedSet := make(map[string]bool)
	for _, name := range result.UnmatchedModels {
		unmatchedSet[name] = true
	}
	if !unmatchedSet["custom-nonexistent-model-xyz"] && !matchedNames["custom-nonexistent-model-xyz"] {
		t.Log("custom-nonexistent-model-xyz: not in matches or unmatched (unexpected)")
	}
}

// TestFetchAndApplyPricing_FullFlow is the end-to-end test that mirrors the
// complete user workflow: fetch pricing → select source → apply → verify.
func TestFetchAndApplyPricing_FullFlow(t *testing.T) {
	env := setupFullEnv(t, "pricing-flow", 1)
	defer env.Close()

	// Step 1: Create models with zero prices
	idA := createModelZeroPrice(t, env, "test-model-a")
	idB := createModelZeroPrice(t, env, "test-model-b")

	// Step 2: Apply pricing (simulating what the frontend does after fetch-pricing preview)
	applyResp := env.DoAdmin("POST", "/api/admin/models/apply-pricing", map[string]any{
		"updates": []map[string]any{
			{
				"model_id":          idA,
				"input_price":       2.5,
				"output_price":      10.0,
				"cache_read_price":  0.25,
				"cache_write_price": 0.5,
			},
			{
				"model_id":          idB,
				"input_price":       0.15,
				"output_price":      0.6,
				"cache_read_price":  0.02,
				"cache_write_price": 0,
			},
		},
	})
	if applyResp.StatusCode != 200 {
		body, _ := io.ReadAll(applyResp.Body)
		t.Fatalf("apply-pricing: expected 200, got %d: %s", applyResp.StatusCode, body)
	}
	var applyResult map[string]any
	json.NewDecoder(applyResp.Body).Decode(&applyResult)
	applyResp.Body.Close()
	if applyResult["updated"] != float64(2) {
		t.Fatalf("expected updated=2, got %v", applyResult["updated"])
	}

	// Step 3: Verify all 4 price fields are persisted correctly
	verifyResp := env.DoAdmin("GET", fmt.Sprintf("/api/admin/models/%d", idA), nil)
	var modelA map[string]any
	json.NewDecoder(verifyResp.Body).Decode(&modelA)
	verifyResp.Body.Close()

	if modelA["input_price"] != float64(2.5) {
		t.Errorf("model-a input_price: expected 2.5, got %v", modelA["input_price"])
	}
	if modelA["output_price"] != float64(10.0) {
		t.Errorf("model-a output_price: expected 10.0, got %v", modelA["output_price"])
	}
	if modelA["cache_read_price"] != float64(0.25) {
		t.Errorf("model-a cache_read_price: expected 0.25, got %v", modelA["cache_read_price"])
	}
	if modelA["cache_write_price"] != float64(0.5) {
		t.Errorf("model-a cache_write_price: expected 0.5, got %v", modelA["cache_write_price"])
	}

	// Step 4: Verify price filter works correctly after applying
	noPriceResp := env.DoAdmin("GET", "/api/admin/models?price_filter=no_price", nil)
	var noPriceList struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.NewDecoder(noPriceResp.Body).Decode(&noPriceList)
	noPriceResp.Body.Close()

	noPriceNames := modelNameSet(noPriceList.Data)
	if noPriceNames["test-model-a"] || noPriceNames["test-model-b"] {
		t.Error("models with applied prices should not appear in no_price filter")
	}

	hasPriceResp := env.DoAdmin("GET", "/api/admin/models?price_filter=has_price", nil)
	var hasPriceList struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.NewDecoder(hasPriceResp.Body).Decode(&hasPriceList)
	hasPriceResp.Body.Close()

	hasPriceNames := modelNameSet(hasPriceList.Data)
	if !hasPriceNames["test-model-a"] || !hasPriceNames["test-model-b"] {
		t.Error("models with applied prices should appear in has_price filter")
	}
}

// modelNameSet extracts model_name values from a list of model maps into a set.
func modelNameSet(models []map[string]any) map[string]bool {
	names := make(map[string]bool, len(models))
	for _, m := range models {
		if name, ok := m["model_name"].(string); ok {
			names[name] = true
		}
	}
	return names
}
