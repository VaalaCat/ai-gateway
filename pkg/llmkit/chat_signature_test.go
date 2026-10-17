package llmkit_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	codec "github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

// 客户端仅保存标准工具字段，下一轮仍须恢复上游要求的两层签名。
func TestChatSignatureToolRoundTrip(t *testing.T) {
	for _, protocol := range []codec.Protocol{codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				message := `{"role":"assistant","signature":"message-signature","tool_calls":[{"index":0,"id":"","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"echo hi\"}"},"signature":"first-signature"},{"index":1,"id":"call_keep","type":"function","function":{"name":"clock","arguments":"{}"},"signature":"second-signature"}]}`
				upstream := `{"model":"gemini-4-argon","choices":[{"message":` + message + `,"finish_reason":"stop"}]}`
				if stream {
					upstream = "data: " + `{"model":"gemini-4-argon","choices":[{"delta":` + message + `}]}` + "\n\ndata: " + `{"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
				}
				client := encodeSignedChatResponse(t, protocol, stream, upstream)
				calls := client["calls"].([]map[string]any)
				require.Len(t, calls, 2)
				require.NotEmpty(t, calls[0]["id"])
				require.NotEqual(t, calls[0]["id"], calls[1]["id"])

				var history map[string]any
				if protocol == codec.ProtocolOpenAIChat {
					history = map[string]any{"model": "gemini-4-argon", "messages": []any{
						map[string]any{"role": "assistant", "tool_calls": calls},
						map[string]any{"role": "tool", "tool_call_id": calls[0]["id"], "content": "hi"},
						map[string]any{"role": "tool", "tool_call_id": calls[1]["id"], "content": "12:00"},
					}}
				} else {
					var input []any
					for _, call := range calls {
						function := call["function"].(map[string]any)
						input = append(input, map[string]any{"type": "function_call", "call_id": call["id"], "name": function["name"], "arguments": function["arguments"]})
					}
					for _, call := range calls {
						input = append(input, map[string]any{"type": "function_call_output", "call_id": call["id"], "output": "hi"})
					}
					history = map[string]any{"model": "gemini-4-argon", "input": input}
				}
				body, err := json.Marshal(history)
				require.NoError(t, err)
				outbound := roundTripRequest(t, protocol, codec.ProtocolOpenAIChat, string(body))
				messages := outbound["messages"].([]any)
				assistant := messages[0].(map[string]any)
				require.Equal(t, "message-signature", assistant["signature"])
				tools := assistant["tool_calls"].([]any)
				for i, signature := range []string{"first-signature", "second-signature"} {
					call := tools[i].(map[string]any)
					require.Equal(t, signature, call["signature"])
					require.Equal(t, calls[i]["function"], call["function"])
					require.NotEmpty(t, call["id"])
					require.Equal(t, call["id"], messages[i+1].(map[string]any)["tool_call_id"])
				}
				require.Equal(t, "call_keep", tools[1].(map[string]any)["id"])
			})
		}
	}
}

func TestChatSignatureExplicitHistory(t *testing.T) {
	body := `{"model":"gemini-4-argon","messages":[{"role":"assistant","content":"收到","signature":"text-signature"},{"role":"assistant","signature":"message-signature","tool_calls":[{"id":"call_original","type":"function","function":{"name":"shell","arguments":"{}"},"signature":"tool-signature"}]},{"role":"tool","tool_call_id":"call_original","content":"hi"}]}`
	outbound := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, body)
	messages := outbound["messages"].([]any)
	require.Equal(t, "text-signature", messages[0].(map[string]any)["signature"])
	assistant := messages[1].(map[string]any)
	require.Equal(t, "message-signature", assistant["signature"])
	call := assistant["tool_calls"].([]any)[0].(map[string]any)
	require.Equal(t, "tool-signature", call["signature"])
	require.Equal(t, "call_original", call["id"])
	require.Equal(t, "call_original", messages[2].(map[string]any)["tool_call_id"])
}

func TestChatSignatureCompleteAssistantHistory(t *testing.T) {
	upstream := `{"choices":[{"message":{"role":"assistant","signature":"message-signature","tool_calls":[{"id":"call_original","type":"function","function":{"name":"shell","arguments":"{}"},"signature":"tool-signature"}]},"finish_reason":"tool_calls"}]}`
	client := encodeSignedChatResponse(t, codec.ProtocolOpenAIChat, false, upstream)
	history := map[string]any{"model": "gemini-4-argon", "messages": []any{
		client["message"],
		map[string]any{"role": "tool", "tool_call_id": client["calls"].([]map[string]any)[0]["id"], "content": "hi"},
	}}
	body, err := json.Marshal(history)
	require.NoError(t, err)
	outbound := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, string(body))
	messages := outbound["messages"].([]any)
	assistant := messages[0].(map[string]any)
	call := assistant["tool_calls"].([]any)[0].(map[string]any)
	require.Equal(t, "call_original", call["id"])
	require.Equal(t, "call_original", messages[1].(map[string]any)["tool_call_id"])
	require.Equal(t, "message-signature", assistant["signature"])
	require.Equal(t, "tool-signature", call["signature"])
}

func TestChatSignatureReusedUpstreamID(t *testing.T) {
	body := `{"model":"gemini-4-argon","messages":[{"role":"assistant","signature":"first","tool_calls":[{"id":"call_reused","type":"function","function":{"name":"shell","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_reused","content":"first result"},{"role":"assistant","signature":"second","tool_calls":[{"id":"call_reused","type":"function","function":{"name":"shell","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_reused","content":"second result"}]}`
	outbound := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, body)
	input := outbound["input"].([]any)
	require.Len(t, input, 4)
	require.Equal(t, input[0].(map[string]any)["call_id"], input[1].(map[string]any)["call_id"])
	require.Equal(t, input[2].(map[string]any)["call_id"], input[3].(map[string]any)["call_id"])
	require.NotEqual(t, input[0].(map[string]any)["call_id"], input[2].(map[string]any)["call_id"])
}

func TestChatSignatureTextRoundTrip(t *testing.T) {
	for _, protocol := range []codec.Protocol{codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				message := `{"role":"assistant","content":"收到","reasoning_content":"","signature":"text-signature"}`
				upstream := `{"choices":[{"message":` + message + `,"finish_reason":"stop"}]}`
				if stream {
					upstream = "data: " + `{"choices":[{"delta":` + message + `,"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
				}
				client := encodeSignedChatResponse(t, protocol, stream, upstream)
				history := map[string]any{"model": "gemini-4-argon"}
				if protocol == codec.ProtocolOpenAIChat {
					history["messages"] = []any{client["message"]}
				} else {
					history["input"] = client["output"]
				}
				body, err := json.Marshal(history)
				require.NoError(t, err)
				outbound := roundTripRequest(t, protocol, codec.ProtocolOpenAIChat, string(body))
				assistant := outbound["messages"].([]any)[0].(map[string]any)
				require.Equal(t, "text-signature", assistant["signature"])
				require.Equal(t, "收到", assistant["content"])
			})
		}
	}
}

// 流式 Chat 按 index 拼接标准字段；Responses 读取最终 output，模拟客户端保存历史。
func encodeSignedChatResponse(t *testing.T, protocol codec.Protocol, stream bool, upstream string) map[string]any {
	t.Helper()
	events, err := getOutbound(codec.ProtocolOpenAIChat).DecodeResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(upstream))}, stream)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	require.NoError(t, getInbound(protocol).EncodeResponse(events, recorder, stream))
	var response map[string]any
	message := map[string]any{"role": "assistant", "content": ""}
	calls := []map[string]any{}
	if stream {
		scanner := bufio.NewScanner(strings.NewReader(recorder.Body.String()))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			var frame map[string]any
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame))
			if protocol == codec.ProtocolOpenAIResponses {
				if frame["type"] == "response.completed" {
					response = frame["response"].(map[string]any)
				}
				continue
			}
			choices := frame["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			delta := choices[0].(map[string]any)["delta"].(map[string]any)
			if text, ok := delta["content"].(string); ok {
				message["content"] = message["content"].(string) + text
			}
			if signature, ok := delta["signature"].(string); ok {
				message["signature"] = signature
			}
			for _, value := range sliceOrEmpty(delta["tool_calls"]) {
				call := value.(map[string]any)
				index := int(call["index"].(float64))
				for len(calls) <= index {
					calls = append(calls, map[string]any{"type": "function", "function": map[string]any{"name": "", "arguments": ""}})
				}
				if id, ok := call["id"]; ok {
					calls[index]["id"] = id
				}
				for key, value := range call["function"].(map[string]any) {
					function := calls[index]["function"].(map[string]any)
					function[key] = function[key].(string) + value.(string)
				}
			}
		}
		require.NoError(t, scanner.Err())
	} else {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		if protocol == codec.ProtocolOpenAIChat {
			message = response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
			for _, value := range sliceOrEmpty(message["tool_calls"]) {
				call := value.(map[string]any)
				calls = append(calls, map[string]any{"id": call["id"], "type": call["type"], "function": call["function"]})
			}
		}
	}
	if protocol == codec.ProtocolOpenAIResponses {
		require.NotNil(t, response)
		for _, value := range response["output"].([]any) {
			item := value.(map[string]any)
			if item["type"] == "function_call" {
				calls = append(calls, map[string]any{"id": item["call_id"], "type": "function", "function": map[string]any{"name": item["name"], "arguments": item["arguments"]}})
			}
		}
		return map[string]any{"calls": calls, "output": response["output"]}
	}
	return map[string]any{"calls": calls, "message": message}
}

func sliceOrEmpty(value any) []any {
	values, _ := value.([]any)
	return values
}
