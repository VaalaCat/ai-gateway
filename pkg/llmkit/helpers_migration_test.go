package llmkit_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	codec "github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

type testChannelConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	OnDroppedTools func([]codec.DroppedTool)
}

type testInboundCodec interface {
	DecodeRequest(*http.Request) (*codec.Request, error)
	EncodeResponse(<-chan codec.Event, http.ResponseWriter, bool) error
}

type testOutboundCodec interface {
	EncodeRequest(*codec.Request, *testChannelConfig) (*http.Request, error)
	DecodeResponse(*http.Response, bool) (<-chan codec.Event, error)
}

type publicCodecAdapter struct {
	codec    codec.Codec
	protocol codec.Protocol
	state    codec.ConversionState
}

func getInbound(protocol codec.Protocol) testInboundCodec {
	return &publicCodecAdapter{codec: codec.NewCodec(), protocol: protocol}
}

func getOutbound(protocol codec.Protocol) testOutboundCodec {
	return &publicCodecAdapter{codec: codec.NewCodec(), protocol: protocol}
}

func (adapter *publicCodecAdapter) DecodeRequest(request *http.Request) (*codec.Request, error) {
	method, path := endpointForProtocol(adapter.protocol)
	if request.Method != "" {
		method = request.Method
	}
	if request.URL != nil && request.URL.Path != "" {
		path = request.URL.Path
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	decoded, err := adapter.codec.DecodeRequest(codec.DecodeRequestInput{
		Method: method, Path: path, Headers: request.Header, Body: body,
	})
	if err != nil {
		return nil, err
	}
	return &decoded.Request, nil
}

func (adapter *publicCodecAdapter) EncodeRequest(request *codec.Request, config *testChannelConfig) (*http.Request, error) {
	encoded, err := adapter.codec.EncodeRequest(codec.EncodeRequestInput{
		Request: *request,
		Target: codec.Target{
			Protocol: adapter.protocol,
			BaseURL:  config.BaseURL,
			APIKey:   config.APIKey,
			Model:    config.Model,
		},
		Options: codec.ConversionOptions{OnDroppedTools: config.OnDroppedTools},
	})
	if err != nil {
		return nil, err
	}
	adapter.state = encoded.State
	httpRequest, err := http.NewRequest(encoded.Method, strings.TrimRight(config.BaseURL, "/")+encoded.Path, bytes.NewReader(encoded.Body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header = make(http.Header, len(encoded.Headers))
	for key, values := range encoded.Headers {
		httpRequest.Header[key] = append([]string(nil), values...)
	}
	return httpRequest, nil
}

func TestPublicCodecAdapterCopiesEncodedHeaders(t *testing.T) {
	request := &codec.Request{
		Model: "gpt-5",
		Messages: []codec.Message{{
			Role:    codec.RoleUser,
			Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hello"}},
		}},
	}
	outbound := getOutbound(codec.ProtocolOpenAIChat)
	encoded, err := outbound.EncodeRequest(request, &testChannelConfig{
		BaseURL: "https://test.example.com",
		APIKey:  "test-api-key",
		Model:   "gpt-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := encoded.Header.Get("Authorization"); got != "Bearer test-api-key" {
		t.Fatalf("Authorization = %q, want Bearer test-api-key", got)
	}
}

func (adapter *publicCodecAdapter) DecodeResponse(response *http.Response, stream bool) (<-chan codec.Event, error) {
	return adapter.codec.DecodeResponse(context.Background(), codec.DecodeResponseInput{
		Protocol: adapter.protocol, StatusCode: response.StatusCode, Headers: response.Header,
		Body: response.Body, Stream: stream, State: adapter.state,
	})
}

func (adapter *publicCodecAdapter) EncodeResponse(events <-chan codec.Event, writer http.ResponseWriter, stream bool) error {
	chunks, err := adapter.codec.EncodeResponse(context.Background(), codec.EncodeResponseInput{
		Protocol: adapter.protocol, Events: events, Stream: stream,
	})
	if err != nil {
		return err
	}
	for chunk := range chunks {
		if chunk.Err != nil {
			return chunk.Err
		}
		if _, err := writer.Write(chunk.Data); err != nil {
			return err
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return nil
}

func endpointForProtocol(protocol codec.Protocol) (string, string) {
	switch protocol {
	case codec.ProtocolOpenAIResponses:
		return http.MethodPost, "/v1/responses"
	case codec.ProtocolClaude:
		return http.MethodPost, "/v1/messages"
	default:
		return http.MethodPost, "/v1/chat/completions"
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (recorder *flushRecorder) Flush() {
	recorder.flushed++
	recorder.ResponseRecorder.Flush()
}

type toolCallState int

const (
	toolCallStarted toolCallState = iota
	toolCallEnded
)

func assertStreamingToolCallInvariant(events []codec.Event) error {
	states := make(map[string]toolCallState)
	for index, event := range events {
		switch event.Type {
		case codec.EventToolCallStart:
			if event.ToolCall == nil || event.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: Start event missing CallID", index)
			}
			if _, exists := states[event.ToolCall.CallID]; exists {
				return fmt.Errorf("event %d: duplicate Start for call_id %s", index, event.ToolCall.CallID)
			}
			states[event.ToolCall.CallID] = toolCallStarted
		case codec.EventToolCallArgumentsDelta:
			if event.ToolCall == nil || event.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: ArgumentsDelta event missing CallID", index)
			}
			state, exists := states[event.ToolCall.CallID]
			if !exists {
				return fmt.Errorf("event %d: ArgumentsDelta without Start for call_id %s", index, event.ToolCall.CallID)
			}
			if state == toolCallEnded {
				return fmt.Errorf("event %d: ArgumentsDelta after End for call_id %s", index, event.ToolCall.CallID)
			}
		case codec.EventToolCallEnd:
			if event.ToolCall == nil || event.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: End event missing CallID", index)
			}
			state, exists := states[event.ToolCall.CallID]
			if !exists {
				return fmt.Errorf("event %d: End without Start for call_id %s", index, event.ToolCall.CallID)
			}
			if state == toolCallEnded {
				return fmt.Errorf("event %d: duplicate End for call_id %s", index, event.ToolCall.CallID)
			}
			states[event.ToolCall.CallID] = toolCallEnded
		}
	}
	for callID, state := range states {
		if state == toolCallStarted {
			return fmt.Errorf("call_id %s: Start without End (unterminated stream)", callID)
		}
	}
	return nil
}

// roundTripRequest decodes and re-encodes a request through the public Codec.
func roundTripRequest(t *testing.T, inProto, outProto codec.Protocol, reqBody string) map[string]any {
	t.Helper()
	inCodec := getInbound(inProto)
	outCodec := getOutbound(outProto)
	if inCodec == nil || outCodec == nil {
		t.Fatalf("public codec adapters unavailable: in=%s out=%s", inProto, outProto)
	}
	httpReq := &http.Request{Body: io.NopCloser(bytes.NewBufferString(reqBody))}
	irReq, err := inCodec.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	cfg := &testChannelConfig{BaseURL: "https://test.example.com", APIKey: "test", Model: irReq.Model}
	outReq, err := outCodec.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var result map[string]any
	if err := json.NewDecoder(outReq.Body).Decode(&result); err != nil {
		t.Fatalf("decode output body: %v", err)
	}
	return result
}

// mustGetString extracts a string value from a nested map path.
func mustGetString(t *testing.T, m map[string]any, keys ...string) string {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("expected map at key path %v, got %T", keys, cur)
		}
		cur, ok = obj[k]
		if !ok {
			t.Fatalf("key %q not found in %v", k, keys)
		}
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("expected string at key path %v, got %T (%v)", keys, cur, cur)
	}
	return s
}

// mustGetArray extracts an array from a map.
func mustGetArray(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q not found", key)
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("key %q: expected []any, got %T", key, v)
	}
	return arr
}

var updateGolden = flag.Bool("update", false, "update golden files")

func roundTripStream(
	t *testing.T,
	fixturePath string,
	outboundCodec testOutboundCodec,
	inboundCodec testInboundCodec,
	decodeAsStream bool,
	encodeAsStream bool,
) ([]codec.Event, string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", fixturePath))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	if !decodeAsStream {
		resp.Header.Set("Content-Type", "application/json")
	}

	ch, err := outboundCodec.DecodeResponse(resp, decodeAsStream)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	replayCh := make(chan codec.Event, len(events))
	for _, ev := range events {
		replayCh <- ev
	}
	close(replayCh)

	rec := &flushRecorder{httptest.NewRecorder(), 0}
	if err := inboundCodec.EncodeResponse(replayCh, rec, encodeAsStream); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	return events, rec.Body.String()
}

type expectedEvent struct {
	Type         codec.EventType
	Text         string
	ToolName     string
	ToolArgs     string
	FinishReason string
	Signature    string
}

func assertEventSequence(t *testing.T, events []codec.Event, expected []expectedEvent) {
	t.Helper()
	var filtered []codec.Event
	for _, ev := range events {
		// Skip passthrough events (implementation detail, not semantic IR).
		// Also skip deprecated EventToolCallDelta — it is dual-tracked alongside
		// the new EventToolCallStart/ArgumentsDelta/End events; assertions should
		// use the new types going forward.
		if ev.Type == codec.EventRawPassthrough || ev.Type == codec.EventToolCallDelta {
			continue
		}
		filtered = append(filtered, ev)
	}
	if len(filtered) != len(expected) {
		t.Fatalf("event count: got %d, want %d\ngot types: %v", len(filtered), len(expected), eventTypes(filtered))
	}
	for i, exp := range expected {
		got := filtered[i]
		if got.Type != exp.Type {
			t.Errorf("event[%d] type: got %v, want %v", i, got.Type, exp.Type)
		}
		if exp.Text != "" && (got.Delta == nil || !strings.Contains(got.Delta.Text, exp.Text)) {
			text := ""
			if got.Delta != nil {
				text = got.Delta.Text
			}
			t.Errorf("event[%d] text: got %q, want substring %q", i, text, exp.Text)
		}
		// ToolName: check new ToolCall field first, fall back to deprecated Delta.ToolCall.
		if exp.ToolName != "" {
			gotName := ""
			if got.ToolCall != nil {
				gotName = got.ToolCall.Name
			} else if got.Delta != nil && got.Delta.ToolCall != nil {
				gotName = got.Delta.ToolCall.Name
			}
			if gotName != exp.ToolName {
				t.Errorf("event[%d] tool name: got %q, want %q", i, gotName, exp.ToolName)
			}
		}
		// ToolArgs: check new ToolCall field first, fall back to deprecated Delta.ToolCall.
		if exp.ToolArgs != "" {
			gotArgs := ""
			if got.ToolCall != nil {
				gotArgs = got.ToolCall.Arguments
			} else if got.Delta != nil && got.Delta.ToolCall != nil {
				gotArgs = got.Delta.ToolCall.Arguments
			}
			if !strings.Contains(gotArgs, exp.ToolArgs) {
				t.Errorf("event[%d] tool args: got %q, want substring %q", i, gotArgs, exp.ToolArgs)
			}
		}
		if exp.FinishReason != "" && got.FinishReason != exp.FinishReason {
			t.Errorf("event[%d] finish_reason: got %q, want %q", i, got.FinishReason, exp.FinishReason)
		}
		if exp.Signature != "" && (got.Delta == nil || got.Delta.Signature != exp.Signature) {
			t.Errorf("event[%d] signature: want %q", i, exp.Signature)
		}
	}
}

func eventTypes(events []codec.Event) []codec.EventType {
	types := make([]codec.EventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

func assertSSEFormat(t *testing.T, sseText string, protocol codec.Protocol) {
	t.Helper()
	switch protocol {
	case codec.ProtocolClaude:
		assertClaudeSSEFormat(t, sseText)
	case codec.ProtocolOpenAIChat:
		assertChatSSEFormat(t, sseText)
	case codec.ProtocolOpenAIResponses:
		assertResponsesSSEFormat(t, sseText)
	}
}

func assertClaudeSSEFormat(t *testing.T, sseText string) {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(sseText))
	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			t.Errorf("invalid JSON in Claude data line: %s", data)
			continue
		}
		typ, ok := obj["type"].(string)
		if !ok {
			t.Errorf("Claude SSE data missing 'type' field (event=%s): %s", currentEvent, data)
		}
		if currentEvent != "" && typ != currentEvent {
			t.Errorf("type mismatch: event=%q but data.type=%q", currentEvent, typ)
		}
		if typ == "message_start" {
			msg, ok := obj["message"].(map[string]any)
			if !ok {
				t.Error("message_start missing 'message' object")
			} else {
				for _, f := range []string{"id", "type", "role", "model", "content"} {
					if _, exists := msg[f]; !exists {
						t.Errorf("message_start.message missing field %q", f)
					}
				}
			}
		}
		currentEvent = ""
	}
}

func assertChatSSEFormat(t *testing.T, sseText string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(sseText), "\n")
	var lastFinishReason string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			t.Errorf("invalid JSON in Chat SSE data: %s", data)
			continue
		}
		choices, _ := obj["choices"].([]any)
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			if fr, ok := choice["finish_reason"]; ok && fr != nil {
				lastFinishReason, _ = fr.(string)
			}
		}
	}
	if lastFinishReason == "" {
		t.Error("Chat SSE: no finish_reason found in any chunk")
	}
}

func assertResponsesSSEFormat(t *testing.T, sseText string) {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(sseText))
	var hasCompleted bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event := strings.TrimPrefix(line, "event: ")
			if event == "response.completed" {
				hasCompleted = true
			}
		}
	}
	if !hasCompleted {
		t.Error("Responses SSE: missing response.completed event")
	}
}

// normalizeSSEIDs replaces generated IDs (chatcmpl-..., msg_..., resp_...)
// with deterministic placeholders so golden file comparisons are stable across runs.
func normalizeSSEIDs(s string) string {
	// Replace chatcmpl-<hex> IDs
	s = regexp.MustCompile(`chatcmpl-[0-9a-f]+`).ReplaceAllString(s, "chatcmpl-NORMALIZED")
	// Replace msg_<hex or UUID> IDs (Claude message IDs, DashScope UUIDs)
	s = regexp.MustCompile(`msg_[0-9a-f-]+`).ReplaceAllString(s, "msg_NORMALIZED")
	// Replace resp_<hex or UUID> IDs (Responses IDs, DashScope UUIDs)
	s = regexp.MustCompile(`resp_[0-9a-f-]+`).ReplaceAllString(s, "resp_NORMALIZED")
	// Replace toolu_<hex> IDs (Claude tool use IDs)
	s = regexp.MustCompile(`toolu_[0-9a-zA-Z]+`).ReplaceAllString(s, "toolu_NORMALIZED")
	// Replace call_<hex> IDs (OpenAI function call IDs)
	s = regexp.MustCompile(`call_[0-9a-zA-Z]+`).ReplaceAllString(s, "call_NORMALIZED")
	// Replace item_<hex> IDs (Responses item IDs)
	s = regexp.MustCompile(`item_[0-9a-f]+`).ReplaceAllString(s, "item_NORMALIZED")
	// Replace fc_<hex or id> IDs (function call item IDs)
	s = regexp.MustCompile(`fc_[0-9a-zA-Z_]+`).ReplaceAllString(s, "fc_NORMALIZED")
	// Normalize "created":<timestamp> values (Unix timestamps change between runs)
	s = regexp.MustCompile(`"created":\d+`).ReplaceAllString(s, `"created":0`)
	return s
}

func assertGoldenSSE(t *testing.T, got string, goldenPath string) {
	t.Helper()
	fullPath := filepath.Join("testdata", "golden", goldenPath)

	if *updateGolden {
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden file: %v", err)
		}
		t.Logf("updated golden file: %s", fullPath)
		return
	}

	expected, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v (run with -update to create)", fullPath, err)
	}
	normalizedGot := normalizeSSEIDs(got)
	normalizedExpected := normalizeSSEIDs(string(expected))
	if normalizedGot != normalizedExpected {
		t.Errorf("output differs from golden file %s\n--- got (first 500 chars) ---\n%s\n--- want (first 500 chars) ---\n%s",
			goldenPath, truncate(got, 500), truncate(string(expected), 500))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
