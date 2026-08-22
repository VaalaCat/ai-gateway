package claude

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// ---------------------------------------------------------------------------
// Test helpers for claude stream encode tests
// ---------------------------------------------------------------------------

// claudeFlushRecorder wraps httptest.ResponseRecorder to implement http.Flusher.
type claudeFlushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *claudeFlushRecorder) Flush() {
	f.ResponseRecorder.Flush()
}

// claudeSSEEvent holds a parsed SSE event name + data line.
type claudeSSEEvent struct {
	Event string
	Data  string
}

// parseClaudeSSE splits raw SSE bytes into a slice of claudeSSEEvent.
func parseClaudeSSE(raw []byte) []claudeSSEEvent {
	var out []claudeSSEEvent
	var cur claudeSSEEvent
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "event: ") {
			cur.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			cur.Data = strings.TrimPrefix(line, "data: ")
		} else if line == "" && cur.Event != "" {
			out = append(out, cur)
			cur = claudeSSEEvent{}
		}
	}
	return out
}

// runClaudeEncodeStream drives handler.EncodeResponse in stream mode and
// returns the raw SSE bytes.
func runClaudeEncodeStream(t *testing.T, events []ir.Event) []byte {
	t.Helper()
	ch := make(chan ir.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)

	rec := httptest.NewRecorder()
	w := &claudeFlushRecorder{ResponseRecorder: rec}
	c := &handler{}
	if err := c.encodeHTTPResponse(ch, w, true); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	return rec.Body.Bytes()
}

// ---------------------------------------------------------------------------
// C2: tool_choice dropped on encode
// ---------------------------------------------------------------------------

func TestEncodeRequest_ToolChoice(t *testing.T) {
	c := &handler{}
	req := &ir.Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages:  []ir.Message{ir.TextMessage(ir.RoleUser, "hello")},
		Tools: []ir.Tool{
			{Name: "search", Description: "Search", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: &ir.ToolChoice{Type: "auto"},
	}
	cfg := &channelConfig{
		BaseURL: "https://api.anthropic.com",
		APIKey:  "sk-test",
		Model:   "claude-sonnet-4-20250514",
	}

	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest failed: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)

	tc, ok := raw["tool_choice"]
	if !ok || tc == nil {
		t.Fatal("tool_choice should be present in encoded request")
	}
	tcMap, ok := tc.(map[string]any)
	if !ok {
		t.Fatalf("tool_choice should be an object, got %T", tc)
	}
	if tcMap["type"] != "auto" {
		t.Errorf("tool_choice.type = %v, want 'auto'", tcMap["type"])
	}
}

func TestEncodeRequest_ToolChoiceFunction(t *testing.T) {
	c := &handler{}
	req := &ir.Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages:  []ir.Message{ir.TextMessage(ir.RoleUser, "hello")},
		Tools: []ir.Tool{
			{Name: "search", Description: "Search", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: &ir.ToolChoice{Type: "function", Name: "search"},
	}
	cfg := &channelConfig{
		BaseURL: "https://api.anthropic.com",
		APIKey:  "sk-test",
		Model:   "claude-sonnet-4-20250514",
	}

	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest failed: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)

	tc, ok := raw["tool_choice"]
	if !ok || tc == nil {
		t.Fatal("tool_choice should be present in encoded request")
	}
	tcMap := tc.(map[string]any)
	// Claude uses "tool" type with a "name" field for specific tool selection
	if tcMap["name"] != "search" {
		t.Errorf("tool_choice.name = %v, want 'search'", tcMap["name"])
	}
}

// ---------------------------------------------------------------------------
// C5: thinking config dropped on encode
// ---------------------------------------------------------------------------

func TestEncodeRequest_ThinkingConfig(t *testing.T) {
	c := &handler{}
	req := &ir.Request{
		Model:           "claude-sonnet-4-20250514",
		MaxTokens:       16000,
		Messages:        []ir.Message{ir.TextMessage(ir.RoleUser, "think about this")},
		ThinkingEnabled: true,
		ThinkingBudget:  10000,
	}
	cfg := &channelConfig{
		BaseURL: "https://api.anthropic.com",
		APIKey:  "sk-test",
		Model:   "claude-sonnet-4-20250514",
	}

	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest failed: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)

	thinking, ok := raw["thinking"]
	if !ok || thinking == nil {
		t.Fatal("thinking config should be present in encoded request")
	}
	thinkMap, ok := thinking.(map[string]any)
	if !ok {
		t.Fatalf("thinking should be an object, got %T", thinking)
	}
	if thinkMap["type"] != "enabled" {
		t.Errorf("thinking.type = %v, want 'enabled'", thinkMap["type"])
	}
	budget, ok := thinkMap["budget_tokens"].(float64)
	if !ok || int(budget) != 10000 {
		t.Errorf("thinking.budget_tokens = %v, want 10000", thinkMap["budget_tokens"])
	}
}

// ---------------------------------------------------------------------------
// C6: non-stream encode ignores thinking events
// ---------------------------------------------------------------------------

func TestEncodeNonStream_Thinking(t *testing.T) {
	events := make(chan ir.Event, 10)
	events <- ir.Event{Type: ir.EventStreamStart}
	events <- ir.Event{
		Type: ir.EventThinkingDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeThinking,
			Text:        "Let me think about this...",
		},
	}
	events <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "The answer is 42.",
		},
	}
	events <- ir.Event{
		Type: ir.EventUsage,
		Usage: &ir.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
	events <- ir.Event{Type: ir.EventDone, FinishReason: "stop"}
	close(events)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(events, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse non-stream failed: %v", err)
	}

	var resp claudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	// Should have a thinking content block in the response
	var hasThinking bool
	for _, block := range resp.Content {
		if block.Type == "thinking" {
			hasThinking = true
			if block.Thinking != "Let me think about this..." {
				t.Errorf("thinking text = %q, want 'Let me think about this...'", block.Thinking)
			}
		}
	}
	if !hasThinking {
		t.Error("response should include a thinking content block")
	}

	// Should also have the text block
	var hasText bool
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text == "The answer is 42." {
			hasText = true
		}
	}
	if !hasText {
		t.Error("response should include the text content block")
	}
}

// ---------------------------------------------------------------------------
// C3 + C4: stream encode creates new content_block_start per delta,
//          missing content_block_stop
// ---------------------------------------------------------------------------

func TestEncodeStream_BlockLifecycle(t *testing.T) {
	events := make(chan ir.Event, 10)
	events <- ir.Event{Type: ir.EventStreamStart}
	events <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "Hello",
		},
	}
	events <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        " world",
		},
	}
	events <- ir.Event{
		Type: ir.EventUsage,
		Usage: &ir.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	events <- ir.Event{Type: ir.EventDone, FinishReason: "stop"}
	close(events)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(events, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream failed: %v", err)
	}

	body := w.Body.String()

	// C3: Two text deltas for the same logical block should produce
	// exactly ONE content_block_start, not one per delta.
	// Count only "event:" lines to avoid double-counting the "type" field in data payloads.
	startCount := strings.Count(body, "event: content_block_start")
	if startCount != 1 {
		t.Errorf("content_block_start count = %d, want 1 (one per logical block, not per delta)", startCount)
	}

	// Should have 2 content_block_delta events
	deltaCount := strings.Count(body, "event: content_block_delta")
	if deltaCount != 2 {
		t.Errorf("content_block_delta count = %d, want 2", deltaCount)
	}

	// C4: Should have a content_block_stop event before message_delta
	if !strings.Contains(body, "event: content_block_stop") {
		t.Error("stream output should include content_block_stop event")
	}
	stopCount := strings.Count(body, "event: content_block_stop")
	if stopCount != 1 {
		t.Errorf("content_block_stop count = %d, want 1", stopCount)
	}
}

// ---------------------------------------------------------------------------
// C7: stream message_start has empty model
// ---------------------------------------------------------------------------

func TestEncodeStream_ModelInMessageStart(t *testing.T) {
	events := make(chan ir.Event, 10)
	events <- ir.Event{
		Type:  ir.EventStreamStart,
		Model: "claude-sonnet-4-20250514",
	}
	events <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "Hi",
		},
	}
	events <- ir.Event{Type: ir.EventDone, FinishReason: "stop"}
	close(events)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(events, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream failed: %v", err)
	}

	body := w.Body.String()

	// The message_start event should include the model name, not an empty string
	// Find the message_start data line
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "message_start") {
			if strings.Contains(line, `"model":""`) {
				t.Error("message_start should include the model name, not empty string")
			}
			if !strings.Contains(line, "claude-sonnet-4-20250514") {
				t.Error("message_start should contain the model name 'claude-sonnet-4-20250514'")
			}
			break
		}
	}
}

// ---------------------------------------------------------------------------
// C8 + C14: stop_reason mapping incomplete (missing pause_turn, refusal)
// ---------------------------------------------------------------------------

func TestReverseMapStopReason(t *testing.T) {
	// Basic mappings that should work
	tests := []struct {
		input string
		want  string
	}{
		{consts.FinishReasonStop, consts.ClaudeStopEndTurn},
		{consts.FinishReasonLength, consts.ClaudeStopMaxTokens},
		{consts.FinishReasonToolCalls, consts.ClaudeStopToolUse},
	}

	for _, tt := range tests {
		got := reverseMapStopReason(tt.input)
		if got != tt.want {
			t.Errorf("reverseMapStopReason(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	// C8: content_filter should reverse-map to a valid Claude stop reason
	// Currently it falls through to "content_filter" which is not a valid Claude stop_reason
	got := reverseMapStopReason(consts.FinishReasonContentFilter)
	validClaudeReasons := map[string]bool{
		consts.ClaudeStopEndTurn:   true,
		consts.ClaudeStopMaxTokens: true,
		consts.ClaudeStopToolUse:   true,
		"pause_turn":               true,
		"refusal":                  true,
	}
	if !validClaudeReasons[got] {
		t.Errorf("reverseMapStopReason(%q) = %q, which is not a valid Claude stop_reason", consts.FinishReasonContentFilter, got)
	}

	// C14: Claude's pause_turn and refusal stop_reasons should be preserved as-is
	// in the IR, allowing Claude-native clients to see them unchanged.
	// OpenAI encoders handle mapping them to "stop" on the outbound side.
	pauseResult := mapStopReason("pause_turn")
	if pauseResult != "pause_turn" {
		t.Errorf("mapStopReason('pause_turn') = %q, want 'pause_turn' (preserved as-is)", pauseResult)
	}

	refusalResult := mapStopReason("refusal")
	if refusalResult != "refusal" {
		t.Errorf("mapStopReason('refusal') = %q, want 'refusal' (preserved as-is)", refusalResult)
	}

	// stop_sequence should map to "stop"
	stopSeqResult := mapStopReason("stop_sequence")
	if stopSeqResult != consts.FinishReasonStop {
		t.Errorf("mapStopReason('stop_sequence') = %q, want %q", stopSeqResult, consts.FinishReasonStop)
	}
}

// ---------------------------------------------------------------------------
// C10: stream usage missing input_tokens
// ---------------------------------------------------------------------------

func TestEncodeStream_UsageIncludesInputTokens(t *testing.T) {
	events := make(chan ir.Event, 10)
	events <- ir.Event{Type: ir.EventStreamStart}
	events <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "Hi",
		},
	}
	events <- ir.Event{
		Type: ir.EventUsage,
		Usage: &ir.Usage{
			PromptTokens:     42,
			CompletionTokens: 10,
			TotalTokens:      52,
		},
	}
	events <- ir.Event{Type: ir.EventDone, FinishReason: "stop"}
	close(events)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(events, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream failed: %v", err)
	}

	body := w.Body.String()

	// Find the message_delta event data which contains usage
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "message_delta") {
			// The usage in message_delta should include input_tokens, not just output_tokens
			if !strings.Contains(line, "input_tokens") {
				t.Error("message_delta usage should include input_tokens")
			}
			if strings.Contains(line, `"input_tokens"`) {
				// Parse and verify the actual value
				data := strings.TrimPrefix(line, "data:")
				data = strings.TrimLeft(data, " ")
				var md struct {
					Usage map[string]any `json:"usage"`
				}
				if err := json.Unmarshal([]byte(data), &md); err == nil && md.Usage != nil {
					if v, ok := md.Usage["input_tokens"].(float64); ok {
						if int(v) != 42 {
							t.Errorf("input_tokens = %v, want 42", v)
						}
					}
				}
			}
			break
		}
	}
}

// ---------------------------------------------------------------------------
// Bug 1: SSE data payloads missing "type" field
// ---------------------------------------------------------------------------

func TestEncodeStream_TypeFieldsPresent(t *testing.T) {
	events := make(chan ir.Event, 10)
	events <- ir.Event{
		Type:  ir.EventStreamStart,
		Model: "claude-sonnet-4-20250514",
	}
	events <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "Hello",
		},
	}
	events <- ir.Event{Type: ir.EventDone, FinishReason: "stop"}
	close(events)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(events, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream failed: %v", err)
	}

	body := w.Body.String()

	// Every "data:" line should contain a "type" field in the JSON payload.
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("failed to parse data line: %v\nline: %s", err, data)
		}
		if _, ok := payload["type"]; !ok {
			t.Errorf("data payload missing 'type' field: %s", data)
		}
	}

	// Verify specific type values are correct
	expectedTypes := map[string]bool{
		sseconsts.MessageStart:      false,
		sseconsts.ContentBlockStart: false,
		sseconsts.ContentBlockDelta: false,
		sseconsts.ContentBlockStop:  false,
		sseconsts.MessageDelta:      false,
		sseconsts.MessageStop:       false,
	}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}
		if tp, ok := payload["type"].(string); ok {
			if _, exists := expectedTypes[tp]; exists {
				expectedTypes[tp] = true
			}
		}
	}
	for tp, found := range expectedTypes {
		if !found {
			t.Errorf("expected SSE event type %q not found in stream output", tp)
		}
	}
}

// ---------------------------------------------------------------------------
// Bug 2: message_start missing required fields (content, stop_reason, stop_sequence)
// ---------------------------------------------------------------------------

func TestEncodeStream_MessageStartRequiredFields(t *testing.T) {
	events := make(chan ir.Event, 10)
	events <- ir.Event{
		Type:  ir.EventStreamStart,
		Model: "claude-sonnet-4-20250514",
	}
	events <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "Hi",
		},
	}
	events <- ir.Event{Type: ir.EventDone, FinishReason: "stop"}
	close(events)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(events, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream failed: %v", err)
	}

	body := w.Body.String()

	// Find the message_start data line and parse the message object
	found := false
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}
		if payload["type"] != sseconsts.MessageStart {
			continue
		}
		found = true
		msg, ok := payload["message"].(map[string]any)
		if !ok {
			t.Fatal("message_start payload missing 'message' object")
		}

		// content must be present (empty array)
		content, ok := msg["content"]
		if !ok {
			t.Error("message_start message missing 'content' field")
		} else {
			arr, ok := content.([]any)
			if !ok {
				t.Errorf("message_start message.content should be an array, got %T", content)
			} else if len(arr) != 0 {
				t.Errorf("message_start message.content should be empty, got %v", arr)
			}
		}

		// stop_reason must be present (null)
		if _, ok := msg["stop_reason"]; !ok {
			t.Error("message_start message missing 'stop_reason' field")
		} else if msg["stop_reason"] != nil {
			t.Errorf("message_start message.stop_reason should be null, got %v", msg["stop_reason"])
		}

		// stop_sequence must be present (null)
		if _, ok := msg["stop_sequence"]; !ok {
			t.Error("message_start message missing 'stop_sequence' field")
		} else if msg["stop_sequence"] != nil {
			t.Errorf("message_start message.stop_sequence should be null, got %v", msg["stop_sequence"])
		}
		break
	}
	if !found {
		t.Fatal("message_start event not found in stream output")
	}
}

// ---------------------------------------------------------------------------
// Task 10: ResolveTool integration
// ---------------------------------------------------------------------------

func TestClaudeEncodeBuiltinDropDefault(t *testing.T) {
	req := &ir.Request{
		Model:           "claude-sonnet-4-20250514",
		Messages:        []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "hi"}}}},
		MaxTokens:       64,
		InboundProtocol: ir.ProtocolOpenAIResponses,
		Tools: []ir.Tool{
			{Type: "function", Name: "f", InputSchema: map[string]any{"type": "object"}},
			{Type: "web_search", RawConfig: map[string]any{"type": "web_search"}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "claude-sonnet-4-20250514"}
	c := &handler{}
	httpReq, err := c.encodeHTTPRequest(req, cfg)
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
		t.Fatalf("want 1 tool (web_search dropped), got %d: %s", len(decoded.Tools), body)
	}
	if decoded.Tools[0]["name"] != "f" {
		t.Errorf("unexpected tool: %v", decoded.Tools[0])
	}
	dropped, _ := req.Metadata["dropped_tools"].([]convert.DroppedTool)
	if len(dropped) != 1 || dropped[0].Type != "web_search" {
		t.Errorf("want dropped web_search, got %v", dropped)
	}
}

func TestClaudeEncodeRejectsEmptyNameFunctionTool(t *testing.T) {
	req := &ir.Request{
		Model:           "claude-sonnet-4-20250514",
		Messages:        []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "x"}}}},
		MaxTokens:       64,
		InboundProtocol: ir.ProtocolClaude,
		Tools:           []ir.Tool{{Type: "function", Name: ""}},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "claude-sonnet-4-20250514"}
	c := &handler{}
	_, err := c.encodeHTTPRequest(req, cfg)
	if !errors.Is(err, convert.ErrFunctionToolMissingName) {
		t.Errorf("want ErrFunctionToolMissingName, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 6: claude stream encoder handles Start/ArgsDelta/End IR events
// ---------------------------------------------------------------------------

func TestClaudeStreamEncode_ToolCallCorrectShape(t *testing.T) {
	events := []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "toolu_x", Name: "exec"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "toolu_x", Arguments: `{"a"`}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "toolu_x", Arguments: `:1}`}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "toolu_x", Arguments: `{"a":1}`}},
		{Type: ir.EventDone},
	}
	raw := runClaudeEncodeStream(t, events)
	sse := parseClaudeSSE(raw)

	var blockStart, blockStop int
	var partialJSONs []string
	for _, e := range sse {
		switch e.Event {
		case "content_block_start":
			if strings.Contains(e.Data, `"type":"tool_use"`) {
				blockStart++
				if !strings.Contains(e.Data, `"id":"toolu_x"`) {
					t.Errorf("content_block_start missing id=toolu_x: %s", e.Data)
				}
				if !strings.Contains(e.Data, `"name":"exec"`) {
					t.Errorf("content_block_start missing name=exec: %s", e.Data)
				}
			}
		case "content_block_delta":
			if strings.Contains(e.Data, `"type":"input_json_delta"`) {
				var p struct {
					Delta struct {
						PartialJSON string `json:"partial_json"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
					t.Fatalf("unmarshal content_block_delta: %v", err)
				}
				partialJSONs = append(partialJSONs, p.Delta.PartialJSON)
			}
		case "content_block_stop":
			blockStop++
		}
	}
	if blockStart != 1 {
		t.Errorf("content_block_start (tool_use) count = %d, want 1", blockStart)
	}
	if blockStop != 1 {
		t.Errorf("content_block_stop count = %d, want 1", blockStop)
	}
	if len(partialJSONs) < 2 {
		t.Errorf("input_json_delta count = %d, want >= 2", len(partialJSONs))
	}
	got := strings.Join(partialJSONs, "")
	if got != `{"a":1}` {
		t.Errorf("concatenated partial_json = %q, want {\"a\":1}", got)
	}
}

func TestClaudeStreamEncode_DoneClosesOpenToolCall(t *testing.T) {
	events := []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "toolu_x", Name: "exec"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "toolu_x", Arguments: `{"a":1}`}},
		// Defensive boundary: an upstream decoder reaches Done without an End.
		{Type: ir.EventDone, FinishReason: consts.FinishReasonStop},
	}
	sse := parseClaudeSSE(runClaudeEncodeStream(t, events))

	toolStopIndex, messageDeltaIndex := -1, -1
	var stopReason string
	for index, event := range sse {
		switch event.Event {
		case sseconsts.ContentBlockStop:
			toolStopIndex = index
		case sseconsts.MessageDelta:
			messageDeltaIndex = index
			var payload struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
				t.Fatalf("unmarshal message_delta: %v", err)
			}
			stopReason = payload.Delta.StopReason
		}
	}

	if toolStopIndex < 0 || messageDeltaIndex < 0 || toolStopIndex > messageDeltaIndex {
		t.Fatalf("tool block must stop before message_delta: tool_stop=%d message_delta=%d", toolStopIndex, messageDeltaIndex)
	}
	if stopReason != consts.ClaudeStopToolUse {
		t.Fatalf("stop_reason = %q, want %q", stopReason, consts.ClaudeStopToolUse)
	}
}

func TestEncodeRequest_DropsEmptyTextBlock(t *testing.T) {
	c := &handler{}
	req := &ir.Request{
		Model: "claude-x",
		Messages: []ir.Message{
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: ""},
				{Type: ir.ContentTypeText, Text: "Hello! I see you're working on..."},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "claude-x"}
	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	if strings.Contains(string(bodyBytes), `{"type":"text"}`) {
		t.Errorf("body contains illegal empty text block: %s", bodyBytes)
	}
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg := raw["messages"].([]any)[0].(map[string]any)
	if msg["content"] != "Hello! I see you're working on..." {
		t.Errorf("content = %#v, want real text string shorthand", msg["content"])
	}
}

func TestEncodeRequest_SingleEmptyTextBecomesEmptyString(t *testing.T) {
	c := &handler{}
	req := &ir.Request{
		Model: "claude-x",
		Messages: []ir.Message{
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: ""},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "claude-x"}
	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)
	msg := raw["messages"].([]any)[0].(map[string]any)
	if msg["content"] != "" {
		t.Errorf("content = %#v, want empty string", msg["content"])
	}
}

func TestEncodeRequest_TwoRealTextBlocksUnchanged(t *testing.T) {
	c := &handler{}
	req := &ir.Request{
		Model: "claude-x",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: "first"},
				{Type: ir.ContentTypeText, Text: "second"},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "claude-x"}
	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)
	content := raw["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}
}

// 回归护栏:thinking-only 消息不得被兜底误改;现状 content:null,应保持。
func TestEncodeRequest_ThinkingOnlyMessageUnchanged(t *testing.T) {
	c := &handler{}
	req := &ir.Request{
		Model: "claude-x",
		Messages: []ir.Message{
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeThinking, Text: "let me think"},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "claude-x"}
	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)
	msg := raw["messages"].([]any)[0].(map[string]any)
	if v, present := msg["content"]; !present || v != nil {
		t.Errorf("thinking-only content should stay null, got present=%v val=%#v", present, v)
	}
}
