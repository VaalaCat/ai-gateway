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
)

// ---------------------------------------------------------------------------
// Test helpers for stream encode tests
// ---------------------------------------------------------------------------

// sseEvent holds a parsed SSE event name + data line.
type sseEvent struct {
	Event string
	Data  string
}

// parseSSE splits raw SSE bytes into a slice of sseEvent.
func parseSSE(raw []byte) []sseEvent {
	var out []sseEvent
	var cur sseEvent
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "event: ") {
			cur.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			cur.Data = strings.TrimPrefix(line, "data: ")
		} else if line == "" && cur.Event != "" {
			out = append(out, cur)
			cur = sseEvent{}
		}
	}
	return out
}

// runEncodeStream drives ResponsesCodec.EncodeResponse in stream mode with the
// given events and returns the raw SSE bytes.
func runEncodeStream(t *testing.T, events []codec.Event) []byte {
	t.Helper()
	ch := make(chan codec.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)

	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}
	c := &ResponsesCodec{}
	if err := c.EncodeResponse(ch, w, true); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	return rec.Body.Bytes()
}

// ---------------------------------------------------------------------------
// New Task 4 tests — must be RED before encodeStream refactor
// ---------------------------------------------------------------------------

func TestResponsesEncodeStream_ToolCallCorrectShape(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventStreamStart},
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Index: 0, Name: "exec"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Arguments: `{"a"`}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Arguments: `:1}`}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "call_x", Arguments: `{"a":1}`}},
		{Type: codec.EventDone},
	}
	raw := runEncodeStream(t, events)
	sse := parseSSE(raw)

	var added, done int
	var argDeltas []string
	var argDoneArguments string
	for _, e := range sse {
		switch e.Event {
		case "response.output_item.added":
			if strings.Contains(e.Data, `"type":"function_call"`) {
				added++
				if !strings.Contains(e.Data, `"call_id":"call_x"`) {
					t.Errorf("output_item.added missing call_id=call_x: %s", e.Data)
				}
				if !strings.Contains(e.Data, `"name":"exec"`) {
					t.Errorf("output_item.added missing name=exec: %s", e.Data)
				}
				if !strings.Contains(e.Data, `"status":"in_progress"`) {
					t.Errorf("output_item.added missing status=in_progress: %s", e.Data)
				}
				// Bug B guard: id must not be the bare "fc_" with empty suffix
				var p struct {
					Item struct {
						ID string `json:"id"`
					} `json:"item"`
				}
				if err := json.Unmarshal([]byte(e.Data), &p); err == nil {
					if p.Item.ID == "fc_" {
						t.Errorf("output_item.added id must not be bare 'fc_', got: %s", e.Data)
					}
				}
			}
		case "response.function_call_arguments.delta":
			var p struct {
				Arguments string `json:"arguments"`
				Delta     struct {
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
				t.Fatalf("unmarshal args.delta: %v", err)
			}
			if p.Delta.Text != "" {
				argDeltas = append(argDeltas, p.Delta.Text)
			} else {
				argDeltas = append(argDeltas, p.Arguments)
			}
		case "response.function_call_arguments.done":
			var p struct {
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
				t.Fatalf("unmarshal args.done: %v", err)
			}
			argDoneArguments = p.Arguments
		case "response.output_item.done":
			if strings.Contains(e.Data, `"type":"function_call"`) {
				done++
				if !strings.Contains(e.Data, `"status":"completed"`) {
					t.Errorf("output_item.done missing status=completed: %s", e.Data)
				}
			}
		}
	}
	if added != 1 {
		t.Errorf("function_call output_item.added count = %d, want 1", added)
	}
	if done != 1 {
		t.Errorf("function_call output_item.done count = %d, want 1", done)
	}
	if len(argDeltas) < 2 {
		t.Errorf("function_call_arguments.delta count = %d, want >= 2", len(argDeltas))
	}
	if got := strings.Join(argDeltas, ""); got != `{"a":1}` {
		t.Errorf("accumulated arg deltas = %q, want {\"a\":1}", got)
	}
	if argDoneArguments != `{"a":1}` {
		t.Errorf("function_call_arguments.done arguments = %q, want {\"a\":1}", argDoneArguments)
	}
}

func TestResponsesEncodeStream_NoEmptyTextDelta(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Delta: &codec.DeltaPayload{Text: ""}}, // empty text — must be suppressed
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "c1", Name: "f"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: codec.EventDone},
	}
	raw := runEncodeStream(t, events)
	sse := parseSSE(raw)
	for _, e := range sse {
		if e.Event == "response.output_text.delta" {
			t.Fatalf("must not emit output_text.delta for empty text: %s", e.Data)
		}
		if e.Event == "response.output_item.added" && strings.Contains(e.Data, `"type":"message"`) {
			t.Fatalf("pure tool_call response must not open a message item: %s", e.Data)
		}
	}
}

func TestResponsesEncodeStream_OutputIndexUnique(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Delta: &codec.DeltaPayload{Text: "hello"}},
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "c1", Name: "f"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: codec.EventDone},
	}
	raw := runEncodeStream(t, events)
	sse := parseSSE(raw)
	seen := map[int]string{}
	for _, e := range sse {
		if e.Event != "response.output_item.added" {
			continue
		}
		var p struct {
			OutputIndex int `json:"output_index"`
			Item        struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
			t.Fatalf("unmarshal output_item.added: %v", err)
		}
		if prev, ok := seen[p.OutputIndex]; ok {
			t.Fatalf("output_index %d reused: first=%s second=%s", p.OutputIndex, prev, p.Item.Type)
		}
		seen[p.OutputIndex] = p.Item.Type
	}
	// message item gets index 0, function_call gets index 1
	if seen[0] != "message" {
		t.Errorf("output_index 0 = %q, want message", seen[0])
	}
	if seen[1] != "function_call" {
		t.Errorf("output_index 1 = %q, want function_call", seen[1])
	}
}

// ---------------------------------------------------------------------------
// TestResponsesEncodeNonStream_Text — basic non-stream response
// ---------------------------------------------------------------------------

func TestResponsesEncodeNonStream_Text(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart, Model: "gpt-4o"}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "Hello!",
		},
	}
	ch <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ResponsesCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp["object"] != "response" {
		t.Errorf("object = %v, want 'response'", resp["object"])
	}

	output, ok := resp["output"].([]any)
	if !ok || len(output) == 0 {
		t.Fatalf("output empty or wrong type")
	}

	// Find message item with output_text
	foundText := false
	for _, item := range output {
		m, _ := item.(map[string]any)
		if m["type"] == "message" {
			content, _ := m["content"].([]any)
			for _, cb := range content {
				block, _ := cb.(map[string]any)
				if block["type"] == "output_text" && block["text"] == "Hello!" {
					foundText = true
				}
			}
		}
	}
	if !foundText {
		t.Error("missing output_text 'Hello!' in response output")
	}

	// Check usage
	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatal("missing usage in response")
	}
	if usage["input_tokens"] != float64(10) {
		t.Errorf("input_tokens = %v, want 10", usage["input_tokens"])
	}
	if usage["output_tokens"] != float64(5) {
		t.Errorf("output_tokens = %v, want 5", usage["output_tokens"])
	}
}

// ---------------------------------------------------------------------------
// TestResponsesEncodeNonStream_CachedTokens — R3: emit input_tokens_details
// when cached tokens present
// ---------------------------------------------------------------------------

func TestResponsesEncodeNonStream_CachedTokens(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart, Model: "gpt-4o"}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "ok",
		},
	}
	ch <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     100,
			CompletionTokens: 10,
			TotalTokens:      110,
			CachedTokens:     80,
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ResponsesCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatal("missing usage in response")
	}

	// R3: must have input_tokens_details.cached_tokens
	details, ok := usage["input_tokens_details"].(map[string]any)
	if !ok {
		t.Fatal("missing input_tokens_details in usage")
	}
	if details["cached_tokens"] != float64(80) {
		t.Errorf("cached_tokens = %v, want 80", details["cached_tokens"])
	}
}

// ---------------------------------------------------------------------------
// TestResponsesEncodeNonStream_NoCachedTokens — R3: omit input_tokens_details
// when cached tokens is zero
// ---------------------------------------------------------------------------

func TestResponsesEncodeNonStream_NoCachedTokens(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart, Model: "gpt-4o"}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "ok",
		},
	}
	ch <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     50,
			CompletionTokens: 5,
			TotalTokens:      55,
			CachedTokens:     0,
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ResponsesCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatal("missing usage in response")
	}

	// R3: input_tokens_details should be omitted when cached_tokens is 0
	if _, exists := usage["input_tokens_details"]; exists {
		t.Error("input_tokens_details should be omitted when cached_tokens is 0")
	}
}

// ---------------------------------------------------------------------------
// TestResponsesEncodeSameProtocolPassthrough — tool passthrough on same protocol
// ---------------------------------------------------------------------------

func TestResponsesEncodeSameProtocolPassthrough(t *testing.T) {
	rawBuiltin := map[string]any{"type": "web_search", "external_web_access": true}
	req := &codec.Request{
		Model:           "gpt-5",
		Messages:        []codec.Message{{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}}},
		InboundProtocol: codec.ProtocolOpenAIResponses,
		Tools: []codec.Tool{
			{Type: "function", Name: "f", InputSchema: map[string]any{"type": "object"}},
			{Type: "web_search", RawConfig: rawBuiltin},
		},
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	c := &ResponsesCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	if !bytes.Contains(body, []byte(`"type":"web_search"`)) {
		t.Errorf("expected same-protocol passthrough of web_search, got: %s", body)
	}
	if dropped, ok := req.Metadata["dropped_tools"]; ok {
		t.Errorf("expected no dropped_tools on same-protocol call, got %v", dropped)
	}
}

// ---------------------------------------------------------------------------
// TestResponsesEncodeCrossProtocolFromChat — tool drop on cross-protocol
// ---------------------------------------------------------------------------

func TestResponsesEncodeCrossProtocolFromChat(t *testing.T) {
	rawBuiltin := map[string]any{"type": "web_search"}
	req := &codec.Request{
		Model:           "gpt-5",
		Messages:        []codec.Message{{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}}},
		InboundProtocol: codec.ProtocolOpenAIChat,
		Tools:           []codec.Tool{{Type: "web_search", RawConfig: rawBuiltin}},
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	c := &ResponsesCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	if bytes.Contains(body, []byte(`"web_search"`)) {
		t.Errorf("expected web_search dropped, but found in body: %s", body)
	}
}

// ---------------------------------------------------------------------------
// TestResponsesEncodeRejectsEmptyNameFunctionTool — missing name validation
// ---------------------------------------------------------------------------

func TestResponsesEncodeRejectsEmptyNameFunctionTool(t *testing.T) {
	req := &codec.Request{
		Model:           "gpt-5",
		Messages:        []codec.Message{{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "x"}}}},
		InboundProtocol: codec.ProtocolOpenAIResponses,
		Tools:           []codec.Tool{{Type: "function", Name: ""}},
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	c := &ResponsesCodec{}
	_, err := c.EncodeRequest(req, cfg)
	if !errors.Is(err, codec.ErrFunctionToolMissingName) {
		t.Errorf("want ErrFunctionToolMissingName, got %v", err)
	}
}
