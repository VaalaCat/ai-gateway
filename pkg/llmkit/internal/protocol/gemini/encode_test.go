package gemini

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func TestEncodeRequestToolsThinkingAndExtensions(t *testing.T) {
	req := &ir.Request{Model: "alias", Stream: true, ThinkingEnabled: ptr(false), ThinkingBudget: ptr(-25), ReasoningEffort: "future-level", IncludeThoughts: ptr(false), Temperature: ptr(0.7), MaxTokens: 100,
		Messages: []ir.Message{ir.TextMessage(ir.RoleSystem, "system"), ir.TextMessage(ir.RoleUser, "draw"), {Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{{ID: "a", Name: "read", Arguments: `{"path":"a"}`, ProviderData: &ir.ProviderData{Protocol: ir.ProtocolGemini, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"signed"`)}}}, {ID: "b", Name: "read", Arguments: `{"path":"b"}`}}}, {Role: ir.RoleTool, ToolCallID: "a", Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: `{"ok":true}`}}}, {Role: ir.RoleTool, ToolCallID: "b", Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "plain result"}}}},
		Tools:    []ir.Tool{{Name: "read", InputSchema: map[string]any{"type": "object"}}}, ToolChoice: &ir.ToolChoice{Type: "function", Name: "read"}, Extras: map[string]any{"generationConfig": map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}, "futureField": true}, "safetySettings": []any{map[string]any{"category": "x"}}, "futureExtension": 42}}
	encoded, state, err := NewHandler().EncodeRequest(protocol.EncodeRequestInput{Request: req, Target: protocol.Target{Model: "gemini-image", APIKey: "key", EndpointPath: "/proxy/models/{model}:{action}?custom=yes"}})
	require.NoError(t, err)
	require.Equal(t, "gemini-image", state.(conversionState).model)
	require.Equal(t, "/proxy/models/gemini-image:streamGenerateContent?alt=sse&custom=yes", encoded.Path)
	require.Equal(t, []string{"key"}, encoded.Headers["X-Goog-Api-Key"])
	var body map[string]any
	require.NoError(t, json.Unmarshal(encoded.Body, &body))
	contents := body["contents"].([]any)
	require.Len(t, contents, 3)
	calls := contents[1].(map[string]any)["parts"].([]any)
	require.Len(t, calls, 2)
	require.Equal(t, "signed", calls[0].(map[string]any)["thoughtSignature"])
	results := contents[2].(map[string]any)["parts"].([]any)
	require.Len(t, results, 2)
	require.Equal(t, "read", results[1].(map[string]any)["functionResponse"].(map[string]any)["name"])
	config := body["generationConfig"].(map[string]any)
	thinking := config["thinkingConfig"].(map[string]any)
	require.Equal(t, float64(-25), thinking["thinkingBudget"])
	require.Equal(t, false, thinking["includeThoughts"])
	require.NotContains(t, thinking, "thinkingLevel")
	require.Equal(t, true, config["futureField"])
	require.Equal(t, float64(42), body["futureExtension"])
	require.NotContains(t, body, "model")
}

func TestEncodeThinkingPermissive(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  ir.Request
		want map[string]any
	}{
		{"unspecified", ir.Request{}, nil},
		{"false", ir.Request{ThinkingEnabled: ptr(false)}, map[string]any{"thinkingBudget": float64(0)}},
		{"zero explicit", ir.Request{ThinkingEnabled: ptr(true), ThinkingBudget: ptr(0)}, map[string]any{"thinkingBudget": float64(0)}},
		{"adaptive", ir.Request{ThinkingEnabled: ptr(true)}, map[string]any{"thinkingBudget": float64(-1)}},
		{"unknown effort", ir.Request{ReasoningEffort: "xhigh"}, map[string]any{"thinkingLevel": "xhigh"}},
		{"include false", ir.Request{IncludeThoughts: ptr(false)}, map[string]any{"includeThoughts": false}},
		{"native conflict preserved", ir.Request{Extras: map[string]any{"generationConfig": map[string]any{"thinkingConfig": map[string]any{"thinkingLevel": "future", "thinkingBudget": -99}}}}, map[string]any{"thinkingLevel": "future", "thinkingBudget": float64(-99)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := encodeRequest(&tc.req, "m", convert.Options{})
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, json.Unmarshal(raw, &body))
			config, _ := body["generationConfig"].(map[string]any)
			if tc.want == nil {
				require.Nil(t, config["thinkingConfig"])
			} else {
				require.Equal(t, tc.want, config["thinkingConfig"])
			}
		})
	}
}

func TestEncodeImagePositionAndSignature(t *testing.T) {
	data := func(index, offset int, model string) *ir.ProviderData {
		p, _ := json.Marshal(contentPosition{PartIndex: index, TextOffset: offset})
		return &ir.ProviderData{Protocol: ir.ProtocolGemini, Models: []string{model}, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"sig"`), "content_position": p}}
	}
	for _, tc := range []struct {
		name     string
		offset   int
		explicit bool
		model    string
		want     []string
	}{
		{"restore", 3, false, "m", []string{"你", "image", "好"}},
		{"bad UTF8 hint ignored", 1, false, "m", []string{"你好", "image"}},
		{"explicit array", 3, true, "m", []string{"你好", "image"}},
		{"other endpoint signature preserved", 3, false, "other", []string{"你", "image", "好"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "你好", Metadata: map[string]any{"explicit_content_order": tc.explicit}}, {Type: ir.ContentTypeImage, MediaB64: "AA==", MimeType: "image/webp", ProviderData: data(1, tc.offset, tc.model)}}}
			parts, err := encodeMessage(m, "m")
			require.NoError(t, err)
			var values []string
			for _, p := range parts {
				if p.Text != nil {
					values = append(values, *p.Text)
				} else {
					values = append(values, "image")
					require.Equal(t, "image/webp", p.InlineData.MimeType)
					require.JSONEq(t, `"sig"`, string(p.ThoughtSignature))
				}
			}
			require.Equal(t, tc.want, values)
		})
	}
}

func TestImageGenerationAndJSONSchema(t *testing.T) {
	req := &ir.Request{Tools: []ir.Tool{{Type: "image_generation"}}, ResponseFormat: map[string]any{"type": "json_schema", "json_schema": map[string]any{"schema": map[string]any{"type": "object"}}}, Extras: map[string]any{"modalities": []string{"image", "text"}}}
	raw, err := encodeRequest(req, "m", convert.Options{})
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	config := body["generationConfig"].(map[string]any)
	require.Equal(t, []any{"IMAGE", "TEXT"}, config["responseModalities"])
	require.Equal(t, "application/json", config["responseMimeType"])
	require.Equal(t, map[string]any{"type": "object"}, config["responseJsonSchema"])
	require.NotContains(t, body, "tools")
}

func TestEncodeConfiguredGenerationSettings(t *testing.T) {
	options := convert.Options{GeminiGeneration: convert.GeminiGenerationConfig{
		Mode: "level", Mappings: []convert.ThinkingLevelMapping{{From: "none", To: "LOW"}, {From: "minimal", To: "LOW"}, {From: "xhigh", To: "HIGH"}},
	}}
	for _, tc := range []struct {
		name, effort string
		budget       *int
		enabled      *bool
		want         string
	}{
		{"remapped effort", "xhigh", nil, nil, "HIGH"},
		{"minimal effort", "minimal", nil, nil, "LOW"},
		{"disabled", "", nil, ptr(false), "LOW"},
		{"future effort", "future", nil, nil, "FUTURE"},
		{"zero budget", "", ptr(0), nil, "LOW"},
		{"positive budget", "", ptr(512), nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := encodeRequest(&ir.Request{
				N: 1, ThinkingBudget: tc.budget, ThinkingEnabled: tc.enabled,
				ReasoningEffort: tc.effort, IncludeThoughts: ptr(true),
				FrequencyPenalty: ptr(0.0), PresencePenalty: ptr(0.0),
			}, "custom-endpoint", options)
			require.NoError(t, err)
			var payload struct {
				GenerationConfig map[string]any `json:"generationConfig"`
			}
			require.NoError(t, json.Unmarshal(body, &payload))
			require.NotContains(t, payload.GenerationConfig, "candidateCount")
			require.Equal(t, float64(0), payload.GenerationConfig["frequencyPenalty"])
			require.Equal(t, float64(0), payload.GenerationConfig["presencePenalty"])
			thinking := payload.GenerationConfig["thinkingConfig"].(map[string]any)
			require.NotContains(t, thinking, "thinkingBudget")
			require.Equal(t, true, thinking["includeThoughts"])
			if tc.want == "" {
				require.NotContains(t, thinking, "thinkingLevel")
			} else {
				require.Equal(t, tc.want, thinking["thinkingLevel"])
			}
		})
	}
	_, err := encodeRequest(&ir.Request{N: 2}, "gemini-4-argon", convert.Options{})
	require.ErrorContains(t, err, "supports one candidate")
}

func TestEncodeUnconfiguredModelGenerationConfig(t *testing.T) {
	for _, model := range []string{"gemini-3.1-pro", "gemini-4-argon", "gemini-4-carbon", "gemini-5-new-model", "custom-4.1-carbon"} {
		t.Run(model, func(t *testing.T) {
			raw, err := encodeRequest(&ir.Request{
				N: 1, ThinkingBudget: ptr(32), FrequencyPenalty: ptr(0.5), PresencePenalty: ptr(0.25),
			}, model, convert.Options{})
			require.NoError(t, err)
			var body struct {
				GenerationConfig json.RawMessage `json:"generationConfig"`
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			require.JSONEq(t, `{"frequencyPenalty":0.5,"presencePenalty":0.25,"thinkingConfig":{"thinkingBudget":32}}`, string(body.GenerationConfig))
		})
	}
}

func TestThinkingBudgetMapping(t *testing.T) {
	cfg := generationCapabilities{Mode: "budget", Mappings: []convert.ThinkingLevelMapping{{From: "high", To: "2048"}}}
	require.Equal(t, 2048, cfg.thinkingConfig(&ir.Request{ReasoningEffort: "HIGH"})["thinkingBudget"])
	require.Equal(t, 64, cfg.thinkingConfig(&ir.Request{ReasoningEffort: "HIGH", ThinkingBudget: ptr(64)})["thinkingBudget"])
	require.Empty(t, cfg.thinkingConfig(&ir.Request{}))
}
