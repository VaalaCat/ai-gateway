package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/stretchr/testify/require"
)

// collectResponsesStreamEvents runs the responses stream decoder on the given SSE string
// and returns all emitted events. This is a test helper used by streaming tests.
func collectResponsesStreamEvents(t *testing.T, sseData string) []codec.Event {
	t.Helper()
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}
	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeRequest_Simple — basic parsing from fixture
// ---------------------------------------------------------------------------

func TestResponsesDecodeRequest_Simple(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hello!"}],"instructions":"You are helpful.","stream":false}`
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ResponsesCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if req.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", req.Model)
	}
	if req.Stream {
		t.Error("stream = true, want false")
	}
	// instructions → system message, input → user message
	if len(req.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != codec.RoleSystem {
		t.Errorf("msg[0].role = %q, want system", req.Messages[0].Role)
	}
	if req.Messages[0].Content[0].Text != "You are helpful." {
		t.Errorf("msg[0].content = %q, want 'You are helpful.'", req.Messages[0].Content[0].Text)
	}
	if req.Messages[1].Role != codec.RoleUser {
		t.Errorf("msg[1].role = %q, want user", req.Messages[1].Role)
	}
	if req.Messages[1].Content[0].Text != "Hello!" {
		t.Errorf("msg[1].content = %q, want 'Hello!'", req.Messages[1].Content[0].Text)
	}
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeResponse_Text — non-stream text response
// ---------------------------------------------------------------------------

func TestResponsesDecodeResponse_Text(t *testing.T) {
	body := `{"id":"resp_abc123","object":"response","model":"gpt-4o","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello! How can I help?"}]}],"usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Should have: StreamStart, ContentDelta, Usage, Done
	if len(events) < 3 {
		t.Fatalf("events len = %d, want >= 3", len(events))
	}

	// First event is stream start
	if events[0].Type != codec.EventStreamStart {
		t.Errorf("events[0].type = %v, want StreamStart", events[0].Type)
	}
	if events[0].Model != "gpt-4o" {
		t.Errorf("events[0].model = %q, want gpt-4o", events[0].Model)
	}

	// Find content delta
	foundText := false
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "Hello! How can I help?" {
			foundText = true
		}
	}
	if !foundText {
		t.Error("missing content delta with expected text")
	}

	// Find usage
	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.PromptTokens == 20 && ev.Usage.CompletionTokens == 10 && ev.Usage.TotalTokens == 30 {
				foundUsage = true
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event with expected values")
	}
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeResponse_FunctionCall — R4: non-stream function_call
// must set FinishReason=tool_calls
// ---------------------------------------------------------------------------

func TestResponsesDecodeResponse_FunctionCall(t *testing.T) {
	body := `{"id":"resp_abc456","object":"response","model":"gpt-4o","output":[{"type":"function_call","id":"fc_call_abc","call_id":"call_abc","name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}],"usage":{"input_tokens":50,"output_tokens":20,"total_tokens":70}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Should have tool call delta
	foundToolCall := false
	for _, ev := range events {
		if ev.Type == codec.EventToolCallDelta && ev.Delta != nil && ev.Delta.ToolCall != nil {
			tc := ev.Delta.ToolCall
			if tc.Name == "get_weather" && tc.ID == "call_abc" {
				foundToolCall = true
			}
		}
	}
	if !foundToolCall {
		t.Error("missing tool call delta for get_weather")
	}

	// R4: Done event must have FinishReason = "tool_calls"
	lastEvent := events[len(events)-1]
	if lastEvent.Type != codec.EventDone {
		t.Fatalf("last event = %v, want Done", lastEvent.Type)
	}
	if lastEvent.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want 'tool_calls'", lastEvent.FinishReason)
	}
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeResponse_FinishReason — R4: normal text completion
// must set FinishReason=stop
// ---------------------------------------------------------------------------

func TestResponsesDecodeResponse_FinishReason(t *testing.T) {
	body := `{"id":"resp_stop1","object":"response","model":"gpt-4o","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there!"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// R4: Done event must have FinishReason = "stop" for normal completion
	lastEvent := events[len(events)-1]
	if lastEvent.Type != codec.EventDone {
		t.Fatalf("last event = %v, want Done", lastEvent.Type)
	}
	if lastEvent.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want 'stop'", lastEvent.FinishReason)
	}
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeStream_Refusal — R2: refusal delta should set Refusal
// field, not Text
// ---------------------------------------------------------------------------

func TestResponsesDecodeStream_Refusal(t *testing.T) {
	sseData := `event: response.created
data: {"type":"response.created","response":{"id":"resp_ref1"}}

event: response.refusal.delta
data: {"type":"response.refusal.delta","delta":{"type":"refusal","text":"I cannot"}}

event: response.refusal.delta
data: {"type":"response.refusal.delta","delta":{"type":"refusal","text":" help with that."}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_ref1","usage":{"input_tokens":5,"output_tokens":6,"total_tokens":11}}}

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// R2: refusal deltas must populate ev.Delta.Refusal, NOT ev.Delta.Text
	var refusalText strings.Builder
	var plainText strings.Builder
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil {
			refusalText.WriteString(ev.Delta.Refusal)
			plainText.WriteString(ev.Delta.Text)
		}
	}

	if refusalText.String() != "I cannot help with that." {
		t.Errorf("refusal = %q, want 'I cannot help with that.'", refusalText.String())
	}
	if plainText.String() != "" {
		t.Errorf("text = %q, want empty (refusal should not be in Text field)", plainText.String())
	}
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeNonStream_CachedTokens — R3: decode cached tokens
// from response
// ---------------------------------------------------------------------------

func TestResponsesDecodeNonStream_CachedTokens(t *testing.T) {
	body := `{"id":"resp_cache1","object":"response","model":"gpt-4o","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110,"input_tokens_details":{"cached_tokens":80}}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// R3: usage event must include CachedTokens
	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.CachedTokens == 80 {
				foundUsage = true
			} else {
				t.Errorf("cached_tokens = %d, want 80", ev.Usage.CachedTokens)
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event with cached tokens")
	}
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeStream_FinishReason — R4: stream response.completed
// must infer FinishReason from output items
// ---------------------------------------------------------------------------

func TestResponsesDecodeStream_FinishReason_Stop(t *testing.T) {
	sseData := `event: response.created
data: {"type":"response.created","response":{"id":"resp_s1","model":"gpt-4o"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":{"type":"text_delta","text":"Hello"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_s1","output":[{"type":"message"}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Last event should be Done with FinishReason = "stop"
	lastEvent := events[len(events)-1]
	if lastEvent.Type != codec.EventDone {
		t.Fatalf("last event = %v, want Done", lastEvent.Type)
	}
	if lastEvent.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want 'stop'", lastEvent.FinishReason)
	}
}

func TestResponsesDecodeStream_FinishReason_ToolCalls(t *testing.T) {
	sseData := `event: response.created
data: {"type":"response.created","response":{"id":"resp_tc1","model":"gpt-4o"}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"search","arguments":"{\"q\":\"test\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_tc1","output":[{"type":"function_call"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Last event should be Done with FinishReason = "tool_calls"
	lastEvent := events[len(events)-1]
	if lastEvent.Type != codec.EventDone {
		t.Fatalf("last event = %v, want Done", lastEvent.Type)
	}
	if lastEvent.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want 'tool_calls'", lastEvent.FinishReason)
	}
}

// ---------------------------------------------------------------------------
// TestResponsesDecodeStream_CachedTokens — R3: stream decode cached tokens
// ---------------------------------------------------------------------------

func TestResponsesDecodeStream_CachedTokens(t *testing.T) {
	sseData := `event: response.created
data: {"type":"response.created","response":{"id":"resp_sc1"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":{"type":"text_delta","text":"ok"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_sc1","usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,"input_tokens_details":{"cached_tokens":60}}}}

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &ResponsesCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.CachedTokens == 60 {
				foundUsage = true
			} else {
				t.Errorf("cached_tokens = %d, want 60", ev.Usage.CachedTokens)
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event with cached tokens in stream")
	}
}

// ---------------------------------------------------------------------------
// TestResponsesCodecSetsInboundProtocol — Task 7: InboundProtocol field
// ---------------------------------------------------------------------------

func TestResponsesCodecSetsInboundProtocol(t *testing.T) {
	body := `{"model":"gpt-5","input":"hi"}`
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	c := &ResponsesCodec{}
	req, err := c.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.InboundProtocol != codec.ProtocolOpenAIResponses {
		t.Errorf("want %q, got %q", codec.ProtocolOpenAIResponses, req.InboundProtocol)
	}
}

// ---------------------------------------------------------------------------
// TestResponsesStreamDecode_ToolCallEvents — Task 8: responses stream decoder
// must emit EventToolCallStart / EventToolCallArgumentsDelta / EventToolCallEnd
// ---------------------------------------------------------------------------

func TestResponsesStreamDecode_ToolCallEvents(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_x","call_id":"call_x","name":"exec","status":"in_progress"}}`,
		``,
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_x","output_index":1,"delta":{"type":"text_delta","text":"{\"a\""}}`,
		``,
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_x","output_index":1,"delta":{"type":"text_delta","text":":1}"}}`,
		``,
		`event: response.function_call_arguments.done`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_x","output_index":1,"arguments":"{\"a\":1}"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_x","call_id":"call_x","name":"exec","status":"completed","arguments":"{\"a\":1}"}}`,
		``,
		`event: response.completed`,
		`data: {}`,
		``,
	}, "\n")
	events := collectResponsesStreamEvents(t, sse)

	var hasStart, hasEnd bool
	var deltas []string
	for _, ev := range events {
		switch ev.Type {
		case codec.EventToolCallStart:
			hasStart = true
			require.NotNil(t, ev.ToolCall)
			require.Equal(t, "call_x", ev.ToolCall.CallID)
			require.Equal(t, "exec", ev.ToolCall.Name)
		case codec.EventToolCallArgumentsDelta:
			require.NotNil(t, ev.ToolCall)
			deltas = append(deltas, ev.ToolCall.Arguments)
		case codec.EventToolCallEnd:
			hasEnd = true
			require.NotNil(t, ev.ToolCall)
			require.Equal(t, `{"a":1}`, ev.ToolCall.Arguments)
		}
	}
	require.True(t, hasStart, "expected EventToolCallStart")
	require.Len(t, deltas, 2, "expected 2 EventToolCallArgumentsDelta events")
	require.True(t, hasEnd, "expected EventToolCallEnd")
	require.NoError(t, codec.AssertStreamingToolCallInvariant(events))
}
