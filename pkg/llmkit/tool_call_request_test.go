package llmkit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInspectToolCallRequest(t *testing.T) {
	packed := "call_llmkit_v1_abc"
	for _, tc := range []struct {
		name     string
		protocol Protocol
		body     string
		want     ToolCallRequestInfo
	}{
		{"chat tools", ProtocolOpenAIChat, `{"tools":[{"type":"function"}]}`, ToolCallRequestInfo{HasTools: true}},
		{"chat empty tools", ProtocolOpenAIChat, `{"tools":[],"messages":[{"role":"user","content":"hello"}]}`, ToolCallRequestInfo{}},
		{"chat tool result", ProtocolOpenAIChat, `{"messages":[{"role":"tool","tool_call_id":"call_1"}]}`, ToolCallRequestInfo{HasToolHistory: true}},
		{"chat tool call", ProtocolOpenAIChat, `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_1"}]}]}`, ToolCallRequestInfo{HasToolHistory: true}},
		{"chat packed tool call", ProtocolOpenAIChat, `{"messages":[{"role":"assistant","tool_calls":[{"id":"` + packed + `"}]}]}`, ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}},
		{"chat packed tool result", ProtocolOpenAIChat, `{"tools":[{}],"messages":[{"role":"tool","tool_call_id":"` + packed + `"}]}`, ToolCallRequestInfo{HasTools: true, HasToolHistory: true, HasPackedToolCallID: true}},
		{"chat v1 packed tool call", ProtocolOpenAIChat, `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_llmkit_v1_abc"}]}]}`, ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}},
		{"chat broken packed tool call", ProtocolOpenAIChat, `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_llmkit_garbage"}]}]}`, ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}},
		{"chat plain tool call id", ProtocolOpenAIChat, `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_abc"}]}]}`, ToolCallRequestInfo{HasToolHistory: true}},
		{"chat text", ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"hello"}]}`, ToolCallRequestInfo{}},
		{"responses call output", ProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`, ToolCallRequestInfo{HasToolHistory: true}},
		{"responses custom call", ProtocolOpenAIResponses, `{"input":[{"type":"custom_tool_call","call_id":"call_1"}]}`, ToolCallRequestInfo{HasToolHistory: true}},
		{"responses packed call", ProtocolOpenAIResponses, `{"input":[{"type":"function_call","call_id":"` + packed + `"}]}`, ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}},
		{"responses text input", ProtocolOpenAIResponses, `{"input":"hello"}`, ToolCallRequestInfo{}},
		{"claude tool use", ProtocolClaudeMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"read","input":{}}]}]}`, ToolCallRequestInfo{HasToolHistory: true}},
		{"claude packed tool result", ProtocolClaudeMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + packed + `"}]}]}`, ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}},
		{"claude text content", ProtocolClaudeMessages, `{"messages":[{"role":"user","content":"hello"}]}`, ToolCallRequestInfo{}},
		{"other protocol is not inspected", ProtocolGemini, `{"tools":[{}]}`, ToolCallRequestInfo{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := InspectToolCallRequest(tc.protocol, []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, tc.want, info)
		})
	}
}

func TestInspectToolCallRequestRejectsInvalidJSON(t *testing.T) {
	for _, body := range []string{`{"tools":`, `{"tools":[{}]} trailing`, `[]`} {
		_, err := InspectToolCallRequest(ProtocolOpenAIChat, []byte(body))
		require.Error(t, err, body)
	}
}
