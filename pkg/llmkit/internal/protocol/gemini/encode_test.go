package gemini

import (
	"encoding/json"
	"testing"

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
			raw, err := encodeRequest(&tc.req, "m")
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
	raw, err := encodeRequest(req, "m")
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	config := body["generationConfig"].(map[string]any)
	require.Equal(t, []any{"IMAGE", "TEXT"}, config["responseModalities"])
	require.Equal(t, "application/json", config["responseMimeType"])
	require.Equal(t, map[string]any{"type": "object"}, config["responseJsonSchema"])
	require.NotContains(t, body, "tools")
}
