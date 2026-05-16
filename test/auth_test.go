package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuth_NoAuthHeader(t *testing.T) {
	env := setupFullEnv(t, "auth-noheader", 1)
	defer env.Close()

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	env.Router.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("expected 401 without auth header, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_InvalidKey(t *testing.T) {
	env := setupFullEnv(t, "auth-invalidkey", 1)
	defer env.Close()

	w := env.SendChat("sk-bogus-key-that-does-not-exist", "gpt-4o", "hello")
	if w.Code != 401 {
		t.Errorf("expected 401 with invalid key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_ValidKey(t *testing.T) {
	env := setupFullEnv(t, "auth-validkey", 1)
	defer env.Close()

	mockUpstream := mockOpenAIUpstream("hi there")
	defer mockUpstream.Close()

	userID := env.CreateUserWithQuota("authuser", 100000)
	apiKey := env.CreateToken(userID, "auth-tok")
	env.CreateChannel("auth-ch", 1, "k", mockUpstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	w := env.SendChat(apiKey, "gpt-4o", "hello")
	if w.Code != 200 {
		t.Errorf("expected 200 with valid key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_XAPIKeyHeader(t *testing.T) {
	env := setupFullEnv(t, "auth-x-api-key", 1)
	defer env.Close()

	mockUpstream := mockOpenAIUpstream("hi there")
	defer mockUpstream.Close()

	userID := env.CreateUserWithQuota("authxapikeyuser", 100000)
	apiKey := env.CreateToken(userID, "auth-x-api-key-tok")
	env.CreateChannel("auth-x-api-key-ch", 1, "k", mockUpstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	env.Router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 with x-api-key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_XAPIKeyHeaderForModels(t *testing.T) {
	env := setupFullEnv(t, "auth-x-api-key-models", 1)
	defer env.Close()

	mockUpstream := mockOpenAIUpstream("unused")
	defer mockUpstream.Close()

	userID := env.CreateUserWithQuota("authxapikeymodelsuser", 100000)
	apiKey := env.CreateToken(userID, "auth-x-api-key-models-tok")
	env.CreateChannel("auth-x-api-key-models-ch", 1, "k", mockUpstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("x-api-key", apiKey)
	env.Router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 for GET /v1/models with x-api-key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_TokenModelRestriction(t *testing.T) {
	env := setupFullEnv(t, "auth-modelrestrict", 1)
	defer env.Close()

	mockUpstream := mockOpenAIUpstream("ok")
	defer mockUpstream.Close()

	userID := env.CreateUserWithQuota("restrictuser", 100000)

	// Create token with model restriction via admin API
	resp := env.DoAdmin("POST", "/api/admin/tokens", map[string]any{
		"user_id": userID,
		"name":    "restricted-tok",
		"models":  `["gpt-3.5-turbo"]`,
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create restricted token: %d", resp.StatusCode)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	apiKey := tokenResp["key"].(string)

	env.CreateChannel("restrict-ch", 1, "k", mockUpstream.URL, "gpt-4o,gpt-3.5-turbo")
	env.CreateModelConfig("gpt-4o")
	env.CreateModelConfig("gpt-3.5-turbo")
	env.SyncFromMaster()

	t.Run("AllowedModel", func(t *testing.T) {
		w := env.SendChat(apiKey, "gpt-3.5-turbo", "hello")
		if w.Code != 200 {
			t.Errorf("expected 200 for allowed model, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("BlockedModel", func(t *testing.T) {
		w := env.SendChat(apiKey, "gpt-4o", "hello")
		if w.Code == 200 {
			t.Errorf("expected non-200 for blocked model, got 200")
		}
	})
}
