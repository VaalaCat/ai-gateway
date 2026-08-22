package llmkit_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	codec "github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestFunctionFallbackStateRestoresResponse(t *testing.T) {
	publicCodec := codec.NewCodec()
	decoded, err := publicCodec.DecodeRequest(codec.DecodeRequestInput{
		Method: "POST",
		Path:   "/v1/responses",
		Body: []byte(`{
			"model":"gpt-5.5",
			"input":"edit",
			"tools":[{"type":"custom","name":"apply_patch","description":"Edit files","format":{"type":"grammar"}}]
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := publicCodec.EncodeRequest(codec.EncodeRequestInput{
		Request: decoded.Request,
		Target:  codec.Target{Protocol: codec.ProtocolOpenAIResponses, Model: "upstream"},
		Options: codec.ConversionOptions{BuiltinToolFallback: codec.BuiltinToolFallbackFunction},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		arguments string
		wantInput string
	}{
		{name: "wrapped input", arguments: `{"input":"*** Begin Patch"}`, wantInput: "*** Begin Patch"},
		{name: "malformed arguments", arguments: `not-json`, wantInput: "not-json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"function_call","call_id":"call_patch","name":"apply_patch","arguments":` + mustMarshalString(t, test.arguments) + `}]}`
			events, err := publicCodec.DecodeResponse(context.Background(), codec.DecodeResponseInput{
				Protocol: codec.ProtocolOpenAIResponses,
				Body:     strings.NewReader(response),
				State:    encoded.State,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertCustomToolEvent(t, collectPublicEvents(events), test.wantInput)
		})
	}
}

func TestNamespacedFunctionStateRestoresChatResponse(t *testing.T) {
	publicCodec := codec.NewCodec()
	decoded, err := publicCodec.DecodeRequest(codec.DecodeRequestInput{
		Method: "POST",
		Path:   "/v1/responses",
		Body: []byte(`{
			"model":"gpt-5.5",
			"input":"delegate",
			"tools":[{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}]
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := publicCodec.EncodeRequest(codec.EncodeRequestInput{
		Request: decoded.Request,
		Target:  codec.Target{Protocol: codec.ProtocolOpenAIChat, Model: "upstream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var outbound struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(encoded.Body, &outbound); err != nil {
		t.Fatal(err)
	}
	if len(outbound.Tools) != 1 || outbound.Tools[0].Function.Name == "" {
		t.Fatalf("encoded tools = %#v", outbound.Tools)
	}
	flattenedName := outbound.Tools[0].Function.Name
	response := `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":` + mustMarshalString(t, flattenedName) + `,"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`

	t.Run("state restores namespace", func(t *testing.T) {
		events, err := publicCodec.DecodeResponse(context.Background(), codec.DecodeResponseInput{
			Protocol: codec.ProtocolOpenAIChat,
			Body:     strings.NewReader(response),
			State:    encoded.State,
		})
		if err != nil {
			t.Fatal(err)
		}
		call := findToolCallDelta(t, collectPublicEvents(events))
		if call.Name != "spawn_agent" || call.Namespace != "multi_agent_v1" {
			t.Fatalf("tool call = %#v", call)
		}
	})

	t.Run("empty state keeps wire name", func(t *testing.T) {
		events, err := publicCodec.DecodeResponse(context.Background(), codec.DecodeResponseInput{
			Protocol: codec.ProtocolOpenAIChat,
			Body:     strings.NewReader(response),
		})
		if err != nil {
			t.Fatal(err)
		}
		call := findToolCallDelta(t, collectPublicEvents(events))
		if call.Name != flattenedName || call.Namespace != "" {
			t.Fatalf("tool call = %#v, want wire name %q", call, flattenedName)
		}
	})
}

func collectPublicEvents(events <-chan codec.Event) []codec.Event {
	var collected []codec.Event
	for event := range events {
		collected = append(collected, event)
	}
	return collected
}

func assertCustomToolEvent(t *testing.T, events []codec.Event, wantInput string) {
	t.Helper()
	for _, event := range events {
		if event.RawPassthrough == nil || event.RawPassthrough.EventName != "response.output_item.done" {
			continue
		}
		var payload struct {
			Item struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
				Input  string `json:"input"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(event.RawPassthrough.Data), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Item.Type != "custom_tool_call" || payload.Item.CallID != "call_patch" || payload.Item.Name != "apply_patch" || payload.Item.Input != wantInput {
			t.Fatalf("custom tool payload = %#v", payload.Item)
		}
		return
	}
	t.Fatalf("custom tool event not found: %#v", events)
}

func findToolCallDelta(t *testing.T, events []codec.Event) *codec.ToolCallDelta {
	t.Helper()
	for _, event := range events {
		if event.Type == codec.EventToolCallDelta && event.Delta != nil && event.Delta.ToolCall != nil {
			return event.Delta.ToolCall
		}
	}
	t.Fatalf("tool call delta not found: %#v", events)
	return nil
}

func mustMarshalString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
