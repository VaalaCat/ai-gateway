package test

import (
	"encoding/json"
	"testing"
)

func TestListModels_NoAuth(t *testing.T) {
	env := setupFullEnv(t, "models-noauth", 1)
	defer env.Close()

	w := env.SendRaw("", "GET", "/v1/models", nil, nil)
	if w.Code != 401 {
		t.Errorf("expected 401 without auth, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListModels_AllModels(t *testing.T) {
	env := setupFullEnv(t, "models-all", 1)
	defer env.Close()

	upstream := mockOpenAIUpstream("ok")
	defer upstream.Close()

	userID := env.CreateUserWithQuota("modelsuser", 100000)
	apiKey := env.CreateToken(userID, "models-tok")
	env.CreateChannel("models-ch", 1, "k", upstream.URL, "gpt-4o,gpt-3.5-turbo,claude-sonnet")
	env.CreateModelConfig("gpt-4o")
	env.CreateModelConfig("gpt-3.5-turbo")
	env.CreateModelConfig("claude-sonnet")
	env.SyncFromMaster()

	w := env.ListModels(apiKey)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Object != "list" {
		t.Errorf("expected object=list, got %q", resp.Object)
	}

	modelIDs := make(map[string]bool)
	for _, m := range resp.Data {
		modelIDs[m.ID] = true
	}

	for _, expected := range []string{"gpt-4o", "gpt-3.5-turbo", "claude-sonnet"} {
		if !modelIDs[expected] {
			t.Errorf("expected model %q in response, got models: %v", expected, modelIDs)
		}
	}
}

func TestListModels_ExactModelRestriction(t *testing.T) {
	env := setupFullEnv(t, "models-exact", 1)
	defer env.Close()

	upstream := mockOpenAIUpstream("ok")
	defer upstream.Close()

	userID := env.CreateUserWithQuota("exactuser", 100000)

	// Create token restricted to gpt-3.5-turbo only
	resp := env.DoAdmin("POST", "/api/admin/tokens", map[string]any{
		"user_id": userID,
		"name":    "exact-tok",
		"models":  `["gpt-3.5-turbo"]`,
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create token: %d", resp.StatusCode)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	apiKey := tokenResp["key"].(string)

	env.CreateChannel("exact-ch", 1, "k", upstream.URL, "gpt-4o,gpt-3.5-turbo,claude-sonnet")
	env.CreateModelConfig("gpt-4o")
	env.CreateModelConfig("gpt-3.5-turbo")
	env.CreateModelConfig("claude-sonnet")
	env.SyncFromMaster()

	w := env.ListModels(apiKey)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)

	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 model, got %d: %v", len(listResp.Data), listResp.Data)
	}
	if listResp.Data[0].ID != "gpt-3.5-turbo" {
		t.Errorf("expected gpt-3.5-turbo, got %q", listResp.Data[0].ID)
	}
}

func TestListModels_RegexModelRestriction(t *testing.T) {
	env := setupFullEnv(t, "models-regex", 1)
	defer env.Close()

	upstream := mockOpenAIUpstream("ok")
	defer upstream.Close()

	userID := env.CreateUserWithQuota("regexuser", 100000)

	// Create token with regex pattern gpt-4.* (matches gpt-4o, gpt-4o-mini, etc.)
	resp := env.DoAdmin("POST", "/api/admin/tokens", map[string]any{
		"user_id": userID,
		"name":    "regex-tok",
		"models":  `["gpt-4.*"]`,
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create token: %d", resp.StatusCode)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	apiKey := tokenResp["key"].(string)

	env.CreateChannel("regex-ch", 1, "k", upstream.URL, "gpt-4o,gpt-4o-mini,gpt-3.5-turbo,claude-sonnet")
	env.CreateModelConfig("gpt-4o")
	env.CreateModelConfig("gpt-4o-mini")
	env.CreateModelConfig("gpt-3.5-turbo")
	env.CreateModelConfig("claude-sonnet")
	env.SyncFromMaster()

	w := env.ListModels(apiKey)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)

	modelIDs := make(map[string]bool)
	for _, m := range listResp.Data {
		modelIDs[m.ID] = true
	}

	// Should include gpt-4o and gpt-4o-mini (match gpt-4.*)
	if !modelIDs["gpt-4o"] {
		t.Error("expected gpt-4o in filtered results")
	}
	if !modelIDs["gpt-4o-mini"] {
		t.Error("expected gpt-4o-mini in filtered results")
	}

	// Should NOT include gpt-3.5-turbo or claude-sonnet
	if modelIDs["gpt-3.5-turbo"] {
		t.Error("gpt-3.5-turbo should not be in filtered results")
	}
	if modelIDs["claude-sonnet"] {
		t.Error("claude-sonnet should not be in filtered results")
	}
}

func TestListModels_EmptyWhenNoChannels(t *testing.T) {
	env := setupFullEnv(t, "models-empty", 1)
	defer env.Close()

	userID := env.CreateUserWithQuota("emptyuser", 100000)
	apiKey := env.CreateToken(userID, "empty-tok")
	env.SyncFromMaster()

	w := env.ListModels(apiKey)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var listResp struct {
		Object string `json:"object"`
		Data   []any  `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)

	if resp := listResp.Data; resp != nil && len(resp) > 0 {
		t.Errorf("expected empty data, got %d models", len(resp))
	}
}
