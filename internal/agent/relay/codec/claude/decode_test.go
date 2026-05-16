package claude

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

func makeHTTPRequest(body []byte) *http.Request {
	return &http.Request{Body: io.NopCloser(strings.NewReader(string(body)))}
}

// ---------------------------------------------------------------------------
// C1: system field only supports string, not array
// ---------------------------------------------------------------------------

func TestDecodeRequest_SystemArray(t *testing.T) {
	body := loadFixture(t, "claude/request_system_array.json")
	r := makeHTTPRequest(body)
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest should not error on system array: %v", err)
	}

	// The system array has two text blocks: "You are a helpful assistant." and
	// "Always respond in JSON format." Both should be decoded into a system message.
	if len(req.Messages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(req.Messages))
	}

	sysMsg := req.Messages[0]
	if sysMsg.Role != codec.RoleSystem {
		t.Fatalf("first message role = %q, want %q", sysMsg.Role, codec.RoleSystem)
	}

	// The system message should contain text from both array elements
	var sysText string
	for _, cb := range sysMsg.Content {
		if cb.Type == codec.ContentTypeText {
			sysText += cb.Text + " "
		}
	}
	sysText = strings.TrimSpace(sysText)

	if !strings.Contains(sysText, "helpful assistant") {
		t.Errorf("system text missing 'helpful assistant': got %q", sysText)
	}
	if !strings.Contains(sysText, "JSON format") {
		t.Errorf("system text missing 'JSON format': got %q", sysText)
	}
}

// ---------------------------------------------------------------------------
// C2: tool_choice dropped entirely on decode
// ---------------------------------------------------------------------------

func TestDecodeRequest_ToolChoice(t *testing.T) {
	body := loadFixture(t, "claude/request_tools.json")
	r := makeHTTPRequest(body)
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest failed: %v", err)
	}

	// The fixture has "tool_choice": {"type": "auto"}
	if req.ToolChoice == nil {
		t.Fatal("ToolChoice should not be nil; tool_choice was present in request")
	}
	if req.ToolChoice.Type != "auto" {
		t.Errorf("ToolChoice.Type = %q, want %q", req.ToolChoice.Type, "auto")
	}
}

// ---------------------------------------------------------------------------
// C5: thinking config dropped on decode
// ---------------------------------------------------------------------------

func TestDecodeRequest_ThinkingConfig(t *testing.T) {
	body := loadFixture(t, "claude/request_thinking.json")
	r := makeHTTPRequest(body)
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest failed: %v", err)
	}

	// The fixture has "thinking": {"type": "enabled", "budget_tokens": 10000}
	if !req.ThinkingEnabled {
		t.Error("ThinkingEnabled should be true")
	}
	if req.ThinkingBudget != 10000 {
		t.Errorf("ThinkingBudget = %d, want 10000", req.ThinkingBudget)
	}
}

// ---------------------------------------------------------------------------
// C11: thinking content blocks dropped in decode
// ---------------------------------------------------------------------------

func TestDecodeRequest_ThinkingContentBlocks(t *testing.T) {
	body := loadFixture(t, "claude/request_thinking.json")
	r := makeHTTPRequest(body)
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest failed: %v", err)
	}

	// The fixture has an assistant message with [thinking, text] content blocks.
	// Find the assistant message.
	var assistantMsg *codec.Message
	for i := range req.Messages {
		if req.Messages[i].Role == codec.RoleAssistant {
			assistantMsg = &req.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("no assistant message found")
	}

	// Should have a thinking content block
	var hasThinking bool
	for _, cb := range assistantMsg.Content {
		if cb.Type == codec.ContentTypeThinking {
			hasThinking = true
			if !strings.Contains(cb.Text, "calculate") {
				t.Errorf("thinking text = %q, expected to contain 'calculate'", cb.Text)
			}
		}
	}
	if !hasThinking {
		t.Error("assistant message should contain a thinking content block")
	}

	// Should also have a text content block
	var hasText bool
	for _, cb := range assistantMsg.Content {
		if cb.Type == codec.ContentTypeText {
			hasText = true
		}
	}
	if !hasText {
		t.Error("assistant message should also contain a text content block")
	}
}

// ---------------------------------------------------------------------------
// C9 + C13: tool_result+text not split correctly / multiple tool_results overwrite
// ---------------------------------------------------------------------------

func TestDecodeRequest_MultiToolResult(t *testing.T) {
	body := loadFixture(t, "claude/request_tool_result_multi.json")
	r := makeHTTPRequest(body)
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest failed: %v", err)
	}

	// The fixture has a user message with:
	//   [tool_result(call_1), tool_result(call_2), text("Please compare")]
	//
	// Bug C9: the text block alongside tool_results should be preserved.
	// Bug C13: multiple tool_results should each become their own tool-role message
	//          (or at least not overwrite each other's tool_call_id).

	// Count how many tool-role messages we got
	var toolMsgs []codec.Message
	for _, m := range req.Messages {
		if m.Role == codec.RoleTool {
			toolMsgs = append(toolMsgs, m)
		}
	}

	// We expect 2 separate tool messages (one per tool_result)
	if len(toolMsgs) != 2 {
		t.Errorf("expected 2 tool messages, got %d", len(toolMsgs))
	}

	// Verify both tool_call_ids are present
	toolCallIDs := map[string]bool{}
	for _, m := range toolMsgs {
		toolCallIDs[m.ToolCallID] = true
	}
	if !toolCallIDs["call_1"] {
		t.Error("missing tool message for call_1")
	}
	if !toolCallIDs["call_2"] {
		t.Error("missing tool message for call_2")
	}

	// Verify the text block ("Please compare these results") is preserved somewhere
	var foundCompareText bool
	for _, m := range req.Messages {
		for _, cb := range m.Content {
			if cb.Type == codec.ContentTypeText && strings.Contains(cb.Text, "compare") {
				foundCompareText = true
			}
		}
	}
	if !foundCompareText {
		t.Error("text block 'Please compare these results' should be preserved alongside tool_results")
	}
}

// ---------------------------------------------------------------------------
// Task 3: signature_delta support
// ---------------------------------------------------------------------------

func TestDecodeStream_SignatureDelta(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4","stop_reason":null}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think..."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc123sig"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}

event: message_stop
data: {"type":"message_stop"}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatal(err)
	}
	var foundSig bool
	for ev := range ch {
		if ev.Type == codec.EventSignatureDelta && ev.Delta != nil && ev.Delta.Signature == "abc123sig" {
			foundSig = true
		}
	}
	if !foundSig {
		t.Error("expected EventSignatureDelta with signature 'abc123sig'")
	}
}

// ---------------------------------------------------------------------------
// Task 4: ping event handling
// ---------------------------------------------------------------------------

func TestDecodeStream_PingEventIgnored(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4","stop_reason":null}}

event: ping
data: {"type":"ping"}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		if ev.Type == codec.EventRawPassthrough && ev.RawPassthrough != nil && ev.RawPassthrough.EventName == "ping" {
			t.Error("ping should not produce EventRawPassthrough")
		}
	}
}

// ---------------------------------------------------------------------------
// C12: decodeStream ignores content_block_stop
// ---------------------------------------------------------------------------

func TestDecodeStream_EmitsContentBlockStop(t *testing.T) {
	// Use the stream_text.txt fixture which includes content_block_stop events
	sseData := loadFixture(t, "claude/stream_text.txt")

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(sseData))),
	}

	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse failed: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// The stream fixture has a content_block_stop event.
	// The decoder should emit an EventContentBlockStop for it.
	var hasBlockStop bool
	for _, ev := range events {
		if ev.Type == codec.EventContentBlockStop {
			hasBlockStop = true
			break
		}
	}
	if !hasBlockStop {
		t.Error("decoder should emit EventContentBlockStop when it encounters content_block_stop SSE event")
	}
}

// ---------------------------------------------------------------------------
// Task 5: streaming tool_use → EventToolCallStart / ArgumentsDelta / End
// ---------------------------------------------------------------------------

func TestClaudeStreamDecode_ToolUseEventsCorrectShape(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4","stop_reason":null}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_x","name":"exec","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatal(err)
	}
	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Collect the new semantic events.
	var starts, argDeltas, ends []codec.Event
	for _, ev := range events {
		switch ev.Type {
		case codec.EventToolCallStart:
			starts = append(starts, ev)
		case codec.EventToolCallArgumentsDelta:
			argDeltas = append(argDeltas, ev)
		case codec.EventToolCallEnd:
			ends = append(ends, ev)
		}
	}

	// Exactly 1 Start with the right call_id and name.
	if len(starts) != 1 {
		t.Fatalf("want 1 EventToolCallStart, got %d", len(starts))
	}
	if starts[0].ToolCall == nil {
		t.Fatal("Start event missing ToolCall")
	}
	if starts[0].ToolCall.CallID != "toolu_x" {
		t.Errorf("Start CallID = %q, want toolu_x", starts[0].ToolCall.CallID)
	}
	if starts[0].ToolCall.Name != "exec" {
		t.Errorf("Start Name = %q, want exec", starts[0].ToolCall.Name)
	}

	// Exactly 2 ArgumentsDelta with the right fragments.
	if len(argDeltas) != 2 {
		t.Fatalf("want 2 EventToolCallArgumentsDelta, got %d", len(argDeltas))
	}
	if argDeltas[0].ToolCall == nil || argDeltas[0].ToolCall.Arguments != `{"cmd":` {
		t.Errorf("ArgsDelta[0] Arguments = %q, want {\"cmd\":", func() string {
			if argDeltas[0].ToolCall != nil {
				return argDeltas[0].ToolCall.Arguments
			}
			return "<nil>"
		}())
	}
	if argDeltas[1].ToolCall == nil || argDeltas[1].ToolCall.Arguments != `"ls"}` {
		t.Errorf("ArgsDelta[1] Arguments = %q, want \"ls\"}", func() string {
			if argDeltas[1].ToolCall != nil {
				return argDeltas[1].ToolCall.Arguments
			}
			return "<nil>"
		}())
	}

	// Exactly 1 End with full accumulated arguments.
	if len(ends) != 1 {
		t.Fatalf("want 1 EventToolCallEnd, got %d", len(ends))
	}
	if ends[0].ToolCall == nil {
		t.Fatal("End event missing ToolCall")
	}
	if ends[0].ToolCall.CallID != "toolu_x" {
		t.Errorf("End CallID = %q, want toolu_x", ends[0].ToolCall.CallID)
	}
	wantArgs := `{"cmd":"ls"}`
	if ends[0].ToolCall.Arguments != wantArgs {
		t.Errorf("End Arguments = %q, want %q", ends[0].ToolCall.Arguments, wantArgs)
	}

	// Invariant check.
	if err := codec.AssertStreamingToolCallInvariant(events); err != nil {
		t.Errorf("invariant violation: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestClaudeCodecSetsInboundProtocol — Task 7: InboundProtocol field
// ---------------------------------------------------------------------------

func TestClaudeCodecSetsInboundProtocol(t *testing.T) {
	body := `{"model":"claude-sonnet-4-20250514","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.InboundProtocol != codec.ProtocolClaude {
		t.Errorf("want %q, got %q", codec.ProtocolClaude, req.InboundProtocol)
	}
}
