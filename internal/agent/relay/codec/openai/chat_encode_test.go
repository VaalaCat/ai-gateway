package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Chat stream encode helpers
// ---------------------------------------------------------------------------

// chatChunk is a minimal representation of an OpenAI chat completion chunk.
type chatChunk struct {
	Choices []chatChunkChoice `json:"choices"`
}

type chatChunkChoice struct {
	Delta        chatChunkDelta `json:"delta"`
	FinishReason string         `json:"finish_reason"`
}

type chatChunkDelta struct {
	Role      string              `json:"role,omitempty"`
	Content   string              `json:"content,omitempty"`
	ToolCalls []chatChunkToolCall `json:"tool_calls,omitempty"`
}

type chatChunkToolCall struct {
	Index    int                 `json:"index"`
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function chatChunkToolCallFn `json:"function"`
}

type chatChunkToolCallFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// runChatEncodeStream drives ChatCodec.EncodeResponse in stream mode and
// returns the raw SSE bytes.
func runChatEncodeStream(t *testing.T, events []codec.Event) []byte {
	t.Helper()
	ch := make(chan codec.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}
	c := &ChatCodec{}
	if err := c.EncodeResponse(ch, w, true); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	return rec.Body.Bytes()
}

// parseChatSSE parses raw SSE bytes into chatChunk slices (skipping [DONE] and
// chunks without choices).
func parseChatSSE(raw []byte) []chatChunk {
	var out []chatChunk
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var c chatChunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ---------------------------------------------------------------------------
// O3: EncodeRequest must build data: URI from MediaB64 + MimeType
// ---------------------------------------------------------------------------

func TestChatEncodeRequest_Base64Image(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-4o",
		Messages: []codec.Message{
			{
				Role: codec.RoleUser,
				Content: []codec.ContentBlock{
					{Type: codec.ContentTypeText, Text: "What is this?"},
					{Type: codec.ContentTypeImage, MediaB64: "abc123", MimeType: "image/png"},
				},
			},
		},
	}

	cfg := &codec.ChannelConfig{
		BaseURL: "https://api.openai.com",
		APIKey:  "sk-test",
		Model:   "gpt-4o",
	}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	msgs := raw["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)

	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}

	imgBlock := content[1].(map[string]any)
	if imgBlock["type"] != "image_url" {
		t.Errorf("type = %v, want image_url", imgBlock["type"])
	}
	imgURL, ok := imgBlock["image_url"].(map[string]any)
	if !ok {
		t.Fatal("missing image_url object")
	}
	wantURL := "data:image/png;base64,abc123"
	if imgURL["url"] != wantURL {
		t.Errorf("image_url.url = %q, want %q", imgURL["url"], wantURL)
	}
}

// TestChatEncodeRequest_Base64Image_MediaURLFallback verifies that when only MediaURL
// is set (no base64), EncodeRequest uses the URL directly.
func TestChatEncodeRequest_Base64Image_MediaURLFallback(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-4o",
		Messages: []codec.Message{
			{
				Role: codec.RoleUser,
				Content: []codec.ContentBlock{
					{Type: codec.ContentTypeImage, MediaURL: "https://example.com/img.png"},
				},
			},
		},
	}

	cfg := &codec.ChannelConfig{
		BaseURL: "https://api.openai.com",
		APIKey:  "sk-test",
		Model:   "gpt-4o",
	}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)

	msgs := raw["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)
	imgBlock := content[0].(map[string]any)
	imgURL := imgBlock["image_url"].(map[string]any)

	if imgURL["url"] != "https://example.com/img.png" {
		t.Errorf("image_url.url = %q, want https://example.com/img.png", imgURL["url"])
	}
}

// ---------------------------------------------------------------------------
// O2: Stream encode tool_calls must have index field
// ---------------------------------------------------------------------------

// TestChatEncodeStream_ToolCallIndex verifies that two sequential tool calls
// receive distinct, monotonically-increasing index values (0 and 1) when
// injected via the new EventToolCallStart/ArgumentsDelta/End triple.
func TestChatEncodeStream_ToolCallIndex(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventStreamStart},
		// First tool call
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "call_1", Index: 0, Name: "search"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_1", Arguments: `{"q":"test"}`}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "call_1"}},
		// Second tool call
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "call_2", Index: 1, Name: "lookup"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_2", Arguments: `{"id":1}`}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "call_2"}},
		{Type: codec.EventDone},
	}

	raw := runChatEncodeStream(t, events)
	lines := strings.Split(string(raw), "\n")

	var toolCallChunks []oaiResponse
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var chunk oaiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil && len(chunk.Choices[0].Delta.ToolCalls) > 0 {
			toolCallChunks = append(toolCallChunks, chunk)
		}
	}

	if len(toolCallChunks) < 2 {
		t.Fatalf("tool call chunks = %d, want >= 2", len(toolCallChunks))
	}

	// First Start chunk should have index 0
	tc0 := toolCallChunks[0].Choices[0].Delta.ToolCalls[0]
	if tc0.Index != 0 {
		t.Errorf("first tool call index = %d, want 0", tc0.Index)
	}

	// Second Start chunk should have index 1
	// Find it: the first chunk with call_2 ID
	var tc1Found bool
	for _, chunk := range toolCallChunks {
		tc := chunk.Choices[0].Delta.ToolCalls[0]
		if tc.ID == "call_2" {
			if tc.Index != 1 {
				t.Errorf("second tool call index = %d, want 1", tc.Index)
			}
			tc1Found = true
			break
		}
	}
	if !tc1Found {
		t.Errorf("no chunk with call_2 ID found")
	}

	// Verify the index field actually appears in the raw JSON
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		if strings.Contains(data, "tool_calls") {
			if !strings.Contains(data, `"index"`) {
				t.Errorf("tool_calls chunk missing 'index' field in raw JSON: %s", data)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// O5: Stream encode must propagate finish_reason, not hardcode "stop"
// ---------------------------------------------------------------------------

func TestChatEncodeStream_FinishReason(t *testing.T) {
	tests := []struct {
		name         string
		finishReason string
	}{
		{"tool_calls", "tool_calls"},
		{"length", "length"},
		{"stop", "stop"},
		{"content_filter", "content_filter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan codec.Event, 10)
			ch <- codec.Event{Type: codec.EventStreamStart}
			ch <- codec.Event{
				Type: codec.EventContentDelta,
				Delta: &codec.DeltaPayload{
					ContentType: codec.ContentTypeText,
					Text:        "partial",
				},
			}
			ch <- codec.Event{FinishReason: tt.finishReason}
			ch <- codec.Event{Type: codec.EventDone}
			close(ch)

			rec := httptest.NewRecorder()
			w := &flushRecorder{ResponseRecorder: rec}

			c := &ChatCodec{}
			err := c.EncodeResponse(ch, w, true)
			if err != nil {
				t.Fatalf("EncodeResponse stream: %v", err)
			}

			output := rec.Body.String()
			lines := strings.Split(output, "\n")

			// Find the Done chunk (the one with a non-nil finish_reason)
			foundFR := false
			for _, line := range lines {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					continue
				}
				var chunk oaiResponse
				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					continue
				}
				if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
					if *chunk.Choices[0].FinishReason == tt.finishReason {
						foundFR = true
					} else {
						t.Errorf("finish_reason = %q, want %q", *chunk.Choices[0].FinishReason, tt.finishReason)
					}
				}
			}
			if !foundFR {
				t.Errorf("no chunk with finish_reason=%q found in output:\n%s", tt.finishReason, output)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Fix 1: Stream tool call continuation deltas reuse the same index
// ---------------------------------------------------------------------------

// TestChatEncodeStream_ToolCallContinuation verifies that argument delta chunks
// for a single tool call all carry the same index (0), not an ever-increasing
// counter. Uses the new EventToolCallStart/ArgumentsDelta/End triple.
func TestChatEncodeStream_ToolCallContinuation(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventStreamStart},
		// Start — establishes call_1 at index 0
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "call_1", Index: 0, Name: "search"}},
		// Three argument delta chunks for the same call
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_1", Arguments: `{"q":`}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_1", Arguments: `"test"`}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_1", Arguments: `}`}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "call_1"}},
		{Type: codec.EventDone},
	}

	raw := runChatEncodeStream(t, events)
	chunks := parseChatSSE(raw)

	var toolCallChunks []chatChunk
	for _, c := range chunks {
		if len(c.Choices) > 0 && len(c.Choices[0].Delta.ToolCalls) > 0 {
			toolCallChunks = append(toolCallChunks, c)
		}
	}

	// Expect: 1 Start chunk + 3 ArgsDelta chunks = 4 total (EventToolCallEnd is no-op)
	if len(toolCallChunks) != 4 {
		t.Fatalf("tool call chunks = %d, want 4", len(toolCallChunks))
	}

	// All four chunks should have index 0 (same tool call)
	for i, chunk := range toolCallChunks {
		tc := chunk.Choices[0].Delta.ToolCalls[0]
		if tc.Index != 0 {
			t.Errorf("chunk %d: tool call index = %d, want 0", i, tc.Index)
		}
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse non-stream text
// ---------------------------------------------------------------------------

func TestChatEncodeNonStream_Text(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart, Model: "gpt-4o", Created: 1700000000}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "Hello world",
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp oaiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", resp.Object)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("content = %v, want 'Hello world'", resp.Choices[0].Message)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %v, want stop", resp.Choices[0].FinishReason)
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse non-stream tool calls
// ---------------------------------------------------------------------------

func TestChatEncodeNonStream_ToolCalls(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{
		Type: codec.EventToolCallDelta,
		Delta: &codec.DeltaPayload{
			ToolCall: &codec.ToolCallDelta{
				ID:        "call_abc",
				Name:      "get_weather",
				Arguments: `{"city":"Tokyo"}`,
			},
		},
	}
	ch <- codec.Event{FinishReason: "tool_calls"}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp oaiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.Message == nil {
		t.Fatal("message is nil")
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("tc.id = %q, want call_abc", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("tc.function.name = %q, want get_weather", tc.Function.Name)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choice.FinishReason)
	}
}

// ---------------------------------------------------------------------------
// BuiltinToolFallback policy tests
// ---------------------------------------------------------------------------

func TestChatEncodeBuiltinToolFallback_Drop(t *testing.T) {
	req := &codec.Request{
		Model:           "gpt-5",
		Messages:        []codec.Message{{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}}},
		InboundProtocol: codec.ProtocolOpenAIResponses,
		Tools: []codec.Tool{
			{Type: "function", Name: "get_weather", InputSchema: map[string]any{"type": "object"}},
			{Type: "web_search", RawConfig: map[string]any{"type": "web_search", "external_web_access": false}},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5", BuiltinToolFallback: "drop"}
	c := &ChatCodec{}

	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	body, _ := io.ReadAll(httpReq.Body)
	var decoded struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Tools) != 1 {
		t.Fatalf("want 1 tool (built-in dropped), got %d: %s", len(decoded.Tools), body)
	}
	if fn, ok := decoded.Tools[0]["function"].(map[string]any); !ok || fn["name"] != "get_weather" {
		t.Errorf("unexpected tool: %v", decoded.Tools[0])
	}

	dropped, ok := req.Metadata["dropped_tools"].([]codec.DroppedTool)
	if !ok || len(dropped) != 1 || dropped[0].Type != "web_search" {
		t.Errorf("want one dropped web_search tool in metadata, got %v", req.Metadata)
	}
}

func TestChatEncodeBuiltinToolFallback_Error(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-5", Messages: []codec.Message{{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "x"}}}},
		InboundProtocol: codec.ProtocolOpenAIResponses,
		Tools:           []codec.Tool{{Type: "web_search", RawConfig: map[string]any{"type": "web_search"}}},
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5", BuiltinToolFallback: "error"}
	c := &ChatCodec{}

	_, err := c.EncodeRequest(req, cfg)
	if !errors.Is(err, codec.ErrBuiltinToolUnsupported) {
		t.Errorf("want ErrBuiltinToolUnsupported, got %v", err)
	}
}

func TestChatEncodeBuiltinToolFallback_Passthrough(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-5", Messages: []codec.Message{{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "x"}}}},
		InboundProtocol: codec.ProtocolOpenAIResponses,
		Tools:           []codec.Tool{{Type: "web_search", RawConfig: map[string]any{"type": "web_search", "external_web_access": true}}},
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5", BuiltinToolFallback: "passthrough"}
	c := &ChatCodec{}

	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	if !bytes.Contains(body, []byte(`"type":"web_search"`)) {
		t.Errorf("expected passthrough RawConfig in body, got: %s", body)
	}
}

func TestChatEncodeRejectsEmptyNameFunctionTool(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-5", Messages: []codec.Message{{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "x"}}}},
		InboundProtocol: codec.ProtocolOpenAIChat,
		Tools:           []codec.Tool{{Type: "function", Name: ""}},
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	c := &ChatCodec{}

	_, err := c.EncodeRequest(req, cfg)
	if !errors.Is(err, codec.ErrFunctionToolMissingName) {
		t.Errorf("want ErrFunctionToolMissingName, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 7: chat stream encoder handles Start/ArgsDelta/End events
// ---------------------------------------------------------------------------

func TestChatStreamEncode_ToolCallCorrectShape(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventStreamStart},
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Index: 0, Name: "exec"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Arguments: `{"a"`}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Arguments: `:1}`}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Arguments: `{"a":1}`}},
		{Type: codec.EventDone},
	}
	raw := runChatEncodeStream(t, events)
	chunks := parseChatSSE(raw)

	var firstWithID, firstArgs, secondArgs *chatChunk
	var finalReason string
	for i, c := range chunks {
		if len(c.Choices) == 0 {
			continue
		}
		if c.Choices[0].FinishReason != "" {
			finalReason = c.Choices[0].FinishReason
		}
		if len(c.Choices[0].Delta.ToolCalls) > 0 {
			tc := c.Choices[0].Delta.ToolCalls[0]
			if tc.ID != "" && firstWithID == nil {
				cp := chunks[i]
				firstWithID = &cp
				require.Equal(t, "call_x", tc.ID)
				require.Equal(t, "exec", tc.Function.Name)
				require.Equal(t, 0, tc.Index)
			} else if firstArgs == nil {
				cp := chunks[i]
				firstArgs = &cp
				require.Equal(t, `{"a"`, tc.Function.Arguments)
			} else if secondArgs == nil {
				cp := chunks[i]
				secondArgs = &cp
				require.Equal(t, `:1}`, tc.Function.Arguments)
			}
		}
	}
	require.NotNil(t, firstWithID, "expected a chunk with tool call id+name")
	require.NotNil(t, firstArgs, "expected a chunk with first args fragment")
	require.NotNil(t, secondArgs, "expected a chunk with second args fragment")
	require.Equal(t, "tool_calls", finalReason)
}

func TestChatEncode_AssistantWithThinkingEmitsReasoningContent(t *testing.T) {
	req := &codec.Request{
		Model: "deepseek-v4",
		Messages: []codec.Message{
			{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
			{
				Role: codec.RoleAssistant,
				Content: []codec.ContentBlock{
					{Type: codec.ContentTypeThinking, Text: "let me think..."},
					{Type: codec.ContentTypeText, Text: "answer"},
				},
			},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "https://x", APIKey: "k", Model: "deepseek-v4"}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msgs := raw["messages"].([]any)
	asst := msgs[1].(map[string]any)
	if got, _ := asst["reasoning_content"].(string); got != "let me think..." {
		t.Fatalf("reasoning_content = %q, want %q", got, "let me think...")
	}
	// content 字段不应包含 thinking 文本
	if content, _ := asst["content"].(string); content != "answer" {
		t.Fatalf("content = %q, want %q", content, "answer")
	}
}

func TestChatEncode_AssistantWithEmptyThinkingEmitsEmptyString(t *testing.T) {
	req := &codec.Request{
		Model: "deepseek-v4",
		Messages: []codec.Message{
			{
				Role: codec.RoleAssistant,
				Content: []codec.ContentBlock{
					{Type: codec.ContentTypeThinking, Text: ""}, // 占位符
				},
				ToolCalls: []codec.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}},
			},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "https://x", APIKey: "k", Model: "deepseek-v4"}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msgs := raw["messages"].([]any)
	asst := msgs[0].(map[string]any)
	rc, present := asst["reasoning_content"]
	if !present {
		t.Fatal("reasoning_content field missing; placeholder block must emit empty-string field")
	}
	if rc.(string) != "" {
		t.Fatalf("reasoning_content = %q, want empty string", rc)
	}
}

func TestChatEncode_NoThinkingNoReasoningContentField(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-4o",
		Messages: []codec.Message{
			{Role: codec.RoleAssistant, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hello"}}},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "https://x", APIKey: "k", Model: "gpt-4o"}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msgs := raw["messages"].([]any)
	asst := msgs[0].(map[string]any)
	if _, present := asst["reasoning_content"]; present {
		t.Fatalf("reasoning_content field should be absent when no thinking block; got %v", asst["reasoning_content"])
	}
}

// TestChatEncodeRequest_DropsEmptyTextBlock 复现 SGLang 400:assistant 历史消息含
// 「空块 + 真块」,出站不得产生非法的 {"type":"text"} 块;过滤后只剩真块 → 字符串简写。
func TestChatEncodeRequest_DropsEmptyTextBlock(t *testing.T) {
	req := &codec.Request{
		Model: "qwen",
		Messages: []codec.Message{
			{
				Role: codec.RoleAssistant,
				Content: []codec.ContentBlock{
					{Type: codec.ContentTypeText, Text: ""},
					{Type: codec.ContentTypeText, Text: "Hello! I see you're working on..."},
				},
			},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "https://x", APIKey: "k", Model: "qwen"}

	httpReq, err := (&ChatCodec{}).EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)

	// 非法空块的确切 marshal 形态(Go 无空格):{"type":"text"} 不得出现。
	// 真块若走数组会是 {"type":"text","text":"..."},不含子串 {"type":"text"}。
	if strings.Contains(string(bodyBytes), `{"type":"text"}`) {
		t.Errorf("body contains illegal empty text block: %s", bodyBytes)
	}
	if !strings.Contains(string(bodyBytes), "Hello! I see you're working on...") {
		t.Errorf("real text missing from body: %s", bodyBytes)
	}

	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	msg := raw["messages"].([]any)[0].(map[string]any)
	if msg["content"] != "Hello! I see you're working on..." {
		t.Errorf("content = %#v, want string shorthand of the real text", msg["content"])
	}
}

// TestChatEncodeRequest_AllEmptyTextBecomesEmptyString:整条消息只有空文本块 → 合法 content:""。
func TestChatEncodeRequest_AllEmptyTextBecomesEmptyString(t *testing.T) {
	req := &codec.Request{
		Model: "qwen",
		Messages: []codec.Message{
			{Role: codec.RoleAssistant, Content: []codec.ContentBlock{
				{Type: codec.ContentTypeText, Text: ""},
				{Type: codec.ContentTypeText, Text: ""},
			}},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "https://x", APIKey: "k", Model: "qwen"}
	httpReq, err := (&ChatCodec{}).EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	msg := raw["messages"].([]any)[0].(map[string]any)
	if msg["content"] != "" {
		t.Errorf("content = %#v, want empty string", msg["content"])
	}
}

// TestChatEncodeRequest_TwoRealTextBlocksUnchanged:两个真文本块无回归,仍是数组两块。
func TestChatEncodeRequest_TwoRealTextBlocksUnchanged(t *testing.T) {
	req := &codec.Request{
		Model: "qwen",
		Messages: []codec.Message{
			{Role: codec.RoleUser, Content: []codec.ContentBlock{
				{Type: codec.ContentTypeText, Text: "first"},
				{Type: codec.ContentTypeText, Text: "second"},
			}},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "https://x", APIKey: "k", Model: "qwen"}
	httpReq, err := (&ChatCodec{}).EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	content := raw["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}
}

// TestChatEncodeRequest_EmptyTextWithImageKeepsImageOnly(boundary):空块 + 图片 → 只剩图片。
func TestChatEncodeRequest_EmptyTextWithImageKeepsImageOnly(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-4o",
		Messages: []codec.Message{
			{Role: codec.RoleUser, Content: []codec.ContentBlock{
				{Type: codec.ContentTypeText, Text: ""},
				{Type: codec.ContentTypeImage, MediaB64: "abc", MimeType: "image/png"},
			}},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "https://x", APIKey: "k", Model: "gpt-4o"}
	httpReq, err := (&ChatCodec{}).EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	content := raw["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1 (image only)", len(content))
	}
	if content[0].(map[string]any)["type"] != "image_url" {
		t.Errorf("remaining block type = %v, want image_url", content[0].(map[string]any)["type"])
	}
}
