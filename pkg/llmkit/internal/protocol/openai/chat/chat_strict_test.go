package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestChatFunctionStrictFalseSurvivesEncodeAndDecode(t *testing.T) {
	strict := false
	request := &ir.Request{
		Model: "gpt-5",
		Tools: []ir.Tool{{
			Type:        "function",
			Name:        "top_level_function",
			Description: "must keep strict false",
			InputSchema: map[string]any{"type": "object"},
			Strict:      &strict,
		}},
	}

	encoded, err := (&handler{}).encodeHTTPRequest(request, &channelConfig{
		BaseURL: "https://upstream.example", APIKey: "key", Model: "gpt-5",
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(encoded.Body).Decode(&body); err != nil {
		t.Fatalf("decode encoded Chat request: %v", err)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("encoded tools = %#v, want one function", body["tools"])
	}
	function := tools[0].(map[string]any)["function"].(map[string]any)
	if got, ok := function["strict"].(bool); !ok || got {
		t.Fatalf("encoded strict = %#v, want false", function["strict"])
	}

	raw := `{"model":"gpt-5","messages":[],"tools":[{"type":"function","function":{"name":"top_level_function","parameters":{"type":"object"},"strict":false}}]}`
	decoded, err := (&handler{}).decodeHTTPRequest(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(raw)))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(decoded.Tools) != 1 || decoded.Tools[0].Strict == nil || *decoded.Tools[0].Strict {
		t.Fatalf("decoded tool = %#v, want strict false pointer", decoded.Tools)
	}

	// The exact parameters value is part of the function contract rather than
	// a formatter detail, so verify the request still carries an object schema.
	if got := decoded.Tools[0].InputSchema.(map[string]any)["type"]; got != "object" {
		t.Errorf("decoded parameters.type = %#v, want object", got)
	}
}
