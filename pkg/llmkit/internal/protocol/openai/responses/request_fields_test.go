package responses

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestResponsesRequestFieldsRoundTrip(t *testing.T) {
	inbound, err := http.NewRequest(http.MethodPost, "http://client/v1/responses", strings.NewReader(`{
		"model":"gpt-5","input":"hello","service_tier":"priority",
		"safety_identifier":"user-123","stream_options":{"include_obfuscation":false,"include_usage":true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	handler := &handler{}
	request, err := handler.decodeHTTPRequest(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if request.ServiceTier != "priority" || request.SafetyIdentifier != "user-123" || request.StreamOptions["include_usage"] != true {
		t.Fatalf("request fields were not decoded: %#v", request)
	}

	outbound, err := handler.encodeHTTPRequest(request, &channelConfig{BaseURL: "https://upstream", APIKey: "k", Model: request.Model})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(outbound.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	streamOptions, ok := decoded["stream_options"].(map[string]any)
	if decoded["service_tier"] != "priority" || decoded["safety_identifier"] != "user-123" || !ok || streamOptions["include_usage"] != true {
		t.Fatalf("request fields were not encoded: %#v", decoded)
	}
}

func TestResponsesRequestReasoningRoundTrip(t *testing.T) {
	tests := []struct {
		name          string
		reasoning     string // 为空表示请求体不带 reasoning
		wantReasoning map[string]any
	}{
		{name: "all_turns", reasoning: `{"context":"all_turns"}`, wantReasoning: map[string]any{"context": "all_turns"}},
		{name: "current_turn_with_effort", reasoning: `{"effort":"high","context":"current_turn"}`, wantReasoning: map[string]any{"effort": "high", "context": "current_turn"}},
		{name: "auto", reasoning: `{"context":"auto"}`, wantReasoning: map[string]any{"context": "auto"}},
		{name: "all_fields", reasoning: `{"effort":"low","summary":"auto","context":"all_turns"}`, wantReasoning: map[string]any{"effort": "low", "summary": "auto", "context": "all_turns"}},
		{name: "effort_only", reasoning: `{"effort":"low"}`, wantReasoning: map[string]any{"effort": "low"}},
		{name: "reasoning_missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"model":"gpt-5","input":"hello"`
			if tt.reasoning != "" {
				body += `,"reasoning":` + tt.reasoning
			}
			body += `}`
			inbound, err := http.NewRequest(http.MethodPost, "http://client/v1/responses", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			handler := &handler{}
			request, err := handler.decodeHTTPRequest(inbound)
			if err != nil {
				t.Fatal(err)
			}
			outbound, err := handler.encodeHTTPRequest(request, &channelConfig{BaseURL: "https://upstream", APIKey: "k", Model: request.Model})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := io.ReadAll(outbound.Body)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			got, exists := decoded["reasoning"]
			if tt.wantReasoning == nil {
				if exists {
					t.Fatalf("reasoning = %#v, want absent", got)
				}
				return
			}
			if !reflect.DeepEqual(got, any(tt.wantReasoning)) {
				t.Fatalf("reasoning = %#v, want %#v", got, tt.wantReasoning)
			}
		})
	}
}
