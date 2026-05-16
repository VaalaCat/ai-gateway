package codec_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"

	// Blank imports to trigger init() registration.
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

var protocols = []codec.Protocol{
	codec.ProtocolOpenAIChat,
	codec.ProtocolOpenAIResponses,
	codec.ProtocolClaude,
}

var sampleRequests = map[codec.Protocol]string{
	codec.ProtocolOpenAIChat:      `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":false,"max_tokens":100}`,
	codec.ProtocolOpenAIResponses: `{"model":"test-model","input":[{"role":"user","content":"hello"}],"stream":false}`,
	codec.ProtocolClaude:          `{"model":"test-model","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`,
}

var expectedPaths = map[codec.Protocol]string{
	codec.ProtocolOpenAIChat:      "/v1/chat/completions",
	codec.ProtocolOpenAIResponses: "/v1/responses",
	codec.ProtocolClaude:          "/v1/messages",
}

// sampleResponses returns a mock non-streaming HTTP response body for each protocol.
var sampleResponses = map[codec.Protocol]string{
	codec.ProtocolOpenAIChat:      `{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
	codec.ProtocolOpenAIResponses: `{"id":"resp_test","object":"response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"world"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`,
	codec.ProtocolClaude:          `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"world"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`,
}

func outboundCfg() *codec.ChannelConfig {
	return &codec.ChannelConfig{
		BaseURL: "https://upstream.example.com",
		APIKey:  "test-key",
		Model:   "test-model",
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_RequestConversion
// ---------------------------------------------------------------------------

func TestMatrix_RequestConversion(t *testing.T) {
	for _, inProto := range protocols {
		t.Run("inbound_"+string(inProto), func(t *testing.T) {
			inCodec := codec.GetInbound(inProto)
			if inCodec == nil {
				t.Fatalf("no inbound codec registered for %s", inProto)
			}

			// Build an HTTP request carrying the sample body.
			body := sampleRequests[inProto]
			httpReq, err := http.NewRequest(http.MethodPost, "http://localhost"+expectedPaths[inProto], bytes.NewBufferString(body))
			if err != nil {
				t.Fatalf("create http request: %v", err)
			}
			httpReq.Header.Set("Content-Type", "application/json")

			// Decode → IR.
			ir, err := inCodec.DecodeRequest(httpReq)
			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}

			// Verify IR basics.
			if ir.Model != "test-model" {
				t.Errorf("expected model %q, got %q", "test-model", ir.Model)
			}

			// Find the user message.
			found := false
			for _, m := range ir.Messages {
				if m.Role == codec.RoleUser {
					for _, cb := range m.Content {
						if cb.Type == codec.ContentTypeText && cb.Text == "hello" {
							found = true
						}
					}
				}
			}
			if !found {
				t.Errorf("IR missing user message with text 'hello'; messages: %+v", ir.Messages)
			}

			// For each outbound protocol, encode the IR → HTTP request.
			for _, outProto := range protocols {
				t.Run("outbound_"+string(outProto), func(t *testing.T) {
					outCodec := codec.GetOutbound(outProto)
					if outCodec == nil {
						t.Fatalf("no outbound codec registered for %s", outProto)
					}

					outReq, err := outCodec.EncodeRequest(ir, outboundCfg())
					if err != nil {
						t.Fatalf("EncodeRequest: %v", err)
					}

					// Verify the URL host.
					if outReq.URL.Host != "upstream.example.com" {
						t.Errorf("expected host %q, got %q", "upstream.example.com", outReq.URL.Host)
					}

					// Verify the path matches the outbound protocol.
					wantPath := expectedPaths[outProto]
					if outReq.URL.Path != wantPath {
						t.Errorf("expected path %q, got %q", wantPath, outReq.URL.Path)
					}

					// Verify body is valid JSON.
					outBody, err := io.ReadAll(outReq.Body)
					if err != nil {
						t.Fatalf("read outbound body: %v", err)
					}
					if !json.Valid(outBody) {
						t.Errorf("outbound body is not valid JSON: %s", outBody)
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_ResponseRoundTrip
// ---------------------------------------------------------------------------

func TestMatrix_ResponseRoundTrip(t *testing.T) {
	for _, outProto := range protocols {
		t.Run("decode_"+string(outProto), func(t *testing.T) {
			outCodec := codec.GetOutbound(outProto)
			if outCodec == nil {
				t.Fatalf("no outbound codec registered for %s", outProto)
			}

			// Build a fake HTTP response with the sample body.
			respBody := sampleResponses[outProto]
			httpResp := &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(respBody)),
			}

			// Decode to IR events.
			eventCh, err := outCodec.DecodeResponse(httpResp, false)
			if err != nil {
				t.Fatalf("DecodeResponse: %v", err)
			}

			// Collect all events.
			var events []codec.Event
			for ev := range eventCh {
				events = append(events, ev)
			}

			// Verify we got a content delta with "world".
			foundText := false
			for _, ev := range events {
				if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "world" {
					foundText = true
				}
			}
			if !foundText {
				t.Fatalf("expected content delta with 'world' in events; got %+v", events)
			}

			// For each inbound protocol, encode the events back to the client.
			for _, inProto := range protocols {
				t.Run("encode_"+string(inProto), func(t *testing.T) {
					inCodec := codec.GetInbound(inProto)
					if inCodec == nil {
						t.Fatalf("no inbound codec registered for %s", inProto)
					}

					// Feed the collected events into a channel.
					ch := make(chan codec.Event, len(events))
					for _, ev := range events {
						ch <- ev
					}
					close(ch)

					// Encode to an httptest recorder (non-streaming).
					w := httptest.NewRecorder()
					err := inCodec.EncodeResponse(ch, w, false)
					if err != nil {
						t.Fatalf("EncodeResponse: %v", err)
					}

					// The response body should contain "world".
					respBytes := w.Body.String()
					if !strings.Contains(respBytes, "world") {
						t.Errorf("expected response to contain 'world', got: %s", respBytes)
					}

					// Should be valid JSON.
					if !json.Valid(w.Body.Bytes()) {
						t.Errorf("response is not valid JSON: %s", respBytes)
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRegistryCompleteness
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// TestMatrix_ToolCallRequestConversion
// ---------------------------------------------------------------------------

var sampleToolCallRequests = map[codec.Protocol]string{
	codec.ProtocolOpenAIChat:      `{"model":"test-model","messages":[{"role":"user","content":"Weather?"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"22C"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object"}}}],"stream":false}`,
	codec.ProtocolOpenAIResponses: `{"model":"test-model","input":[{"role":"user","content":"Weather?"},{"type":"function_call_output","call_id":"call_1","output":"22C"}],"tools":[{"type":"function","name":"get_weather","description":"Get weather","parameters":{"type":"object"}}],"stream":false}`,
	codec.ProtocolClaude:          `{"model":"test-model","max_tokens":100,"messages":[{"role":"user","content":"Weather?"},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"get_weather","input":{"city":"NYC"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"22C"}]}],"tools":[{"name":"get_weather","description":"Get weather","input_schema":{"type":"object"}}]}`,
}

func TestMatrix_ToolCallRequestConversion(t *testing.T) {
	for _, inProto := range protocols {
		t.Run("inbound_"+string(inProto), func(t *testing.T) {
			inCodec := codec.GetInbound(inProto)
			if inCodec == nil {
				t.Fatalf("no inbound codec registered for %s", inProto)
			}

			body := sampleToolCallRequests[inProto]
			httpReq, err := http.NewRequest(http.MethodPost, "http://localhost"+expectedPaths[inProto], bytes.NewBufferString(body))
			if err != nil {
				t.Fatalf("create http request: %v", err)
			}
			httpReq.Header.Set("Content-Type", "application/json")

			ir, err := inCodec.DecodeRequest(httpReq)
			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}

			// Verify IR has tool-related content.
			hasToolContent := len(ir.Tools) > 0
			for _, m := range ir.Messages {
				if len(m.ToolCalls) > 0 || m.ToolCallID != "" {
					hasToolContent = true
				}
				for _, cb := range m.Content {
					if cb.Type == codec.ContentTypeToolResult {
						hasToolContent = true
					}
				}
			}
			if !hasToolContent {
				t.Errorf("IR missing tool-related content; tools=%+v messages=%+v", ir.Tools, ir.Messages)
			}

			for _, outProto := range protocols {
				t.Run("outbound_"+string(outProto), func(t *testing.T) {
					outCodec := codec.GetOutbound(outProto)
					if outCodec == nil {
						t.Fatalf("no outbound codec registered for %s", outProto)
					}

					outReq, err := outCodec.EncodeRequest(ir, outboundCfg())
					if err != nil {
						t.Fatalf("EncodeRequest: %v", err)
					}

					wantPath := expectedPaths[outProto]
					if outReq.URL.Path != wantPath {
						t.Errorf("expected path %q, got %q", wantPath, outReq.URL.Path)
					}

					outBody, err := io.ReadAll(outReq.Body)
					if err != nil {
						t.Fatalf("read outbound body: %v", err)
					}
					if !json.Valid(outBody) {
						t.Errorf("outbound body is not valid JSON: %s", outBody)
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_ToolCallResponseRoundTrip
// ---------------------------------------------------------------------------

var sampleToolCallResponses = map[codec.Protocol]string{
	codec.ProtocolOpenAIChat:      `{"id":"chatcmpl-tc","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":8,"total_tokens":13}}`,
	codec.ProtocolOpenAIResponses: `{"id":"resp_tc","object":"response","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"NYC\"}"}],"usage":{"input_tokens":5,"output_tokens":8,"total_tokens":13}}`,
	codec.ProtocolClaude:          `{"id":"msg_tc","type":"message","role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"get_weather","input":{"city":"NYC"}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":8}}`,
}

func TestMatrix_ToolCallResponseRoundTrip(t *testing.T) {
	for _, outProto := range protocols {
		t.Run("decode_"+string(outProto), func(t *testing.T) {
			outCodec := codec.GetOutbound(outProto)
			if outCodec == nil {
				t.Fatalf("no outbound codec registered for %s", outProto)
			}

			respBody := sampleToolCallResponses[outProto]
			httpResp := &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(respBody)),
			}

			eventCh, err := outCodec.DecodeResponse(httpResp, false)
			if err != nil {
				t.Fatalf("DecodeResponse: %v", err)
			}

			var events []codec.Event
			for ev := range eventCh {
				events = append(events, ev)
			}

			// Verify at least one ToolCallDelta event exists.
			foundToolCall := false
			for _, ev := range events {
				if ev.Type == codec.EventToolCallDelta {
					foundToolCall = true
				}
			}
			if !foundToolCall {
				t.Fatalf("expected at least one EventToolCallDelta event; got %+v", events)
			}

			for _, inProto := range protocols {
				t.Run("encode_"+string(inProto), func(t *testing.T) {
					inCodec := codec.GetInbound(inProto)
					if inCodec == nil {
						t.Fatalf("no inbound codec registered for %s", inProto)
					}

					ch := make(chan codec.Event, len(events))
					for _, ev := range events {
						ch <- ev
					}
					close(ch)

					w := httptest.NewRecorder()
					err := inCodec.EncodeResponse(ch, w, false)
					if err != nil {
						t.Fatalf("EncodeResponse: %v", err)
					}

					if !json.Valid(w.Body.Bytes()) {
						t.Errorf("response is not valid JSON: %s", w.Body.String())
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_StreamingResponseRoundTrip
// ---------------------------------------------------------------------------

// flushRecorder implements http.Flusher over httptest.ResponseRecorder.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flushRecorder) Flush() {
	f.flushed++
	f.ResponseRecorder.Flush()
}

var streamingResponses = map[codec.Protocol]string{
	codec.ProtocolOpenAIChat:      "data: {\"id\":\"chatcmpl-s1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-s1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-s1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"},\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}]}\n\ndata: [DONE]\n\n",
	codec.ProtocolOpenAIResponses: "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_s1\"}}\n\nevent: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\nevent: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\nevent: response.content_part.delta\ndata: {\"type\":\"response.content_part.delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\nevent: response.content_part.done\ndata: {\"type\":\"response.content_part.done\"}\n\nevent: response.output_item.done\ndata: {\"type\":\"response.output_item.done\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s1\",\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n",
	codec.ProtocolClaude:          "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_s1\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
}

func TestMatrix_StreamingResponseRoundTrip(t *testing.T) {
	for _, outProto := range protocols {
		t.Run("decode_"+string(outProto), func(t *testing.T) {
			outCodec := codec.GetOutbound(outProto)
			if outCodec == nil {
				t.Fatalf("no outbound codec registered for %s", outProto)
			}

			sseData := streamingResponses[outProto]
			httpResp := &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(sseData)),
			}

			eventCh, err := outCodec.DecodeResponse(httpResp, true)
			if err != nil {
				t.Fatalf("DecodeResponse: %v", err)
			}

			var events []codec.Event
			for ev := range eventCh {
				events = append(events, ev)
			}

			// Verify we got text content "Hi".
			foundText := false
			for _, ev := range events {
				if ev.Type == codec.EventContentDelta && ev.Delta != nil && strings.Contains(ev.Delta.Text, "Hi") {
					foundText = true
				}
			}
			if !foundText {
				t.Fatalf("expected content delta with 'Hi' in events; got %+v", events)
			}

			// Encode to each inbound protocol as streaming.
			for _, inProto := range protocols {
				t.Run("encode_"+string(inProto), func(t *testing.T) {
					inCodec := codec.GetInbound(inProto)
					if inCodec == nil {
						t.Fatalf("no inbound codec registered for %s", inProto)
					}

					ch := make(chan codec.Event, len(events))
					for _, ev := range events {
						ch <- ev
					}
					close(ch)

					rec := httptest.NewRecorder()
					w := &flushRecorder{ResponseRecorder: rec}
					err := inCodec.EncodeResponse(ch, w, true)
					if err != nil {
						t.Fatalf("EncodeResponse stream: %v", err)
					}

					output := rec.Body.String()
					if len(output) == 0 {
						t.Error("SSE output is empty")
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRegistryCompleteness
// ---------------------------------------------------------------------------

func TestRegistryCompleteness(t *testing.T) {
	for _, p := range protocols {
		if codec.GetInbound(p) == nil {
			t.Errorf("no inbound codec registered for %s", p)
		}
		if codec.GetOutbound(p) == nil {
			t.Errorf("no outbound codec registered for %s", p)
		}
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_ExtendedRequestFields — new sampling/generation parameters
// ---------------------------------------------------------------------------

func TestMatrix_ExtendedRequestFields(t *testing.T) {
	// Chat Completion request with all extended fields
	chatBody := `{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":"hi"}],
		"stream":false,
		"max_tokens":200,
		"temperature":0.7,
		"top_p":0.9,
		"frequency_penalty":0.5,
		"presence_penalty":0.3,
		"seed":42,
		"n":2,
		"user":"test-user",
		"logprobs":true,
		"top_logprobs":5,
		"service_tier":"default",
		"reasoning_effort":"high",
		"response_format":{"type":"json_object"},
		"stream_options":{"include_usage":true},
		"logit_bias":{"50256":-100}
	}`

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatBody))
	r.Header.Set("Content-Type", "application/json")

	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)
	ir, err := inCodec.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	// Verify all extended fields decoded into IR
	if ir.FrequencyPenalty == nil || *ir.FrequencyPenalty != 0.5 {
		t.Errorf("FrequencyPenalty = %v, want 0.5", ir.FrequencyPenalty)
	}
	if ir.PresencePenalty == nil || *ir.PresencePenalty != 0.3 {
		t.Errorf("PresencePenalty = %v, want 0.3", ir.PresencePenalty)
	}
	if ir.Seed == nil || *ir.Seed != 42 {
		t.Errorf("Seed = %v, want 42", ir.Seed)
	}
	if ir.N != 2 {
		t.Errorf("N = %d, want 2", ir.N)
	}
	if ir.User != "test-user" {
		t.Errorf("User = %q, want test-user", ir.User)
	}
	if ir.Logprobs == nil || !*ir.Logprobs {
		t.Errorf("Logprobs = %v, want true", ir.Logprobs)
	}
	if ir.TopLogprobs == nil || *ir.TopLogprobs != 5 {
		t.Errorf("TopLogprobs = %v, want 5", ir.TopLogprobs)
	}
	if ir.ServiceTier != "default" {
		t.Errorf("ServiceTier = %q, want default", ir.ServiceTier)
	}
	if ir.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high", ir.ReasoningEffort)
	}
	if ir.ResponseFormat == nil {
		t.Error("ResponseFormat is nil, want json_object")
	}
	if ir.StreamOptions == nil {
		t.Error("StreamOptions is nil")
	}
	if ir.LogitBias == nil {
		t.Error("LogitBias is nil")
	}

	// Round-trip: encode back to Chat Completion format
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	outReq, err := outCodec.EncodeRequest(ir, outboundCfg())
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	outBody, _ := io.ReadAll(outReq.Body)
	var raw map[string]any
	json.Unmarshal(outBody, &raw)

	if raw["frequency_penalty"] != 0.5 {
		t.Errorf("encoded frequency_penalty = %v, want 0.5", raw["frequency_penalty"])
	}
	if raw["presence_penalty"] != 0.3 {
		t.Errorf("encoded presence_penalty = %v, want 0.3", raw["presence_penalty"])
	}
	if raw["seed"] != float64(42) {
		t.Errorf("encoded seed = %v, want 42", raw["seed"])
	}
	if raw["n"] != float64(2) {
		t.Errorf("encoded n = %v, want 2", raw["n"])
	}
	if raw["user"] != "test-user" {
		t.Errorf("encoded user = %v, want test-user", raw["user"])
	}
	if raw["reasoning_effort"] != "high" {
		t.Errorf("encoded reasoning_effort = %v, want high", raw["reasoning_effort"])
	}
	if raw["response_format"] == nil {
		t.Error("encoded response_format is nil")
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_ReasoningContentRoundTrip — reasoning_content across protocols
// ---------------------------------------------------------------------------

func TestMatrix_ReasoningContentRoundTrip(t *testing.T) {
	// Create IR events with thinking + content
	events := []codec.Event{
		{Type: codec.EventStreamStart},
		{
			Type: codec.EventThinkingDelta,
			Delta: &codec.DeltaPayload{
				ContentType: codec.ContentTypeThinking,
				Text:        "Let me think about this.",
			},
		},
		{
			Type: codec.EventContentDelta,
			Delta: &codec.DeltaPayload{
				ContentType: codec.ContentTypeText,
				Text:        "The answer is 42.",
			},
		},
		{
			Type: codec.EventUsage,
			Usage: &codec.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
				ReasoningTokens:  15,
			},
		},
		{Type: codec.EventDone},
	}

	for _, inProto := range protocols {
		t.Run("nonstream_"+string(inProto), func(t *testing.T) {
			inCodec := codec.GetInbound(inProto)
			ch := make(chan codec.Event, len(events))
			for _, ev := range events {
				ch <- ev
			}
			close(ch)

			w := httptest.NewRecorder()
			err := inCodec.EncodeResponse(ch, w, false)
			if err != nil {
				t.Fatalf("EncodeResponse: %v", err)
			}

			body := w.Body.String()
			if !strings.Contains(body, "42") {
				t.Errorf("response missing content '42': %s", body)
			}
			// Chat/Claude should contain thinking data in some form
			if !json.Valid(w.Body.Bytes()) {
				t.Errorf("response is not valid JSON: %s", body)
			}
		})

		t.Run("stream_"+string(inProto), func(t *testing.T) {
			inCodec := codec.GetInbound(inProto)
			ch := make(chan codec.Event, len(events))
			for _, ev := range events {
				ch <- ev
			}
			close(ch)

			rec := httptest.NewRecorder()
			w := &flushRecorder{ResponseRecorder: rec}
			err := inCodec.EncodeResponse(ch, w, true)
			if err != nil {
				t.Fatalf("EncodeResponse stream: %v", err)
			}

			output := rec.Body.String()
			if len(output) == 0 {
				t.Error("SSE output is empty")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_RefusalRoundTrip — refusal field across protocols
// ---------------------------------------------------------------------------

func TestMatrix_RefusalRoundTrip(t *testing.T) {
	// Decode a Chat Completion response with refusal
	chatResp := `{
		"id":"chatcmpl-ref","object":"chat.completion",
		"model":"gpt-4o","created":1700000000,
		"choices":[{
			"index":0,
			"message":{"role":"assistant","content":"","refusal":"I cannot help with that."},
			"finish_reason":"stop"
		}],
		"usage":{"prompt_tokens":5,"completion_tokens":8,"total_tokens":13}
	}`

	httpResp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(chatResp)),
	}

	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	eventCh, err := outCodec.DecodeResponse(httpResp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range eventCh {
		events = append(events, ev)
	}

	// Verify refusal was decoded
	foundRefusal := false
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Refusal != "" {
			foundRefusal = true
			if ev.Delta.Refusal != "I cannot help with that." {
				t.Errorf("refusal = %q, want 'I cannot help with that.'", ev.Delta.Refusal)
			}
		}
	}
	if !foundRefusal {
		t.Error("missing refusal in decoded events")
	}

	// Verify model/created were captured
	foundModel := false
	for _, ev := range events {
		if ev.Model == "gpt-4o" {
			foundModel = true
		}
	}
	if !foundModel {
		t.Error("missing model passthrough in events")
	}

	// Re-encode to Chat Completion format
	ch := make(chan codec.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)

	w := httptest.NewRecorder()
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)
	err = inCodec.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		t.Fatal("no choices in encoded response")
	}
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["refusal"] != "I cannot help with that." {
		t.Errorf("encoded refusal = %v, want 'I cannot help with that.'", msg["refusal"])
	}
	if resp["model"] != "gpt-4o" {
		t.Errorf("encoded model = %v, want gpt-4o", resp["model"])
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_DetailedUsageRoundTrip — token details across protocols
// ---------------------------------------------------------------------------

func TestMatrix_DetailedUsageRoundTrip(t *testing.T) {
	// Decode a Chat Completion response with detailed usage
	chatResp := `{
		"id":"chatcmpl-usage","object":"chat.completion",
		"choices":[{
			"index":0,
			"message":{"role":"assistant","content":"result"},
			"finish_reason":"stop"
		}],
		"usage":{
			"prompt_tokens":100,
			"completion_tokens":50,
			"total_tokens":150,
			"completion_tokens_details":{
				"reasoning_tokens":30,
				"accepted_prediction_tokens":5,
				"rejected_prediction_tokens":2
			},
			"prompt_tokens_details":{
				"cached_tokens":20
			}
		}
	}`

	httpResp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(chatResp)),
	}

	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	eventCh, err := outCodec.DecodeResponse(httpResp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range eventCh {
		events = append(events, ev)
	}

	// Verify detailed usage was decoded
	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			foundUsage = true
			u := ev.Usage
			if u.ReasoningTokens != 30 {
				t.Errorf("ReasoningTokens = %d, want 30", u.ReasoningTokens)
			}
			if u.CachedTokens != 20 {
				t.Errorf("CachedTokens = %d, want 20", u.CachedTokens)
			}
			if u.AcceptedPredictionTokens != 5 {
				t.Errorf("AcceptedPredictionTokens = %d, want 5", u.AcceptedPredictionTokens)
			}
			if u.RejectedPredictionTokens != 2 {
				t.Errorf("RejectedPredictionTokens = %d, want 2", u.RejectedPredictionTokens)
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event")
	}

	// Re-encode to Chat Completion and verify details survive
	ch := make(chan codec.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)

	w := httptest.NewRecorder()
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)
	err = inCodec.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	usage, _ := resp["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("missing usage in encoded response")
	}

	details, _ := usage["completion_tokens_details"].(map[string]any)
	if details == nil {
		t.Fatal("missing completion_tokens_details")
	}
	if details["reasoning_tokens"] != float64(30) {
		t.Errorf("reasoning_tokens = %v, want 30", details["reasoning_tokens"])
	}

	promptDetails, _ := usage["prompt_tokens_details"].(map[string]any)
	if promptDetails == nil {
		t.Fatal("missing prompt_tokens_details")
	}
	if promptDetails["cached_tokens"] != float64(20) {
		t.Errorf("cached_tokens = %v, want 20", promptDetails["cached_tokens"])
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_StreamReasoningContent — streaming reasoning across protocols
// ---------------------------------------------------------------------------

func TestMatrix_StreamReasoningContent(t *testing.T) {
	// Simulate a streaming response with reasoning_content (Qwen/DeepSeek style)
	sseData := "data: {\"id\":\"chatcmpl-r1\",\"object\":\"chat.completion.chunk\",\"model\":\"qwen3-32b\",\"created\":1700000000,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"Thinking...\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-r1\",\"object\":\"chat.completion.chunk\",\"model\":\"qwen3-32b\",\"created\":1700000000,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello!\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-r1\",\"object\":\"chat.completion.chunk\",\"model\":\"qwen3-32b\",\"created\":1700000000,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	httpResp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	eventCh, err := outCodec.DecodeResponse(httpResp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range eventCh {
		events = append(events, ev)
	}

	// Verify thinking delta was captured
	foundThinking := false
	foundContent := false
	for _, ev := range events {
		if ev.Type == codec.EventThinkingDelta && ev.Delta != nil && ev.Delta.Text == "Thinking..." {
			foundThinking = true
		}
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "Hello!" {
			foundContent = true
		}
	}
	if !foundThinking {
		t.Error("missing thinking delta")
	}
	if !foundContent {
		t.Error("missing content delta")
	}

	// Re-encode as streaming to all protocols
	for _, inProto := range protocols {
		t.Run("encode_"+string(inProto), func(t *testing.T) {
			inCodec := codec.GetInbound(inProto)
			ch := make(chan codec.Event, len(events))
			for _, ev := range events {
				ch <- ev
			}
			close(ch)

			rec := httptest.NewRecorder()
			w := &flushRecorder{ResponseRecorder: rec}
			err := inCodec.EncodeResponse(ch, w, true)
			if err != nil {
				t.Fatalf("EncodeResponse: %v", err)
			}

			output := rec.Body.String()
			if len(output) == 0 {
				t.Error("SSE output is empty")
			}

			// For Chat Completion, verify reasoning_content appears in output
			if inProto == codec.ProtocolOpenAIChat {
				if !strings.Contains(output, "reasoning_content") {
					t.Errorf("Chat stream output missing reasoning_content:\n%s", output)
				}
				if !strings.Contains(output, "Hello!") {
					t.Errorf("Chat stream output missing content:\n%s", output)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_ResponseFormatRoundTrip — response_format passthrough
// ---------------------------------------------------------------------------

func TestMatrix_ResponseFormatRoundTrip(t *testing.T) {
	body := `{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":"give me json"}],
		"stream":false,
		"response_format":{"type":"json_schema","json_schema":{"name":"output","schema":{"type":"object","properties":{"answer":{"type":"string"}}},"strict":true}}
	}`

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)
	ir, err := inCodec.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if ir.ResponseFormat == nil {
		t.Fatal("ResponseFormat is nil after decode")
	}

	// Round-trip encode
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	outReq, err := outCodec.EncodeRequest(ir, outboundCfg())
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	outBody, _ := io.ReadAll(outReq.Body)
	var raw map[string]any
	json.Unmarshal(outBody, &raw)

	rf, ok := raw["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or not object: %v", raw["response_format"])
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format.type = %v, want json_schema", rf["type"])
	}
	schema, _ := rf["json_schema"].(map[string]any)
	if schema == nil {
		t.Error("response_format.json_schema is nil")
	}
	if schema["name"] != "output" {
		t.Errorf("json_schema.name = %v, want output", schema["name"])
	}
	if schema["strict"] != true {
		t.Errorf("json_schema.strict = %v, want true", schema["strict"])
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_ClaudeThinkingBlocks — Claude thinking block decode
// ---------------------------------------------------------------------------

func TestMatrix_ClaudeThinkingBlocks(t *testing.T) {
	// Non-streaming Claude response with thinking block
	claudeResp := `{
		"id":"msg_think","type":"message","role":"assistant",
		"content":[
			{"type":"thinking","thinking":"Let me analyze this step by step."},
			{"type":"text","text":"The answer is 42."}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":20}
	}`

	httpResp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(claudeResp)),
	}

	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	eventCh, err := outCodec.DecodeResponse(httpResp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range eventCh {
		events = append(events, ev)
	}

	foundThinking := false
	foundContent := false
	for _, ev := range events {
		if ev.Type == codec.EventThinkingDelta && ev.Delta != nil && strings.Contains(ev.Delta.Text, "step by step") {
			foundThinking = true
		}
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "The answer is 42." {
			foundContent = true
		}
	}
	if !foundThinking {
		t.Error("missing thinking delta from Claude thinking block")
	}
	if !foundContent {
		t.Error("missing content delta")
	}

	// Re-encode to Chat Completion format — thinking should become reasoning_content
	ch := make(chan codec.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)

	w := httptest.NewRecorder()
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)
	err = inCodec.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		t.Fatal("no choices")
	}
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["reasoning_content"] == nil || msg["reasoning_content"] == "" {
		t.Error("reasoning_content missing in Chat response from Claude thinking")
	}
	if msg["content"] != "The answer is 42." {
		t.Errorf("content = %v, want 'The answer is 42.'", msg["content"])
	}
}

// ---------------------------------------------------------------------------
// TestMatrix_ClaudeStreamThinkingBlocks — Claude streaming thinking decode
// ---------------------------------------------------------------------------

func TestMatrix_ClaudeStreamThinkingBlocks(t *testing.T) {
	sseData := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_t1\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Analyzing...\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Result.\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	httpResp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	eventCh, err := outCodec.DecodeResponse(httpResp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range eventCh {
		events = append(events, ev)
	}

	foundThinking := false
	foundContent := false
	for _, ev := range events {
		if ev.Type == codec.EventThinkingDelta && ev.Delta != nil && ev.Delta.Text == "Analyzing..." {
			foundThinking = true
		}
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "Result." {
			foundContent = true
		}
	}
	if !foundThinking {
		t.Error("missing thinking delta from Claude stream")
	}
	if !foundContent {
		t.Error("missing content delta from Claude stream")
	}

	// Cross-protocol: encode as Chat Completion stream
	ch := make(chan codec.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)

	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)
	err = inCodec.EncodeResponse(ch, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	output := rec.Body.String()
	if !strings.Contains(output, "reasoning_content") {
		t.Errorf("Claude thinking not converted to reasoning_content in Chat stream:\n%s", output)
	}
	if !strings.Contains(output, "Result.") {
		t.Errorf("missing content in Chat stream:\n%s", output)
	}
}
