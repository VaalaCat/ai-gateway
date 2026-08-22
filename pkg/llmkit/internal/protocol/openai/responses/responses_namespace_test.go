package responses

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol/openai/chat"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestResponsesDecodeRequestExpandsNamespaceFunctions(t *testing.T) {
	body := `{
		"model":"gpt-5",
		"tools":[{"type":"namespace","name":"multi_agent_v1","tools":[{
			"type":"function","name":"spawn_agent","description":"Spawn","parameters":{"type":"object"},"strict":false
		}]}]
	}`
	req, err := (&handler{}).decodeHTTPRequest(httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("decoded tools = %#v, want one expanded namespace child", req.Tools)
	}
	tool := req.Tools[0]
	if tool.Type != "function" || tool.Namespace != "multi_agent_v1" || tool.Name != "spawn_agent" {
		t.Errorf("decoded tool = %#v, want namespaced function", tool)
	}
	if tool.Strict == nil || *tool.Strict {
		t.Errorf("decoded strict = %#v, want false", tool.Strict)
	}
	parameters, ok := tool.InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("decoded parameters type = %T, want json.RawMessage", tool.InputSchema)
	}
	var schema map[string]any
	if err := json.Unmarshal(parameters, &schema); err != nil || schema["type"] != "object" {
		t.Errorf("decoded parameters = %s, want object schema", parameters)
	}
}

func TestResponsesDecodeAndEncodeResponsePreserveNamespaceNonStream(t *testing.T) {
	arguments := `{"task":"inspect repo"}`
	upstream := `{"id":"resp_1","object":"response","output":[{"type":"function_call","id":"fc_1","call_id":"call_spawn","name":"spawn_agent","namespace":"multi_agent_v1","arguments":` + quoteJSON(arguments) + `}]}`
	events, err := (&handler{}).decodeHTTPResponse(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(upstream))}, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var collected []ir.Event
	for event := range events {
		collected = append(collected, event)
	}
	var decodedCall *ir.ToolCallDelta
	for _, event := range collected {
		if event.Type == ir.EventToolCallDelta && event.Delta != nil {
			decodedCall = event.Delta.ToolCall
		}
	}
	if decodedCall == nil || decodedCall.Namespace != "multi_agent_v1" || decodedCall.Name != "spawn_agent" || decodedCall.ID != "call_spawn" || decodedCall.Arguments != arguments {
		t.Fatalf("decoded tool call = %#v, want namespace/name/call_id/arguments", decodedCall)
	}

	forward := make(chan ir.Event, len(collected))
	for _, event := range collected {
		forward <- event
	}
	close(forward)
	rec := httptest.NewRecorder()
	if err := (&handler{}).encodeHTTPResponse(forward, rec, false); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	var response struct {
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode emitted Responses body: %v", err)
	}
	if len(response.Output) != 1 {
		t.Fatalf("output = %#v, want one function call", response.Output)
	}
	item := response.Output[0]
	if item.Type != "function_call" || item.CallID != "call_spawn" || item.Name != "spawn_agent" || item.Namespace != "multi_agent_v1" || item.Arguments != arguments {
		t.Errorf("emitted Responses item = %#v, want namespace/name/call_id/arguments", item)
	}
}

func TestResponsesEncodeStreamWritesNamespaceOnAddedAndDone(t *testing.T) {
	events := []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{
			CallID: "call_spawn", Index: 3, Name: "spawn_agent", Namespace: "multi_agent_v1",
		}},
		{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{
			CallID: "call_spawn", Index: 3, Arguments: `{"task":"inspect"}`,
		}},
		{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{
			CallID: "call_spawn", Index: 3, Namespace: "multi_agent_v1", Arguments: `{"task":"inspect"}`,
		}},
		{Type: ir.EventDone},
	}

	sse := parseSSE(runEncodeStream(t, events))
	seen := map[string]struct {
		Namespace string
		Name      string
		CallID    string
		Arguments string
	}{}
	for _, event := range sse {
		if event.Event != "response.output_item.added" && event.Event != "response.output_item.done" {
			continue
		}
		var payload struct {
			Item struct {
				Type      string `json:"type"`
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
				CallID    string `json:"call_id"`
				Arguments string `json:"arguments"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("decode %s: %v", event.Event, err)
		}
		if payload.Item.Type == "function_call" {
			seen[event.Event] = struct {
				Namespace string
				Name      string
				CallID    string
				Arguments string
			}{payload.Item.Namespace, payload.Item.Name, payload.Item.CallID, payload.Item.Arguments}
		}
	}
	for _, eventName := range []string{"response.output_item.added", "response.output_item.done"} {
		item, ok := seen[eventName]
		if !ok {
			t.Fatalf("missing function_call %s event: %#v", eventName, sse)
		}
		if item.Namespace != "multi_agent_v1" || item.Name != "spawn_agent" || item.CallID != "call_spawn" {
			t.Errorf("%s item = %#v, want namespace/name/call_id", eventName, item)
		}
	}
}

func TestResponsesNamespaceStreamRoundTripPreservesEveryFunctionCallField(t *testing.T) {
	sseInput := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_namespace","object":"response","status":"in_progress","output":[]}}`,
		``,
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_agent","type":"function_call","call_id":"call_agent","name":"spawn_agent","namespace":"multi_agent_v1","arguments":""}}`,
		``,
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_agent","delta":"{\"task\":\""}`,
		``,
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_shell","type":"function_call","call_id":"call_shell","name":"spawn_agent","namespace":"shell_v1","arguments":""}}`,
		``,
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_shell","delta":"{\"cmd\":\""}`,
		``,
		`event: response.function_call_arguments.done`,
		`data: {"type":"response.function_call_arguments.done","output_index":1,"item_id":"fc_shell","arguments":"{\"cmd\":\"pwd\"}"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"id":"fc_shell","type":"function_call","status":"completed","call_id":"call_shell","name":"spawn_agent","namespace":"shell_v1","arguments":"{\"cmd\":\"pwd\"}"}}`,
		``,
		`event: response.function_call_arguments.done`,
		`data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_agent","arguments":"{\"task\":\"inspect\"}"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_agent","type":"function_call","status":"completed","call_id":"call_agent","name":"spawn_agent","namespace":"multi_agent_v1","arguments":"{\"task\":\"inspect\"}"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_namespace","object":"response","status":"completed","output":[]}}`,
		``,
	}, "\n")

	sse := parseSSE([]byte(roundtripSSE(t, sseInput)))
	type functionCallItem struct {
		Name      string
		Namespace string
		Arguments string
	}
	items := map[string]map[string]functionCallItem{}
	for _, event := range sse {
		if event.Event != "response.output_item.added" && event.Event != "response.output_item.done" {
			continue
		}
		var payload struct {
			Item struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Arguments string `json:"arguments"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("decode %s: %v", event.Event, err)
		}
		if payload.Item.Type != "function_call" {
			continue
		}
		if items[payload.Item.CallID] == nil {
			items[payload.Item.CallID] = make(map[string]functionCallItem)
		}
		items[payload.Item.CallID][event.Event] = functionCallItem{
			Name: payload.Item.Name, Namespace: payload.Item.Namespace, Arguments: payload.Item.Arguments,
		}
	}

	for callID, want := range map[string]functionCallItem{
		"call_agent": {Name: "spawn_agent", Namespace: "multi_agent_v1", Arguments: `{"task":"inspect"}`},
		"call_shell": {Name: "spawn_agent", Namespace: "shell_v1", Arguments: `{"cmd":"pwd"}`},
	} {
		for _, eventName := range []string{"response.output_item.added", "response.output_item.done"} {
			got, ok := items[callID][eventName]
			if !ok {
				t.Fatalf("missing %s for %s in %#v", eventName, callID, items)
			}
			if got.Name != want.Name || got.Namespace != want.Namespace {
				t.Errorf("%s %s = %#v, want namespace/name %q/%q", eventName, callID, got, want.Namespace, want.Name)
			}
			wantArguments := ""
			if eventName == "response.output_item.done" {
				wantArguments = want.Arguments
			}
			if got.Arguments != wantArguments {
				t.Errorf("%s %s arguments = %q, want %q", eventName, callID, got.Arguments, wantArguments)
			}
		}
	}
}

func quoteJSON(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func TestResponsesNamespaceInputFunctionCallRoundTripsIntoRequest(t *testing.T) {
	body := `{"model":"gpt-5","input":[{"type":"function_call","call_id":"call_old","name":"spawn_agent","namespace":"multi_agent_v1","arguments":"{\"task\":\"resume\"}"}]}`
	decoded, err := (&handler{}).decodeHTTPRequest(httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body)))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(decoded.Messages) != 1 || len(decoded.Messages[0].ToolCalls) != 1 {
		t.Fatalf("decoded history = %#v, want one function call", decoded.Messages)
	}
	call := decoded.Messages[0].ToolCalls[0]
	if call.Namespace != "multi_agent_v1" || call.Name != "spawn_agent" || call.ID != "call_old" || call.Arguments != `{"task":"resume"}` {
		t.Errorf("decoded history call = %#v, want namespace/name/call_id/arguments", call)
	}
}

func TestResponsesNamespaceParametersRemainRawAcrossChatEncode(t *testing.T) {
	parameters := `{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"}},"required":["z"]}`
	body := `{"model":"gpt-5","tools":[{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent","parameters":` + parameters + `}]}]}`
	decoded, err := (&handler{}).decodeHTTPRequest(httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	encoded, _, err := chat.NewHandler().EncodeRequest(protocol.EncodeRequestInput{
		Request: decoded,
		Target: protocol.Target{
			BaseURL: "https://upstream.example", APIKey: "key", Model: "gpt-5",
		},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	raw := encoded.Body
	want := `"parameters":` + parameters
	if !bytes.Contains(raw, []byte(want)) {
		t.Errorf("encoded parameters changed:\n got %s\nwant fragment %s", raw, want)
	}
}
