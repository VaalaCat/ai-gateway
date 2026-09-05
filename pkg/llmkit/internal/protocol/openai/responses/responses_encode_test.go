package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestResponsesEncodeRequestReasoningPreservesUnknownPartOrderAndOverridesStructured(t *testing.T) {
	req := &ir.Request{Messages: []ir.Message{{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
		{Type: ir.ContentTypeThinking, Reasoning: &ir.ReasoningContent{
			Summary: []string{"new one", "new two"}, Content: []string{"deep"}, Encrypted: "enc-new",
			RawJSON: json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"old one"},{"type":"future_summary","value":7},{"type":"summary_text","text":"old two"}],"content":[],"encrypted_content":"enc-old","future":"kept"}`),
		}},
		{Type: ir.ContentTypeText, Text: "answer"},
	}}}}
	body := encodeResponsesRequestBody(t, req)
	input := body["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("input = %#v", input)
	}
	reasoning := input[0].(map[string]any)
	if reasoning["encrypted_content"] != "enc-new" || reasoning["future"] != "kept" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	summary := reasoning["summary"].([]any)
	if summary[0].(map[string]any)["text"] != "new one" || summary[1].(map[string]any)["type"] != "future_summary" || summary[2].(map[string]any)["text"] != "new two" {
		t.Fatalf("summary order = %#v", summary)
	}
}

func TestResponsesEncodeRequestBadEnvelopeDropsOnlyEncrypted(t *testing.T) {
	req := &ir.Request{Messages: []ir.Message{{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{
		Type: ir.ContentTypeThinking, Reasoning: &ir.ReasoningContent{Summary: []string{"readable"}, Encrypted: "llmkit:v1:%%%"},
	}}}}}
	body := encodeResponsesRequestBody(t, req)
	reasoning := body["input"].([]any)[0].(map[string]any)
	if _, exists := reasoning["encrypted_content"]; exists {
		t.Fatalf("bad envelope leaked: %#v", reasoning)
	}
	if reasoning["summary"].([]any)[0].(map[string]any)["text"] != "readable" {
		t.Fatalf("summary lost: %#v", reasoning)
	}
}

func TestResponsesEncodeStreamReasoningHasLifecycleAndCompletedOpaque(t *testing.T) {
	raw := runEncodeStream(t, []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventReasoningContentDelta, Reasoning: &ir.ReasoningContent{Content: []string{"deep"}}},
		{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: &ir.ReasoningContent{Content: []string{"deep"}, Encrypted: "enc-complete"}},
		{Type: ir.EventDone},
	})
	events := parseSSE(raw)
	var added, delta, done bool
	for _, event := range events {
		switch event.Event {
		case "response.output_item.added":
			var frame respStreamEvent
			_ = json.Unmarshal([]byte(event.Data), &frame)
			added = frame.Item != nil && frame.Item.Type == "reasoning"
		case "response.reasoning_text.delta":
			delta = strings.Contains(event.Data, "deep")
		case "response.output_item.done":
			var frame respStreamEvent
			_ = json.Unmarshal([]byte(event.Data), &frame)
			done = frame.Item != nil && frame.Item.Type == "reasoning" && frame.Item.EncryptedContent == "enc-complete"
		}
	}
	if !added || !delta || !done {
		t.Fatalf("reasoning lifecycle added=%v delta=%v done=%v\n%s", added, delta, done, raw)
	}
}

func TestResponsesEncodeNonStreamReasoningDonePreservesUnknownRawFields(t *testing.T) {
	events := make(chan ir.Event, 1)
	events <- ir.Event{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: &ir.ReasoningContent{
		Summary: []string{"new"}, Encrypted: "enc", RawJSON: json.RawMessage(`{"type":"reasoning","summary":[{"type":"summary_text","text":"old"},{"type":"future_part","value":1}],"encrypted_content":"old","future":{"x":1}}`),
	}}
	close(events)
	recorder := httptest.NewRecorder()
	if err := (&handler{}).encodeHTTPResponse(events, recorder, false); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0]["future"] == nil || response.Output[0]["encrypted_content"] != "enc" {
		t.Fatalf("output = %#v", response.Output)
	}
	summary := response.Output[0]["summary"].([]any)
	if len(summary) != 2 || summary[1].(map[string]any)["type"] != "future_part" {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestResponsesReasoningSameProtocolRoundTripKeepsItemIdentity(t *testing.T) {
	decoded := collectResponsesStreamEvents(t, `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_original","type":"reasoning","status":"in_progress"}}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","item_id":"rs_original","output_index":0,"delta":"deep"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"rs_original","type":"reasoning","status":"completed","content":[{"type":"reasoning_text","text":"deep"}],"encrypted_content":"enc"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed"}}
`)
	raw := runEncodeStream(t, decoded)
	var addedID, deltaID, doneID string
	for _, event := range parseSSE(raw) {
		var frame respStreamEvent
		_ = json.Unmarshal([]byte(event.Data), &frame)
		switch event.Event {
		case "response.output_item.added":
			if frame.Item != nil && frame.Item.Type == "reasoning" {
				addedID = frame.Item.ID
			}
		case "response.reasoning_text.delta":
			deltaID = frame.ItemID
		case "response.output_item.done":
			if frame.Item != nil && frame.Item.Type == "reasoning" {
				doneID = frame.Item.ID
			}
		}
	}
	if addedID != "rs_original" || deltaID != addedID || doneID != addedID {
		t.Fatalf("reasoning identity added=%q delta=%q done=%q\n%s", addedID, deltaID, doneID, raw)
	}
}

func TestResponsesEncodeStreamReasoningKeepsMessageOutputIndexConsistent(t *testing.T) {
	raw := runEncodeStream(t, []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventReasoningContentDelta, Reasoning: &ir.ReasoningContent{Content: []string{"deep"}}},
		{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: &ir.ReasoningContent{Content: []string{"deep"}}},
		{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "answer"}},
		{Type: ir.EventDone},
	})
	messageIndex := -1
	for _, event := range parseSSE(raw) {
		var frame respStreamEvent
		_ = json.Unmarshal([]byte(event.Data), &frame)
		if event.Event == "response.output_item.added" && frame.Item != nil && frame.Item.Type == "message" {
			messageIndex = *frame.OutputIndex
			continue
		}
		if messageIndex >= 0 && (event.Event == "response.content_part.added" || event.Event == "response.output_text.delta" || event.Event == "response.output_text.done" || event.Event == "response.content_part.done") {
			if frame.OutputIndex == nil || *frame.OutputIndex != messageIndex {
				t.Fatalf("%s output_index=%v, want %d\n%s", event.Event, frame.OutputIndex, messageIndex, raw)
			}
		}
	}
	if messageIndex != 1 {
		t.Fatalf("message output index = %d, want 1\n%s", messageIndex, raw)
	}
}

func encodeResponsesRequestBody(t *testing.T, req *ir.Request) map[string]any {
	t.Helper()
	httpReq, err := (&handler{}).encodeHTTPRequest(req, &channelConfig{BaseURL: "https://api.openai.com", Model: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	bodyBytes, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

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

// runEncodeStream drives handler.EncodeResponse in stream mode with the
// given events and returns the raw SSE bytes.
func runEncodeStream(t *testing.T, events []ir.Event) []byte {
	t.Helper()
	ch := make(chan ir.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)

	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}
	c := &handler{}
	if err := c.encodeHTTPResponse(ch, w, true); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	return rec.Body.Bytes()
}

func TestResponsesEncodeStream_CodexWireSchema(t *testing.T) {
	raw := runEncodeStream(t, []ir.Event{
		{Type: ir.EventStreamStart, Model: "glm-5.2"},
		{Type: ir.EventThinkingDelta, Delta: &ir.DeltaPayload{ContentType: ir.ContentTypeThinking, Text: "think"}},
		{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{ContentType: ir.ContentTypeText, Text: "answer"}},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "call_1", Name: "exec"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "call_1", Arguments: `{"cmd":"pwd"}`}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "call_1", Arguments: `{"cmd":"pwd"}`}},
		{Type: ir.EventDone},
	})

	events := parseSSE(raw)
	find := func(eventName string) map[string]any {
		t.Helper()
		for _, event := range events {
			if event.Event != eventName {
				continue
			}
			var data map[string]any
			if err := json.Unmarshal([]byte(event.Data), &data); err != nil {
				t.Fatalf("decode %s: %v", eventName, err)
			}
			return data
		}
		t.Fatalf("missing %s", eventName)
		return nil
	}

	for _, eventName := range []string{
		"response.reasoning_text.delta",
		"response.output_text.delta",
		"response.function_call_arguments.delta",
	} {
		data := find(eventName)
		if _, ok := data["delta"].(string); !ok {
			t.Errorf("%s delta type = %T, want string", eventName, data["delta"])
		}
	}
	summaryRaw := runEncodeStream(t, []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventReasoningSummaryDelta, Reasoning: &ir.ReasoningContent{Summary: []string{"summary"}}},
		{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: &ir.ReasoningContent{Summary: []string{"summary"}}},
		{Type: ir.EventDone},
	})
	for _, event := range parseSSE(summaryRaw) {
		if event.Event != "response.reasoning_summary_text.delta" {
			continue
		}
		var data map[string]any
		_ = json.Unmarshal([]byte(event.Data), &data)
		if data["summary_index"] != float64(0) {
			t.Errorf("reasoning summary delta summary_index = %#v, want 0", data["summary_index"])
		}
		if _, exists := data["content_index"]; exists {
			t.Errorf("reasoning summary delta must not contain content_index: %#v", data)
		}
	}
	for _, eventName := range []string{"response.created", "response.in_progress", "response.completed"} {
		data := find(eventName)
		response, _ := data["response"].(map[string]any)
		if response["object"] != "response" {
			t.Errorf("%s response.object = %#v, want response", eventName, response["object"])
		}
	}
	textDone := find("response.output_text.done")
	var messageID string
	for _, event := range events {
		if event.Event != "response.output_item.added" {
			continue
		}
		var data struct {
			Item respOutputItem `json:"item"`
		}
		_ = json.Unmarshal([]byte(event.Data), &data)
		if data.Item.Type == "message" {
			messageID = data.Item.ID
			break
		}
	}
	if !strings.HasPrefix(messageID, "msg_") {
		t.Errorf("message item id = %q, want msg_ prefix", messageID)
	}
	if textDone["text"] != "answer" {
		t.Errorf("output_text.done text = %#v, want answer", textDone["text"])
	}
	if _, exists := textDone["delta"]; exists {
		t.Errorf("output_text.done must not contain delta: %#v", textDone)
	}

	wantReasoningLifecycle := []string{
		"response.content_part.added",
		"response.reasoning_text.delta",
		"response.reasoning_text.done",
		"response.content_part.done",
		"response.output_item.done",
	}
	var reasoningLifecycle []string
	for _, event := range events {
		var data map[string]any
		_ = json.Unmarshal([]byte(event.Data), &data)
		if data["item_id"] == find("response.reasoning_text.delta")["item_id"] || event.Event == "response.output_item.done" {
			if event.Event == "response.output_item.done" {
				item, _ := data["item"].(map[string]any)
				if item["type"] != "reasoning" {
					continue
				}
			}
			reasoningLifecycle = append(reasoningLifecycle, event.Event)
		}
	}
	if !reflect.DeepEqual(reasoningLifecycle, wantReasoningLifecycle) {
		t.Errorf("reasoning lifecycle = %#v, want %#v", reasoningLifecycle, wantReasoningLifecycle)
	}

	completed := find("response.completed")
	response, _ := completed["response"].(map[string]any)
	output, _ := response["output"].([]any)
	if len(output) != 3 {
		t.Fatalf("response.completed output = %#v, want reasoning + message + function_call", response["output"])
	}
	message, _ := output[1].(map[string]any)
	content, _ := message["content"].([]any)
	outputText, _ := content[0].(map[string]any)
	if annotations, ok := outputText["annotations"].([]any); !ok || len(annotations) != 0 {
		t.Errorf("completed output_text annotations = %#v, want []", outputText["annotations"])
	}
	wantTypes := []string{"reasoning", "message", "function_call"}
	for index, wantType := range wantTypes {
		item, _ := output[index].(map[string]any)
		if item["type"] != wantType {
			t.Errorf("response.completed output[%d].type = %#v, want %s", index, item["type"], wantType)
		}
	}
}

func TestResponsesEncodeStream_DoneClosesOpenToolCall(t *testing.T) {
	raw := runEncodeStream(t, []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "call_open", Name: "exec"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "call_open", Arguments: `{"cmd":"pwd"}`}},
		{Type: ir.EventDone},
	})
	events := parseSSE(raw)
	var completed map[string]any
	for _, event := range events {
		if event.Event == "response.completed" {
			_ = json.Unmarshal([]byte(event.Data), &completed)
		}
	}
	response, _ := completed["response"].(map[string]any)
	output, _ := response["output"].([]any)
	if len(output) != 1 || output[0] == nil {
		t.Fatalf("response.completed output = %#v, want completed function_call", response["output"])
	}
	item, _ := output[0].(map[string]any)
	if item["type"] != "function_call" || item["status"] != "completed" || item["arguments"] != `{"cmd":"pwd"}` {
		t.Errorf("completed function_call = %#v", item)
	}
}

// ---------------------------------------------------------------------------
// New Task 4 tests — must be RED before encodeStream refactor
// ---------------------------------------------------------------------------

func TestResponsesEncodeStream_ToolCallCorrectShape(t *testing.T) {
	events := []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "call_x", Index: 0, Name: "exec"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "call_x", Arguments: `{"a"`}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "call_x", Arguments: `:1}`}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "call_x", Arguments: `{"a":1}`}},
		{Type: ir.EventDone},
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
				// Bug B guard: id must not be the bare "fc_" with empty suffix.
				var p struct {
					Item struct {
						ID string `json:"id"`
					} `json:"item"`
				}
				if err := json.Unmarshal([]byte(e.Data), &p); err == nil && p.Item.ID == "fc_" {
					t.Errorf("output_item.added id must not be bare 'fc_', got: %s", e.Data)
				}
			}
		case "response.function_call_arguments.delta":
			var p struct {
				Arguments string `json:"arguments"`
				Delta     string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
				t.Fatalf("unmarshal args.delta: %v", err)
			}
			if p.Delta != "" {
				argDeltas = append(argDeltas, p.Delta)
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
	events := []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: ""}}, // empty text — must be suppressed
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c1", Name: "f"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: ir.EventDone},
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
	events := []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "hello"}},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c1", Name: "f"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: ir.EventDone},
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
	ch := make(chan ir.Event, 10)
	ch <- ir.Event{Type: ir.EventStreamStart, Model: "gpt-4o"}
	ch <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "Hello!",
		},
	}
	ch <- ir.Event{
		Type: ir.EventUsage,
		Usage: &ir.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	ch <- ir.Event{Type: ir.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(ch, w, false)
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
	ch := make(chan ir.Event, 10)
	ch <- ir.Event{Type: ir.EventStreamStart, Model: "gpt-4o"}
	ch <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "ok",
		},
	}
	ch <- ir.Event{
		Type: ir.EventUsage,
		Usage: &ir.Usage{
			PromptTokens:     100,
			CompletionTokens: 10,
			TotalTokens:      110,
			CachedTokens:     80,
		},
	}
	ch <- ir.Event{Type: ir.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(ch, w, false)
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
	ch := make(chan ir.Event, 10)
	ch <- ir.Event{Type: ir.EventStreamStart, Model: "gpt-4o"}
	ch <- ir.Event{
		Type: ir.EventContentDelta,
		Delta: &ir.DeltaPayload{
			ContentType: ir.ContentTypeText,
			Text:        "ok",
		},
	}
	ch <- ir.Event{
		Type: ir.EventUsage,
		Usage: &ir.Usage{
			PromptTokens:     50,
			CompletionTokens: 5,
			TotalTokens:      55,
			CachedTokens:     0,
		},
	}
	ch <- ir.Event{Type: ir.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &handler{}
	err := c.encodeHTTPResponse(ch, w, false)
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
	req := &ir.Request{
		Model:           "gpt-5",
		Messages:        []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "hi"}}}},
		InboundProtocol: ir.ProtocolOpenAIResponses,
		Tools: []ir.Tool{
			{Type: "function", Name: "f", InputSchema: map[string]any{"type": "object"}},
			{Type: "web_search", RawConfig: rawBuiltin},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	c := &handler{}
	httpReq, err := c.encodeHTTPRequest(req, cfg)
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
	req := &ir.Request{
		Model:           "gpt-5",
		Messages:        []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "hi"}}}},
		InboundProtocol: ir.ProtocolOpenAIChat,
		Tools:           []ir.Tool{{Type: "web_search", RawConfig: rawBuiltin}},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	c := &handler{}
	httpReq, err := c.encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	if bytes.Contains(body, []byte(`"web_search"`)) {
		t.Errorf("expected web_search dropped, but found in body: %s", body)
	}
}

func TestEncodeFunctionFallbackInputItem(t *testing.T) {
	t.Run("custom tool call", func(t *testing.T) {
		got := encodeFunctionFallbackInputItem(json.RawMessage(
			`{"type":"custom_tool_call","id":"ctc_1","call_id":"call_patch","name":"apply_patch","input":"*** Begin Patch"}`,
		))
		var item struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if err := json.Unmarshal(got, &item); err != nil {
			t.Fatalf("decode converted item: %v", err)
		}
		if item.Type != "function_call" || item.ID != "ctc_1" || item.CallID != "call_patch" || item.Name != "apply_patch" {
			t.Fatalf("converted item = %#v", item)
		}
		var arguments map[string]string
		if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
			t.Fatalf("decode function arguments: %v", err)
		}
		if arguments["input"] != "*** Begin Patch" {
			t.Fatalf("arguments = %#v, want original custom input", arguments)
		}
	})

	t.Run("custom tool call output", func(t *testing.T) {
		got := encodeFunctionFallbackInputItem(json.RawMessage(
			`{"type":"custom_tool_call_output","call_id":"call_patch","output":"Done"}`,
		))
		var item map[string]any
		if err := json.Unmarshal(got, &item); err != nil {
			t.Fatalf("decode converted item: %v", err)
		}
		want := map[string]any{
			"type":    "function_call_output",
			"call_id": "call_patch",
			"output":  "Done",
		}
		if !reflect.DeepEqual(item, want) {
			t.Fatalf("converted item = %#v, want %#v", item, want)
		}
	})

	t.Run("custom tool call output text content", func(t *testing.T) {
		got := encodeFunctionFallbackInputItem(json.RawMessage(
			`{"type":"custom_tool_call_output","call_id":"call_patch","output":[{"type":"input_text","text":"first"},{"type":"input_text","text":" second"}]}`,
		))
		var item map[string]any
		if err := json.Unmarshal(got, &item); err != nil {
			t.Fatalf("decode converted item: %v", err)
		}
		if item["output"] != "first second" {
			t.Fatalf("converted output = %#v, want concatenated text", item["output"])
		}
	})

	t.Run("custom tool call output structured content", func(t *testing.T) {
		got := encodeFunctionFallbackInputItem(json.RawMessage(
			`{"type":"custom_tool_call_output","call_id":"call_patch","output":{"ok":true,"count":2}}`,
		))
		var item map[string]any
		if err := json.Unmarshal(got, &item); err != nil {
			t.Fatalf("decode converted item: %v", err)
		}
		if item["output"] != `{"count":2,"ok":true}` {
			t.Fatalf("converted output = %#v, want JSON string", item["output"])
		}
	})

	t.Run("custom tool call output preserves large integer", func(t *testing.T) {
		got := encodeFunctionFallbackInputItem(json.RawMessage(
			`{"type":"custom_tool_call_output","call_id":"call_patch","output":{"id":9007199254740993}}`,
		))
		var item map[string]any
		if err := json.Unmarshal(got, &item); err != nil {
			t.Fatalf("decode converted item: %v", err)
		}
		if item["output"] != `{"id":9007199254740993}` {
			t.Fatalf("converted output = %#v, want exact large integer", item["output"])
		}
	})

	t.Run("non custom item", func(t *testing.T) {
		raw := json.RawMessage(`{"type":"computer_call","id":"computer_1"}`)
		if got := encodeFunctionFallbackInputItem(raw); !bytes.Equal(got, raw) {
			t.Fatalf("non-custom item changed: got %s, want %s", got, raw)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		raw := json.RawMessage(`{"type":`)
		if got := encodeFunctionFallbackInputItem(raw); !bytes.Equal(got, raw) {
			t.Fatalf("malformed item changed: got %s, want %s", got, raw)
		}
	})
}

func TestResponsesFunctionFallbackConvertsCustomToolChoice(t *testing.T) {
	inbound := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{
		"model":"gpt-5.5",
		"input":"edit",
		"tool_choice":{"type":"custom","name":"apply_patch"},
		"tools":[{"type":"custom","name":"apply_patch","description":"Edit files","format":{"type":"grammar"}}]
	}`))
	req, err := (&handler{}).decodeHTTPRequest(inbound)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	outbound, err := (&handler{}).encodeHTTPRequest(req, &channelConfig{
		BaseURL:             "http://stub",
		APIKey:              "k",
		Model:               "glm-5.2",
		BuiltinToolFallback: "function",
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	body, err := io.ReadAll(outbound.Body)
	if err != nil {
		t.Fatalf("read outbound body: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode outbound body: %v", err)
	}
	choice, ok := raw["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "function" || choice["name"] != "apply_patch" {
		t.Fatalf("tool_choice = %#v, want function/apply_patch", raw["tool_choice"])
	}
}

func TestResponsesPreservesCustomToolChoiceWithoutFunctionFallback(t *testing.T) {
	req := &ir.Request{
		InboundProtocol: ir.ProtocolOpenAIResponses,
		ToolChoice:      &ir.ToolChoice{Type: "custom", Name: "apply_patch"},
		Messages:        []ir.Message{ir.TextMessage(ir.RoleUser, "edit")},
		Tools: []ir.Tool{{
			Type:        "custom",
			Name:        "apply_patch",
			Description: "Edit files",
			RawConfig: map[string]any{
				"type": "custom",
				"name": "apply_patch",
			},
		}},
	}
	outbound, err := (&handler{}).encodeHTTPRequest(req, &channelConfig{
		BaseURL: "http://stub",
		APIKey:  "k",
		Model:   "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	body, err := io.ReadAll(outbound.Body)
	if err != nil {
		t.Fatalf("read outbound body: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode outbound body: %v", err)
	}
	choice, ok := raw["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "custom" || choice["name"] != "apply_patch" {
		t.Fatalf("tool_choice = %#v, want custom/apply_patch", raw["tool_choice"])
	}
}

// ---------------------------------------------------------------------------
// TestResponsesEncodeRejectsEmptyNameFunctionTool — missing name validation
// ---------------------------------------------------------------------------

func TestResponsesEncodeRejectsEmptyNameFunctionTool(t *testing.T) {
	req := &ir.Request{
		Model:           "gpt-5",
		Messages:        []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "x"}}}},
		InboundProtocol: ir.ProtocolOpenAIResponses,
		Tools:           []ir.Tool{{Type: "function", Name: ""}},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	c := &handler{}
	_, err := c.encodeHTTPRequest(req, cfg)
	if !errors.Is(err, convert.ErrFunctionToolMissingName) {
		t.Errorf("want ErrFunctionToolMissingName, got %v", err)
	}
}

func TestResponsesEncodeRequest_DropsEmptyTextBlock(t *testing.T) {
	req := &ir.Request{
		Model: "gpt-5",
		Messages: []ir.Message{
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: ""},
				{Type: ir.ContentTypeText, Text: "Hello! I see you're working on..."},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	httpReq, err := (&handler{}).encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	if bytes.Contains(body, []byte(`{"type":"input_text"}`)) {
		t.Errorf("body contains illegal empty input_text block: %s", body)
	}
	if !bytes.Contains(body, []byte("Hello! I see you're working on...")) {
		t.Errorf("real text missing: %s", body)
	}
}

func TestResponsesEncodeRequest_AllEmptyTextBecomesEmptyString(t *testing.T) {
	req := &ir.Request{
		Model: "gpt-5",
		Messages: []ir.Message{
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: ""},
				{Type: ir.ContentTypeText, Text: ""},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	httpReq, err := (&handler{}).encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	item := raw["input"].([]any)[0].(map[string]any)
	if item["content"] != "" {
		t.Errorf("content = %#v, want empty string", item["content"])
	}
}

func TestResponsesEncodeRequest_TwoRealTextBlocksUnchanged(t *testing.T) {
	req := &ir.Request{
		Model: "gpt-5",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: "first"},
				{Type: ir.ContentTypeText, Text: "second"},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	httpReq, err := (&handler{}).encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(body, &raw)
	item := raw["input"].([]any)[0].(map[string]any)
	content := item["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}
}

func TestResponsesEncodeRequest_EmitsImageBlock_Base64(t *testing.T) {
	req := &ir.Request{
		Model: "gpt-5",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: "look"},
				{Type: ir.ContentTypeImage, MediaB64: "abc123", MimeType: "image/png"},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	httpReq, err := (&handler{}).encodeHTTPRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	content := raw["input"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2 (text + image)", len(content))
	}
	img := content[1].(map[string]any)
	if img["type"] != "input_image" {
		t.Errorf("block[1] type = %v, want input_image", img["type"])
	}
	if img["image_url"] != "data:image/png;base64,abc123" {
		t.Errorf("image_url = %v, want data URI", img["image_url"])
	}
}

func TestResponsesEncodeRequest_EmitsImageBlock_URLFallback(t *testing.T) {
	req := &ir.Request{
		Model: "gpt-5",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: "look"},
				{Type: ir.ContentTypeImage, MediaURL: "https://example.com/x.png"},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	httpReq, _ := (&handler{}).encodeHTTPRequest(req, cfg)
	body, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(body, &raw)
	content := raw["input"].([]any)[0].(map[string]any)["content"].([]any)
	img := content[1].(map[string]any)
	if img["type"] != "input_image" || img["image_url"] != "https://example.com/x.png" {
		t.Errorf("got %#v, want input_image with the raw URL", img)
	}
}

func TestResponsesEncodeRequest_TextOnlyUnchanged(t *testing.T) {
	req := &ir.Request{
		Model: "gpt-5",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: "a"},
				{Type: ir.ContentTypeText, Text: "b"},
			}},
		},
	}
	cfg := &channelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-5"}
	httpReq, _ := (&handler{}).encodeHTTPRequest(req, cfg)
	body, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(body, &raw)
	content := raw["input"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2 text blocks", len(content))
	}
	if content[0].(map[string]any)["type"] != "input_text" {
		t.Errorf("text block type wrong: %#v", content[0])
	}
}
