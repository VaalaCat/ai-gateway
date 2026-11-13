package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol/gemini"
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

func TestResponsesEncodeRequestReasoningDropsOutputStatus(t *testing.T) {
	req := &ir.Request{Messages: []ir.Message{{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{
		Type: ir.ContentTypeThinking, Reasoning: &ir.ReasoningContent{
			Summary: []string{"plan"}, Encrypted: "enc",
			RawJSON: json.RawMessage(`{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"enc","future":"kept"}`),
		},
	}}}}}
	body := encodeResponsesRequestBody(t, req)
	reasoning := body["input"].([]any)[0].(map[string]any)
	if _, exists := reasoning["status"]; exists {
		t.Fatalf("output-only status leaked into input: %#v", reasoning)
	}
	if reasoning["id"] != "rs_1" || reasoning["encrypted_content"] != "enc" || reasoning["future"] != "kept" {
		t.Fatalf("reasoning fields lost: %#v", reasoning)
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

// 原样保留的工具结果 item 写出时 call_id 以 IR 的 ToolCallID 为准，其他字段不变。
func TestResponsesEncodeToolResultRawJSONUsesIRCallID(t *testing.T) {
	const imageOutput = `[{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":{"n":9007199254740993}}]`
	tests := []struct {
		name       string
		message    ir.Message
		wantCallID string
		wantRaw    string // 非空时要求 item 原样写出
	}{
		{
			name: "packed call_id replaced by IR id",
			message: ir.Message{Role: ir.RoleTool, ToolCallID: "call_1",
				RawJSON: json.RawMessage(`{"type":"function_call_output","call_id":"call_llmkit_v1_abc","output":` + imageOutput + `}`)},
			wantCallID: "call_1",
		},
		{
			name: "same id kept as is",
			message: ir.Message{Role: ir.RoleTool, ToolCallID: "call_1",
				RawJSON: json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":` + imageOutput + `}`)},
			wantCallID: "call_1",
			wantRaw:    `{"type":"function_call_output","call_id":"call_1","output":` + imageOutput + `}`,
		},
		{
			name:       "raw item that is not a tool result untouched",
			message:    ir.Message{RawJSON: json.RawMessage(`{"type":"custom_tool_call_output","call_id":"call_custom","output":"ok"}`)},
			wantCallID: "call_custom",
			wantRaw:    `{"type":"custom_tool_call_output","call_id":"call_custom","output":"ok"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := (&handler{}).encodeHTTPRequest(&ir.Request{Messages: []ir.Message{tt.message}}, &channelConfig{BaseURL: "https://api.openai.com", Model: "gpt"})
			if err != nil {
				t.Fatal(err)
			}
			bodyBytes, err := io.ReadAll(httpReq.Body)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Input []json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(bodyBytes, &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Input) != 1 {
				t.Fatalf("input = %s", bodyBytes)
			}
			var item struct {
				CallID string          `json:"call_id"`
				Output json.RawMessage `json:"output"`
			}
			if err := json.Unmarshal(body.Input[0], &item); err != nil {
				t.Fatal(err)
			}
			if item.CallID != tt.wantCallID {
				t.Errorf("call_id = %q, want %q", item.CallID, tt.wantCallID)
			}
			if tt.wantRaw != "" && string(body.Input[0]) != tt.wantRaw {
				t.Errorf("item = %s, want unchanged %s", body.Input[0], tt.wantRaw)
			}
			if tt.message.Role == ir.RoleTool && !strings.Contains(string(item.Output), "9007199254740993") {
				t.Errorf("output lost precision: %s", item.Output)
			}
		})
	}
}

func TestResponsesEncodeRequestTruncatesToolCallIDs(t *testing.T) {
	// 1. 覆盖普通 ID、长度边界、实际 Grok ID 和待转发的签名包。
	const grokID = "call-ff0d9a76-533e-4280-9d31-394ef8a9044d-24__fc_91105360-b333-9abb-9d7b-4dbf05945257_0"
	packedID := convert.PackToolCallID("call_original", &ir.ToolCallProviderData{
		Protocol: ir.ProtocolOpenAIChat,
		Models:   []string{"inner-model"},
		Fields:   map[string]json.RawMessage{"signature": json.RawMessage(`"signature-data"`)},
	})
	for _, tt := range []struct{ name, id, want string }{
		{"short", "call_short", "call_short"},
		{"64", strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"65", strings.Repeat("a", 65), strings.Repeat("a", 64)},
		{"grok", grokID, grokID[:64]},
		{"unicode", strings.Repeat("界", 65), strings.Repeat("界", 64)},
		{"packed_for_next_gateway", packedID, packedID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, rawResult := range []bool{false, true} {
				result := ir.Message{Role: ir.RoleTool, ToolCallID: tt.id, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "done"}}}
				if rawResult {
					encodedID, _ := json.Marshal(tt.id)
					result.RawJSON = json.RawMessage(`{"type":"function_call_output","call_id":` + string(encodedID) + `,"output":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}],"future":9007199254740993}`)
				}
				req := &ir.Request{Messages: []ir.Message{
					{Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{{ID: tt.id, Name: "read_file", Arguments: `{}`}}},
					result,
				}}
				before, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				// 2. 调用与结果写出相同短 ID，图片和原始字段保留，输入历史不变。
				httpReq, err := (&handler{}).encodeHTTPRequest(req, &channelConfig{BaseURL: "https://api.openai.com", Model: "gpt"})
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(httpReq.Body)
				httpReq.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				var encoded struct {
					Input []struct {
						CallID string `json:"call_id"`
					} `json:"input"`
				}
				if err := json.Unmarshal(body, &encoded); err != nil {
					t.Fatal(err)
				}
				if len(encoded.Input) != 2 || encoded.Input[0].CallID != tt.want || encoded.Input[1].CallID != tt.want {
					t.Fatalf("rawResult=%v input=%s, want call_id=%q", rawResult, body, tt.want)
				}
				if rawResult && (!bytes.Contains(body, []byte("9007199254740993")) || !bytes.Contains(body, []byte("data:image/png;base64,AAAA"))) {
					t.Fatalf("structured output changed: %s", body)
				}
				after, _ := json.Marshal(req)
				if !bytes.Equal(before, after) {
					t.Fatal("request history mutated")
				}
			}
		})
	}
}

func TestResponsesEncodeRequestTruncatesRawToolCallIDs(t *testing.T) {
	// 1. 原样工具调用和结果按同一规则截断，其他 item 的字段不变。
	id := strings.Repeat("c", 87)
	for _, itemType := range []string{"function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "future_item"} {
		t.Run(itemType, func(t *testing.T) {
			raw := json.RawMessage(`{"type":"` + itemType + `","call_id":"` + id + `","name":"run","input":"pwd","output":"done"}`)
			req := &ir.Request{Messages: []ir.Message{{RawJSON: raw}}}
			body := encodeResponsesRequestBody(t, req)
			item := body["input"].([]any)[0].(map[string]any)
			want := id[:64]
			if itemType == "future_item" {
				want = id
			}
			if item["call_id"] != want || item["input"] != "pwd" || item["output"] != "done" {
				t.Fatalf("item=%#v", item)
			}
			if !bytes.Equal(req.Messages[0].RawJSON, raw) {
				t.Fatal("raw history mutated")
			}
		})
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

func TestResponsesEncodeRequest_AssistantMultiTextPartsUseOutputText(t *testing.T) {
	// Assistant history turns are prior model output: OpenAI-compatible upstreams
	// (new-api relays included) reject assistant message items whose text parts
	// are encoded as input_text. Multi-block assistant content must use
	// output_text, while user content keeps input_text.
	req := &ir.Request{
		Model: "gpt-5",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "hi"}}},
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: "first"},
				{Type: ir.ContentTypeText, Text: "second"},
			}},
			{Role: ir.RoleUser, Content: []ir.ContentBlock{
				{Type: ir.ContentTypeText, Text: "a"},
				{Type: ir.ContentTypeText, Text: "b"},
			}},
		},
	}
	body := encodeResponsesRequestBody(t, req)
	input := body["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input = %#v", input)
	}
	assistantParts := input[1].(map[string]any)["content"].([]any)
	if len(assistantParts) != 2 {
		t.Fatalf("assistant parts = %#v", assistantParts)
	}
	for i, p := range assistantParts {
		if p.(map[string]any)["type"] != "output_text" || p.(map[string]any)["text"] == "" {
			t.Errorf("assistant part %d = %#v, want output_text", i, p)
		}
	}
	userParts := input[2].(map[string]any)["content"].([]any)
	for i, p := range userParts {
		if p.(map[string]any)["type"] != "input_text" {
			t.Errorf("user part %d = %#v, want input_text", i, p)
		}
	}
}

func TestResponsesEncodeRequest_ReasoningConfig(t *testing.T) {
	tests := []struct {
		name          string
		effort        string
		context       ir.ReasoningContext
		summary       any
		wantReasoning map[string]any // nil 表示请求体不应出现 reasoning
	}{
		{name: "only_effort", effort: "high", wantReasoning: map[string]any{"effort": "high"}},
		{name: "only_context", context: ir.ReasoningContextAllTurns, wantReasoning: map[string]any{"context": "all_turns"}},
		{name: "only_context_auto", context: "auto", wantReasoning: map[string]any{"context": "auto"}},
		{name: "effort_and_context", effort: "high", context: ir.ReasoningContextCurrentTurn, wantReasoning: map[string]any{"effort": "high", "context": "current_turn"}},
		{name: "neither"},
		{name: "effort_with_summary", effort: "high", summary: "auto", wantReasoning: map[string]any{"effort": "high", "summary": "auto"}},
		{name: "context_with_summary", context: ir.ReasoningContextAllTurns, summary: "detailed", wantReasoning: map[string]any{"context": "all_turns", "summary": "detailed"}},
		{name: "effort_context_and_summary", effort: "low", context: ir.ReasoningContextCurrentTurn, summary: "auto", wantReasoning: map[string]any{"effort": "low", "context": "current_turn", "summary": "auto"}},
		{name: "only_summary", summary: "auto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ir.Request{
				Messages:         []ir.Message{ir.TextMessage(ir.RoleUser, "hi")},
				ReasoningEffort:  tt.effort,
				ReasoningContext: tt.context,
			}
			if tt.summary != nil {
				req.Extras = map[string]any{"reasoning_summary": tt.summary}
			}
			body := encodeResponsesRequestBody(t, req)
			got, exists := body["reasoning"]
			if tt.wantReasoning == nil {
				if exists {
					t.Fatalf("reasoning = %#v, want absent", got)
				}
				return
			}
			if !reflect.DeepEqual(got, any(tt.wantReasoning)) {
				t.Fatalf("reasoning = %#v, want %#v", got, tt.wantReasoning)
			}
		})
	}
}

// TestResponsesImagesRoundTrip 覆盖图文混合图片响应及再次作为输入提交。
func TestResponsesImagesRoundTrip(t *testing.T) {
	// 1. 两张不同格式、不同签名的图片与文本交错到达。
	first := &ir.ContentBlock{Type: ir.ContentTypeImage, MimeType: "image/webp", MediaB64: "Zmlyc3Q=", ProviderData: &ir.ProviderData{Protocol: ir.ProtocolGemini, Models: []string{"image-model"}, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"sig-first"`)}}}
	second := &ir.ContentBlock{Type: ir.ContentTypeImage, MimeType: "image/jpeg", MediaB64: "c2Vjb25k", ProviderData: &ir.ProviderData{Protocol: ir.ProtocolGemini, Models: []string{"image-model"}, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"sig-second"`)}}}
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			events := []ir.Event{
				{Type: ir.EventStreamStart, Model: "image-model"},
				{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "第一段"}},
				{Type: ir.EventContentBlock, ContentBlock: first},
				{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "第二段"}},
				{Type: ir.EventContentBlock, ContentBlock: second},
				{Type: ir.EventUsage, Usage: &ir.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}},
				{Type: ir.EventDone},
			}
			ch := make(chan ir.Event, len(events))
			for _, event := range events {
				ch <- event
			}
			close(ch)
			w := httptest.NewRecorder()
			if err := (&handler{}).encodeHTTPResponse(ch, w, stream); err != nil {
				t.Fatal(err)
			}
			var output []respOutputItem
			if stream {
				added := map[int]respOutputItem{}
				done := map[int]respOutputItem{}
				sequence := -1
				for _, frame := range parseSSE(w.Body.Bytes()) {
					if strings.Contains(frame.Event, "partial_image") {
						t.Fatal("fabricated partial image event")
					}
					var wire struct {
						respStreamEvent
						Delta json.RawMessage `json:"delta"`
					}
					if err := json.Unmarshal([]byte(frame.Data), &wire); err != nil {
						t.Fatal(err)
					}
					if wire.SequenceNumber <= sequence {
						t.Fatalf("non-increasing sequence: %s", frame.Data)
					}
					sequence = wire.SequenceNumber
					switch frame.Event {
					case "response.output_item.added":
						if _, ok := added[*wire.OutputIndex]; ok {
							t.Fatalf("duplicate added: %s", frame.Data)
						}
						added[*wire.OutputIndex] = *wire.Item
					case "response.output_item.done":
						if _, ok := done[*wire.OutputIndex]; ok {
							t.Fatalf("duplicate done: %s", frame.Data)
						}
						done[*wire.OutputIndex] = *wire.Item
					case "response.completed":
						output = wire.Response.Output
					}
				}
				if len(added) != 4 || len(done) != 4 {
					t.Fatalf("item lifecycle: added=%#v done=%#v", added, done)
				}
				for i := range output {
					if added[i].ID != done[i].ID || done[i].ID != output[i].ID {
						t.Fatalf("unstable item ID at %d", i)
					}
					if output[i].Type == "image_generation_call" && (added[i].Result != "" || done[i].Status != "completed") {
						t.Fatalf("image lifecycle: %#v / %#v", added[i], done[i])
					}
				}
			} else {
				var response respResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				output = response.Output
			}
			if len(output) != 4 {
				t.Fatalf("output = %#v", output)
			}
			if output[0].Type != "message" || output[0].Content[0].Text != "第一段" || output[1].Type != "image_generation_call" || output[2].Content[0].Text != "第二段" || output[3].Type != "image_generation_call" {
				t.Fatalf("lost image/text order: %#v", output)
			}
			for i, image := range []*ir.ContentBlock{first, second} {
				item := output[i*2+1]
				if item.Result != image.MediaB64 || item.OutputFormat != strings.TrimPrefix(image.MimeType, "image/") || !reflect.DeepEqual(item.ProviderData, image.ProviderData) {
					t.Fatalf("image changed: %#v", item)
				}
			}

			// 2. 客户端原样带回输出项，图片变为带签名的 assistant 历史。
			input, _ := json.Marshal(map[string]any{"model": "image-model", "input": output})
			request, err := (&handler{}).decodeHTTPRequest(httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(input)))
			if err != nil {
				t.Fatal(err)
			}
			if len(request.Messages) != 1 || request.Messages[0].Role != ir.RoleAssistant || len(request.Messages[0].Content) != 4 {
				t.Fatalf("decoded history = %#v", request.Messages)
			}
			for i, image := range []*ir.ContentBlock{first, second} {
				block := request.Messages[0].Content[i*2+1]
				if block.Type != ir.ContentTypeImage || block.MediaB64 != image.MediaB64 || block.MimeType != image.MimeType || !reflect.DeepEqual(block.ProviderData, image.ProviderData) {
					t.Fatalf("image history changed: %#v", block)
				}
			}
			// 3. 再编码 Responses 时图片仍是顶层项，不混入 message.content。
			reencoded := encodeResponsesRequestBody(t, request)
			items := reencoded["input"].([]any)
			if len(items) != 4 || items[1].(map[string]any)["type"] != "image_generation_call" || items[3].(map[string]any)["type"] != "image_generation_call" {
				t.Fatalf("reencoded = %#v", reencoded)
			}
		})
	}
}

func TestResponsesImagesAndParallelToolsKeepOutputIndices(t *testing.T) {
	// 1. 工具参数尚未结束时到达图片，图片占用独立输出项。
	events := []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "call_a", Name: "read"}},
		{Type: ir.EventContentBlock, ContentBlock: &ir.ContentBlock{Type: ir.ContentTypeImage, MimeType: "image/png", MediaB64: "YWJj"}},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "call_b", Name: "read"}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "call_a", Arguments: `{}`}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "call_b", Arguments: `{"b":1}`}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "call_a", Arguments: `{}`}},
		{Type: ir.EventDone},
	}
	for _, stream := range []bool{false, true} {
		ch := make(chan ir.Event, len(events))
		for _, event := range events {
			ch <- event
		}
		close(ch)
		w := httptest.NewRecorder()
		if err := (&handler{}).encodeHTTPResponse(ch, w, stream); err != nil {
			t.Fatal(err)
		}
		var output []respOutputItem
		if stream {
			for _, frame := range parseSSE(w.Body.Bytes()) {
				if frame.Event == "response.completed" {
					var wire respStreamEvent
					if err := json.Unmarshal([]byte(frame.Data), &wire); err != nil {
						t.Fatal(err)
					}
					output = wire.Response.Output
				}
			}
		} else {
			var wire respResponse
			if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			output = wire.Output
		}
		if len(output) != 3 || output[0].CallID != "call_a" || output[0].Arguments != `{}` || output[1].Type != "image_generation_call" || output[2].CallID != "call_b" || output[2].Arguments != `{"b":1}` {
			t.Fatalf("stream=%v output=%#v", stream, output)
		}
	}
}

// TestResponsesHandlerRequiresInlineGeneratedImage 校验公开响应入口不把图片 URI 编成成功空图。
func TestResponsesHandlerRequiresInlineGeneratedImage(t *testing.T) {
	// 1. 同时覆盖远端文件 URI、无数据图片及可直接转换的内联图片。
	for _, test := range []struct {
		name       string
		block      ir.ContentBlock
		wantResult string
	}{
		{name: "https URI", block: ir.ContentBlock{MediaURL: "https://example.invalid/generated.webp?secret=do-not-log"}},
		{name: "gs URI", block: ir.ContentBlock{MediaURL: "gs://bucket/generated.webp"}},
		{name: "empty image", block: ir.ContentBlock{}},
		{name: "empty data URI", block: ir.ContentBlock{MediaURL: "data:image/webp;base64,"}},
		{name: "non base64 data URI", block: ir.ContentBlock{MediaURL: "data:image/svg+xml,<svg/>"}},
		{name: "base64 data URI", block: ir.ContentBlock{MediaURL: "data:image/webp;base64,YWJj"}, wantResult: "YWJj"},
		{name: "inline base64", block: ir.ContentBlock{MimeType: "image/webp", MediaB64: "YWJj"}, wantResult: "YWJj"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				image := test.block
				image.Type = ir.ContentTypeImage
				events := make(chan ir.Event, 4)
				events <- ir.Event{Type: ir.EventStreamStart}
				events <- ir.Event{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "before image"}}
				events <- ir.Event{Type: ir.EventContentBlock, ContentBlock: &image}
				events <- ir.Event{Type: ir.EventDone}
				close(events)
				chunks, err := NewHandler().EncodeResponse(ctx, protocol.EncodeResponseInput{Protocol: ir.ProtocolOpenAIResponses, Events: events, Stream: stream})
				if err != nil {
					t.Fatal(err)
				}
				var body bytes.Buffer
				var encodeErr error
				for chunk := range chunks {
					body.Write(chunk.Data)
					if chunk.Err != nil {
						encodeErr = chunk.Err
					}
				}
				// 2. URI-only 图片产生可识别转换错误，不产生图片成功项或 response.completed。
				if test.wantResult == "" {
					if encodeErr == nil || !strings.Contains(encodeErr.Error(), "unsupported_response_content") {
						t.Fatalf("encode error = %v, body = %s", encodeErr, body.String())
					}
					if strings.Contains(encodeErr.Error(), "do-not-log") || strings.Contains(body.String(), "do-not-log") {
						t.Fatal("URI query leaked into error")
					}
					if !stream {
						if body.Len() != 0 {
							t.Fatalf("JSON wrote success bytes: %s", body.String())
						}
						return
					}
					var errorFrames int
					for _, frame := range parseSSE(body.Bytes()) {
						if frame.Event == "response.completed" || strings.Contains(frame.Data, `"type":"image_generation_call"`) {
							t.Fatalf("unsupported image reported success: %s", frame.Data)
						}
						if frame.Event == "error" {
							var payload map[string]any
							if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
								t.Fatal(err)
							}
							if payload["code"] != "unsupported_response_content" {
								t.Fatalf("error = %#v", payload)
							}
							errorFrames++
						}
					}
					if errorFrames != 1 {
						t.Fatalf("error frames = %d", errorFrames)
					}
					return
				}
				// 3. 内联图片继续正常输出 result 和真实格式。
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				var response respResponse
				if stream {
					for _, frame := range parseSSE(body.Bytes()) {
						if frame.Event == "response.completed" {
							var completed struct {
								Response respResponse `json:"response"`
							}
							if err := json.Unmarshal([]byte(frame.Data), &completed); err != nil {
								t.Fatal(err)
							}
							response = completed.Response
						}
					}
				} else if err := json.Unmarshal(body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Output) != 2 || response.Output[1].Result != test.wantResult || response.Output[1].OutputFormat != "webp" {
					t.Fatalf("response = %#v", response)
				}
			})
		}
	}
}

// TestResponsesHandlerTextToolTextOrder 保留工具调用两侧文本的输出顺序与签名。
func TestResponsesHandlerTextToolTextOrder(t *testing.T) {
	// 1. 工具前后的文本分别成为输出项，调用签名通过现有 ID 包装带回。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data := &ir.ProviderData{Protocol: ir.ProtocolGemini, Models: []string{"gemini-model"}, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"tool-signature"`)}}
	events := make(chan ir.Event, 7)
	events <- ir.Event{Type: ir.EventStreamStart}
	events <- ir.Event{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "before tool"}}
	events <- ir.Event{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "call_read", Name: "read", ProviderData: data}}
	events <- ir.Event{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "call_read", Arguments: `{"path":"a"}`}}
	events <- ir.Event{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "call_read", Arguments: `{"path":"a"}`, ProviderData: data}}
	events <- ir.Event{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "after tool"}}
	events <- ir.Event{Type: ir.EventDone}
	close(events)
	chunks, err := NewHandler().EncodeResponse(ctx, protocol.EncodeResponseInput{Protocol: ir.ProtocolOpenAIResponses, Stream: true, Events: convert.PackToolCallEvents(ctx, events)})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		body.Write(chunk.Data)
	}
	var output []respOutputItem
	var lifecycle []string
	for _, frame := range parseSSE(body.Bytes()) {
		switch frame.Event {
		case "response.output_item.added", "response.output_item.done":
			var wire respStreamEvent
			if err := json.Unmarshal([]byte(frame.Data), &wire); err != nil {
				t.Fatal(err)
			}
			lifecycle = append(lifecycle, frame.Event+":"+wire.Item.Type)
		case "response.completed":
			var wire respStreamEvent
			if err := json.Unmarshal([]byte(frame.Data), &wire); err != nil {
				t.Fatal(err)
			}
			output = wire.Response.Output
		}
	}
	wantLifecycle := []string{
		"response.output_item.added:message", "response.output_item.done:message",
		"response.output_item.added:function_call", "response.output_item.done:function_call",
		"response.output_item.added:message", "response.output_item.done:message",
	}
	if !reflect.DeepEqual(lifecycle, wantLifecycle) {
		t.Fatalf("lifecycle = %#v", lifecycle)
	}
	if len(output) != 3 || output[0].Content[0].Text != "before tool" || output[1].Type != "function_call" || output[2].Content[0].Text != "after tool" {
		t.Fatalf("output = %#v", output)
	}
	if output[0].ID == output[2].ID {
		t.Fatal("text items share ID")
	}
	callID, unpacked := convert.UnpackToolCallID(output[1].CallID)
	if callID != "call_read" || !reflect.DeepEqual(unpacked, data) {
		t.Fatalf("tool signature = %q %#v", callID, unpacked)
	}

	// 2. 通过公开请求解码入口读回完整输出，再恢复 Gemini 的原始工具签名。
	requestBody, _ := json.Marshal(map[string]any{"model": "gemini-model", "input": output})
	request, err := NewHandler().DecodeRequest(protocol.DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: requestBody})
	if err != nil {
		t.Fatal(err)
	}
	convert.PrepareToolCallsForUpstream(request, ir.ProtocolGemini, "gemini-model", "")
	encoded, _, err := gemini.NewHandler().EncodeRequest(protocol.EncodeRequestInput{Request: request, Target: protocol.Target{Protocol: ir.ProtocolGemini, Model: "gemini-model"}})
	if err != nil {
		t.Fatal(err)
	}
	var geminiBody struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text         string `json:"text"`
				FunctionCall *struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
				ThoughtSignature string `json:"thoughtSignature"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(encoded.Body, &geminiBody); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, content := range geminiBody.Contents {
		if content.Role != "model" {
			t.Fatalf("unexpected role: %s", content.Role)
		}
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				if part.FunctionCall.ID != "call_read" || part.FunctionCall.Name != "read" || string(part.FunctionCall.Args) != `{"path":"a"}` || part.ThoughtSignature != "tool-signature" {
					t.Fatalf("tool part = %#v", part)
				}
				order = append(order, "call:read")
			} else {
				order = append(order, part.Text)
			}
		}
	}
	if !reflect.DeepEqual(order, []string{"before tool", "call:read", "after tool"}) {
		t.Fatalf("Gemini order = %#v; body=%s", order, encoded.Body)
	}
}
