package codec

import (
	"bytes"
	"encoding/json"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestRequest_NewFields_JSONRoundTrip(t *testing.T) {
	store := true
	parallel := true
	req := Request{
		Model:             "gpt-5",
		ToolChoice:        &ToolChoice{Type: "auto"},
		ParallelToolCalls: &parallel,
		ReasoningEffort:   "xhigh",
		Store:             &store,
		Extras: map[string]any{
			"include":          []any{"reasoning.encrypted_content"},
			"prompt_cache_key": "abc123",
		},
		Tools: []Tool{
			{Name: "exec_command", Description: "Run cmd", InputSchema: map[string]any{"type": "object"}, Type: "function", Strict: boolPtr(false)},
			{Name: "web_search", Type: "web_search", RawConfig: map[string]any{"external_web_access": false}},
			{Name: "apply_patch", Type: "custom", Description: "Edit files", RawConfig: map[string]any{"format": map[string]any{"type": "grammar"}}},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ToolChoice == nil || decoded.ToolChoice.Type != "auto" {
		t.Error("ToolChoice lost or wrong after roundtrip")
	}
	if decoded.ParallelToolCalls == nil || !*decoded.ParallelToolCalls {
		t.Error("ParallelToolCalls lost after roundtrip")
	}
	if decoded.ReasoningEffort != "xhigh" {
		t.Errorf("ReasoningEffort = %q, want xhigh", decoded.ReasoningEffort)
	}
	if decoded.Store == nil || !*decoded.Store {
		t.Error("Store lost after roundtrip")
	}
	if decoded.Extras["prompt_cache_key"] != "abc123" {
		t.Error("Extras lost after roundtrip")
	}
	if len(decoded.Tools) != 3 {
		t.Fatalf("Tools len = %d, want 3", len(decoded.Tools))
	}
	if decoded.Tools[1].Type != "web_search" {
		t.Errorf("Tools[1].Type = %q, want web_search", decoded.Tools[1].Type)
	}
	if decoded.Tools[2].RawConfig == nil {
		t.Error("Tools[2].RawConfig lost after roundtrip")
	}
}

func TestTextMessage(t *testing.T) {
	msg := TextMessage(RoleUser, "hello world")

	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != ContentTypeText {
		t.Errorf("expected content type %q, got %q", ContentTypeText, msg.Content[0].Type)
	}
	if msg.Content[0].Text != "hello world" {
		t.Errorf("expected text %q, got %q", "hello world", msg.Content[0].Text)
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCallID != "" {
		t.Errorf("expected empty tool call ID, got %q", msg.ToolCallID)
	}
}

func TestPathToProtocol(t *testing.T) {
	tests := []struct {
		path string
		want Protocol
	}{
		{"/v1/chat/completions", ProtocolOpenAIChat},
		{"/v1/responses", ProtocolOpenAIResponses},
		{"/v1/messages", ProtocolClaude},
		{"/v1/embeddings", ProtocolUnknown},
		{"/health", ProtocolUnknown},
		{"", ProtocolUnknown},
		{"/v1/chat/completions/", ProtocolUnknown},
		{"/v1/messages/batch", ProtocolUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := PathToProtocol(tc.path)
			if got != tc.want {
				t.Errorf("PathToProtocol(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestChannelTypeToProtocol(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		want        Protocol
	}{
		{"claude channel", 14, ProtocolClaude},
		{"openai channel type 1", 1, ProtocolOpenAIChat},
		{"openai channel type 0", 0, ProtocolOpenAIChat},
		{"openai channel type 3", 3, ProtocolOpenAIChat},
		{"negative channel type", -1, ProtocolOpenAIChat},
		{"large channel type", 999, ProtocolOpenAIChat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ChannelTypeToProtocol(tc.channelType)
			if got != tc.want {
				t.Errorf("ChannelTypeToProtocol(%d) = %q, want %q", tc.channelType, got, tc.want)
			}
		})
	}
}

func TestNegotiateOutboundProtocol(t *testing.T) {
	tests := []struct {
		name              string
		inbound           Protocol
		channelType       int
		supportedAPITypes string
		want              Protocol
	}{
		// empty config → fallback to default
		{"empty supported, openai channel, chat inbound", ProtocolOpenAIChat, 1, "", ProtocolOpenAIChat},
		{"empty supported, claude channel, chat inbound", ProtocolOpenAIChat, 14, "", ProtocolClaude},
		// passthrough
		{"passthrough chat", ProtocolOpenAIChat, 1, `["chat-completion","responses"]`, ProtocolOpenAIChat},
		{"passthrough responses", ProtocolOpenAIResponses, 1, `["chat-completion","responses"]`, ProtocolOpenAIResponses},
		{"passthrough claude", ProtocolClaude, 14, `["claude","chat-completion"]`, ProtocolClaude},
		// fallback by priority
		{"fallback responses>chat", ProtocolClaude, 1, `["chat-completion","responses"]`, ProtocolOpenAIResponses},
		{"fallback chat only", ProtocolClaude, 1, `["chat-completion"]`, ProtocolOpenAIChat},
		{"fallback claude only", ProtocolOpenAIChat, 14, `["claude"]`, ProtocolClaude},
		{"fallback responses only", ProtocolOpenAIChat, 1, `["responses"]`, ProtocolOpenAIResponses},
		// invalid JSON → fallback
		{"invalid json", ProtocolOpenAIChat, 1, `not-json`, ProtocolOpenAIChat},
		// unknown types ignored
		{"unknown types ignored", ProtocolOpenAIChat, 1, `["unknown"]`, ProtocolOpenAIChat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NegotiateOutboundProtocol(tc.inbound, tc.channelType, tc.supportedAPITypes, "", nil)
			if got != tc.want {
				t.Errorf("NegotiateOutboundProtocol(%q, %d, %q) = %q, want %q",
					tc.inbound, tc.channelType, tc.supportedAPITypes, got, tc.want)
			}
		})
	}
}

func TestDefaultEndpointPaths(t *testing.T) {
	tests := []struct {
		proto Protocol
		want  string
	}{
		{ProtocolOpenAIChat, "/v1/chat/completions"},
		{ProtocolOpenAIResponses, "/v1/responses"},
		{ProtocolClaude, "/v1/messages"},
		{ProtocolUnknown, ""},
	}
	for _, tc := range tests {
		t.Run(string(tc.proto), func(t *testing.T) {
			got := DefaultEndpointPath(tc.proto)
			if got != tc.want {
				t.Errorf("DefaultEndpointPath(%q) = %q, want %q", tc.proto, got, tc.want)
			}
		})
	}
}

func TestParseEndpoints(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[Protocol]string
	}{
		{"empty", "", nil},
		{"invalid json", "not-json", nil},
		{"single chat", `{"chat_completions":"/v1/chat/completions"}`, map[Protocol]string{ProtocolOpenAIChat: "/v1/chat/completions"}},
		{"all three", `{"chat_completions":"/api/chat","responses":"/api/resp","messages":"/api/msg"}`,
			map[Protocol]string{ProtocolOpenAIChat: "/api/chat", ProtocolOpenAIResponses: "/api/resp", ProtocolClaude: "/api/msg"}},
		{"unknown keys ignored", `{"chat_completions":"/v1/cc","unknown":"/foo"}`, map[Protocol]string{ProtocolOpenAIChat: "/v1/cc"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseEndpoints(tc.raw)
			if len(got) != len(tc.want) {
				t.Errorf("ParseEndpoints(%q) len = %d, want %d", tc.raw, len(got), len(tc.want))
				return
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("ParseEndpoints(%q)[%q] = %q, want %q", tc.raw, k, got[k], v)
				}
			}
		})
	}
}

func TestResolveEndpointPath(t *testing.T) {
	tests := []struct {
		name      string
		endpoints string
		proto     Protocol
		want      string
	}{
		{"from endpoints", `{"chat_completions":"/custom/cc"}`, ProtocolOpenAIChat, "/custom/cc"},
		{"from endpoints claude", `{"messages":"/custom/msg"}`, ProtocolClaude, "/custom/msg"},
		{"fallback default chat", "", ProtocolOpenAIChat, "/v1/chat/completions"},
		{"fallback default responses", "", ProtocolOpenAIResponses, "/v1/responses"},
		{"fallback default claude", "", ProtocolClaude, "/v1/messages"},
		{"proto not in endpoints uses default", `{"chat_completions":"/cc"}`, ProtocolClaude, "/v1/messages"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEndpointPath(tc.endpoints, tc.proto)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNegotiateOutboundProtocol_Endpoints(t *testing.T) {
	tests := []struct {
		name      string
		inbound   Protocol
		chType    int
		supported string
		endpoints string
		want      Protocol
	}{
		{"endpoints passthrough chat", ProtocolOpenAIChat, 1, "", `{"chat_completions":"/v1/cc"}`, ProtocolOpenAIChat},
		{"endpoints fallback responses>chat", ProtocolClaude, 1, "", `{"chat_completions":"/cc","responses":"/r"}`, ProtocolOpenAIResponses},
		{"endpoints only claude", ProtocolOpenAIChat, 1, "", `{"messages":"/m"}`, ProtocolClaude},
		{"empty endpoints uses supported", ProtocolOpenAIChat, 1, `["responses"]`, "", ProtocolOpenAIResponses},
		{"empty both uses type", ProtocolOpenAIChat, 14, "", "", ProtocolClaude},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NegotiateOutboundProtocol(tc.inbound, tc.chType, tc.supported, tc.endpoints, nil)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEventToolCallStreamingTypes(t *testing.T) {
	// 三个新 event type 必须互不相同且与既有事件不冲突
	seen := map[EventType]string{
		EventStreamStart:            "EventStreamStart",
		EventContentDelta:           "EventContentDelta",
		EventToolCallDelta:          "EventToolCallDelta",
		EventThinkingDelta:          "EventThinkingDelta",
		EventUsage:                  "EventUsage",
		EventDone:                   "EventDone",
		EventError:                  "EventError",
		EventRawPassthrough:         "EventRawPassthrough",
		EventContentBlockStop:       "EventContentBlockStop",
		EventSignatureDelta:         "EventSignatureDelta",
		EventToolCallStart:          "EventToolCallStart",
		EventToolCallArgumentsDelta: "EventToolCallArgumentsDelta",
		EventToolCallEnd:            "EventToolCallEnd",
	}
	if len(seen) != 13 {
		t.Errorf("expected 13 unique EventType values, got %d", len(seen))
	}
}

func TestStreamingToolCallStruct(t *testing.T) {
	tc := StreamingToolCall{
		CallID:    "call_x",
		Index:     0,
		Name:      "exec",
		Arguments: `{"a":1}`,
	}
	if tc.CallID != "call_x" {
		t.Errorf("CallID = %q, want call_x", tc.CallID)
	}
}

func TestEventToolCallField(t *testing.T) {
	ev := Event{
		Type:     EventToolCallStart,
		ToolCall: &StreamingToolCall{CallID: "call_x", Name: "exec"},
	}
	if ev.ToolCall == nil {
		t.Fatal("ToolCall is nil")
	}
	if ev.ToolCall.CallID != "call_x" {
		t.Errorf("ToolCall.CallID = %q, want call_x", ev.ToolCall.CallID)
	}
}

func TestRequestInboundProtocolSerialization(t *testing.T) {
	req := Request{
		Model:           "gpt-5",
		Messages:        []Message{},
		InboundProtocol: ProtocolOpenAIResponses,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"inbound_protocol":"openai_responses"`)) {
		t.Errorf("expected inbound_protocol in JSON, got: %s", data)
	}

	// 零值应被 omitempty 省略
	req2 := Request{Model: "x", Messages: []Message{}}
	data2, _ := json.Marshal(req2)
	if bytes.Contains(data2, []byte(`"inbound_protocol"`)) {
		t.Errorf("expected inbound_protocol omitted when empty, got: %s", data2)
	}
}
