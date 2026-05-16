package test

import (
	"encoding/json"
	"testing"
)

// TestProtocol_ClaudeInboundToClaudeUpstream verifies that a Claude Messages
// request sent to /v1/messages is forwarded to a Claude upstream and returns
// a Claude-format response without any protocol conversion.
func TestProtocol_ClaudeInboundToClaudeUpstream(t *testing.T) {
	upstream := mockClaudeUpstream("Hello from Claude!")
	defer upstream.Close()

	env := setupFullEnv(t, "proto-claude-claude", 1)
	defer env.Close()

	userID := env.CreateUserWithQuota("proto_cc_user", 100000)
	apiKey := env.CreateToken(userID, "proto-cc-token")
	env.CreateChannel("claude-chan", 14, "test-key", upstream.URL, "claude-sonnet-4-20250514")
	env.CreateModelConfig("claude-sonnet-4-20250514")
	env.SyncFromMaster()

	body, _ := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 100,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})

	w := env.SendRaw(apiKey, "POST", "/v1/messages", body, nil)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify Claude Messages response format
	if resp["type"] != "message" {
		t.Errorf("expected type=message, got %v", resp["type"])
	}

	content, ok := resp["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected non-empty content array, got %v", resp["content"])
	}

	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected content block to be object, got %T", content[0])
	}
	if block["type"] != "text" {
		t.Errorf("expected content block type=text, got %v", block["type"])
	}
	if block["text"] != "Hello from Claude!" {
		t.Errorf("expected text='Hello from Claude!', got %v", block["text"])
	}

	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage object, got %v", resp["usage"])
	}
	if usage["input_tokens"] == nil || usage["output_tokens"] == nil {
		t.Errorf("expected input_tokens and output_tokens in usage, got %v", usage)
	}
}

// TestProtocol_OpenAIChatToClaudeUpstream verifies cross-protocol conversion:
// an OpenAI Chat Completions request is sent to /v1/chat/completions, routed
// to a Claude upstream, and the Claude response is converted back to OpenAI format.
func TestProtocol_OpenAIChatToClaudeUpstream(t *testing.T) {
	upstream := mockClaudeUpstream("Hello from Claude via OpenAI!")
	defer upstream.Close()

	env := setupFullEnv(t, "proto-openai-claude", 1)
	defer env.Close()

	userID := env.CreateUserWithQuota("proto_oc_user", 100000)
	apiKey := env.CreateToken(userID, "proto-oc-token")
	env.CreateChannel("claude-chan-oc", 14, "test-key", upstream.URL, "claude-sonnet-4-20250514")
	env.CreateModelConfig("claude-sonnet-4-20250514")
	env.SyncFromMaster()

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})

	w := env.SendRaw(apiKey, "POST", "/v1/chat/completions", body, nil)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify OpenAI Chat Completions response format
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("expected non-empty choices array, got %v", resp["choices"])
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("expected choice to be object, got %T", choices[0])
	}

	message, ok := choice["message"].(map[string]any)
	if !ok {
		t.Fatalf("expected message object in choice, got %v", choice["message"])
	}
	if message["content"] != "Hello from Claude via OpenAI!" {
		t.Errorf("expected content='Hello from Claude via OpenAI!', got %v", message["content"])
	}
}

// TestProtocol_ClaudeInboundToOpenAIUpstream verifies cross-protocol conversion:
// a Claude Messages request is sent to /v1/messages, routed to an OpenAI upstream,
// and the OpenAI response is converted back to Claude Messages format.
func TestProtocol_ClaudeInboundToOpenAIUpstream(t *testing.T) {
	upstream := mockOpenAIUpstream("Hello from OpenAI via Claude!")
	defer upstream.Close()

	env := setupFullEnv(t, "proto-claude-openai", 1)
	defer env.Close()

	userID := env.CreateUserWithQuota("proto_co_user", 100000)
	apiKey := env.CreateToken(userID, "proto-co-token")
	env.CreateChannel("openai-chan-co", 1, "test-key", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	body, _ := json.Marshal(map[string]any{
		"model":      "gpt-4o",
		"max_tokens": 100,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})

	w := env.SendRaw(apiKey, "POST", "/v1/messages", body, nil)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify Claude Messages response format
	if resp["type"] != "message" {
		t.Errorf("expected type=message, got %v", resp["type"])
	}

	content, ok := resp["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected non-empty content array, got %v", resp["content"])
	}

	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected content block to be object, got %T", content[0])
	}
	if block["type"] != "text" {
		t.Errorf("expected content block type=text, got %v", block["type"])
	}
	if block["text"] != "Hello from OpenAI via Claude!" {
		t.Errorf("expected text='Hello from OpenAI via Claude!', got %v", block["text"])
	}

	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage object, got %v", resp["usage"])
	}
	if usage["input_tokens"] == nil || usage["output_tokens"] == nil {
		t.Errorf("expected input_tokens and output_tokens in usage, got %v", usage)
	}
}
