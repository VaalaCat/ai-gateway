package transform

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

// runClaudeToOpenAIChat decodes a Claude inbound request, applies IR transformers,
// then encodes to openai_chat outbound. Returns the upstream body parsed as a
// JSON object.
func runClaudeToOpenAIChat(t *testing.T, claudeReqBody string, sendBackThinking bool) map[string]any {
	t.Helper()
	httpReq := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader([]byte(claudeReqBody)))
	httpReq.Header.Set("Content-Type", "application/json")

	cdec := &claude.ClaudeCodec{}
	irReq, err := cdec.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("claude DecodeRequest: %v", err)
	}

	cfg := &codec.ChannelConfig{
		BaseURL:          "https://x",
		APIKey:           "k",
		Model:            "deepseek-v4-test",
		SendBackThinking: sendBackThinking,
	}
	codec.ApplyIRTransformers(codec.ProtocolOpenAIChat, irReq, cfg)

	enc := &openai.ChatCodec{}
	upstreamReq, err := enc.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("openai EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(upstreamReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal upstream body: %v", err)
	}
	return raw
}

// firstAssistant returns the first message in the upstream body whose role is "assistant".
func firstAssistant(t *testing.T, raw map[string]any) (map[string]any, bool) {
	t.Helper()
	msgs, _ := raw["messages"].([]any)
	for _, m := range msgs {
		mm := m.(map[string]any)
		if r, _ := mm["role"].(string); r == "assistant" {
			return mm, true
		}
	}
	return nil, false
}

func TestRegression_ClaudeInboundDeepSeekOutbound_PreservesThinking(t *testing.T) {
	body := `{
		"model": "claude-3-7",
		"max_tokens": 100,
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "let me think..."},
				{"type": "text", "text": "answer"}
			]}
		]
	}`
	raw := runClaudeToOpenAIChat(t, body, true)
	asst, ok := firstAssistant(t, raw)
	if !ok {
		t.Fatal("no assistant message in upstream body")
	}
	rc, present := asst["reasoning_content"]
	if !present {
		t.Fatalf("reasoning_content missing; expected to be present with thinking text. assistant=%v", asst)
	}
	if !strings.Contains(rc.(string), "let me think") {
		t.Fatalf("reasoning_content = %q, want to contain 'let me think'", rc)
	}
}

func TestRegression_AnthropicThinkingNotLeakedToOpenAI(t *testing.T) {
	body := `{
		"model": "claude-3-7",
		"max_tokens": 100,
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "private"},
				{"type": "text", "text": "answer"}
			]}
		]
	}`
	raw := runClaudeToOpenAIChat(t, body, false) // SendBackThinking=false (default)
	asst, ok := firstAssistant(t, raw)
	if !ok {
		t.Fatal("no assistant message in upstream body")
	}
	if _, present := asst["reasoning_content"]; present {
		t.Fatalf("reasoning_content leaked to OpenAI when SendBackThinking=false: %v", asst["reasoning_content"])
	}
}

// TestRegression_DeepSeekToolCallMultiTurn_HistoryHasReasoningContent_DirectIR
// builds the IR directly (rather than going through Claude inbound, which uses
// a different tool_use format) to verify the placeholder semantics for tool_call
// assistant messages.
func TestRegression_DeepSeekToolCallMultiTurn_HistoryHasReasoningContent_DirectIR(t *testing.T) {
	irReq := &codec.Request{
		Model: "deepseek-v4",
		Messages: []codec.Message{
			{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "use tool"}}},
			{
				Role:      codec.RoleAssistant,
				Content:   []codec.ContentBlock{{Type: codec.ContentTypeText, Text: ""}},
				ToolCalls: []codec.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}},
			},
			{Role: codec.RoleTool, ToolCallID: "c1", Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "result"}}},
		},
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "https://x", APIKey: "k", Model: "deepseek-v4",
		SendBackThinking: true,
	}
	codec.ApplyIRTransformers(codec.ProtocolOpenAIChat, irReq, cfg)

	enc := &openai.ChatCodec{}
	upstreamReq, err := enc.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(upstreamReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	asst, ok := firstAssistant(t, raw)
	if !ok {
		t.Fatal("no assistant message in upstream body")
	}
	rc, present := asst["reasoning_content"]
	if !present {
		t.Fatal("reasoning_content field missing on tool_call assistant — placeholder not added")
	}
	if s, _ := rc.(string); s != "" {
		t.Fatalf("placeholder reasoning_content should be empty string, got %q", s)
	}
}
