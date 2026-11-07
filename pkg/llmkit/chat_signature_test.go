package llmkit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	codec "github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

// 使用者只保存标准工具字段，下一轮仍能恢复上游要求的两层签名。
func TestChatSignatureToolRoundTrip(t *testing.T) {
	for _, protocol := range []codec.Protocol{codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, codec.ProtocolClaudeMessages} {
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

				outbound := roundTripRequest(t, protocol, codec.ProtocolOpenAIChat, toolHistory(t, protocol, calls))
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
	require.Equal(t, "message-signature", assistant["signature"], "使用者自带的签名原样发给 Chat 上游")
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
	// Responses 上游不认 Chat 的签名，两轮都只发原始 ID，结果各自跟随前面的调用。
	for _, item := range input {
		require.Equal(t, "call_reused", item.(map[string]any)["call_id"])
	}
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

// 签名位于首块或 Google 扩展中：解码时收进 ProviderData，使用者只保存工具 ID 后仍能回放；
// 签名比 Start 晚到时 ProviderData 仍完整，但已发给使用者的 ID 带不上它。
func TestChatSignatureCompatibilityRoundTrip(t *testing.T) {
	// 1. 覆盖首块之后的工具签名、消息签名及 Google 扩展。
	start := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_original","type":"function","function":{"name":"shell","arguments":""}}]}}]}`
	cases := []struct {
		name             string
		chunks           []string
		response         string
		signature        string
		thoughtSignature string
		messageSignature string
		lostAfterStart   bool // 签名晚于 Start 到达，通过 ID 拿包的使用者拿不到（Best-Effort）
	}{
		{
			name: "late_tool_signature",
			chunks: []string{start,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"signature":"tool-signature"}]},"finish_reason":"tool_calls"}]}`,
			},
			signature:      "tool-signature",
			lostAfterStart: true,
		},
		{
			name: "late_message_signature",
			chunks: []string{start,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
				`{"choices":[{"delta":{"signature":"message-signature"},"finish_reason":"tool_calls"}]}`,
			},
			messageSignature: "message-signature",
			lostAfterStart:   true,
		},
		{
			name: "google_first_chunk",
			chunks: []string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_original","type":"function","function":{"name":"shell","arguments":"{}"},"extra_content":{"google":{"thought_signature":"google-signature"}}}]},"finish_reason":"tool_calls"}]}`,
			},
			thoughtSignature: "google-signature",
		},
		{
			name: "google_late_chunk",
			chunks: []string{start,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"extra_content":{"google":{"thought_signature":"google-signature"}}}]},"finish_reason":"tool_calls"}]}`,
			},
			thoughtSignature: "google-signature",
			lostAfterStart:   true,
		},
		{
			name:             "nonstream_both_formats",
			response:         `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_original","type":"function","function":{"name":"shell","arguments":"{}"},"signature":"tool-signature","extra_content":{"google":{"thought_signature":"google-signature"}}}]},"finish_reason":"tool_calls"}]}`,
			signature:        "tool-signature",
			thoughtSignature: "google-signature",
		},
	}
	for _, test := range cases {
		upstream := test.response
		for _, chunk := range test.chunks {
			upstream += "data: " + chunk + "\n\n"
		}
		stream := len(test.chunks) > 0
		if stream {
			upstream += "data: [DONE]\n\n"
		}

		// 2. 解码上游响应：签名进 ProviderData，模型记录为本层发给上游的模型名。
		t.Run(test.name+"/provider_data", func(t *testing.T) {
			data := decodeToolCallProviderData(t, stream, upstream)
			require.NotNil(t, data)
			require.Equal(t, codec.ProtocolOpenAIChat, data.Protocol)
			require.Equal(t, []string{"gemini-4-argon"}, data.Models)
			wantFields := map[string]string{}
			if test.signature != "" {
				wantFields["signature"] = strconv.Quote(test.signature)
			}
			if test.thoughtSignature != "" {
				wantFields["extra_content"] = `{"google":{"thought_signature":` + strconv.Quote(test.thoughtSignature) + `}}`
			}
			if test.messageSignature != "" {
				wantFields["message_signature"] = strconv.Quote(test.messageSignature)
			}
			require.Len(t, data.Fields, len(wantFields))
			for key, want := range wantFields {
				require.JSONEq(t, want, string(data.Fields[key]), key)
			}
		})

		for _, protocol := range []codec.Protocol{codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, codec.ProtocolClaudeMessages} {
			t.Run(fmt.Sprintf("%s/%s", test.name, protocol), func(t *testing.T) {
				// 3. 返回使用者，并用使用者保存的标准工具字段构造下一轮。
				client := encodeSignedChatResponse(t, protocol, stream, upstream)
				calls := client["calls"].([]map[string]any)
				require.Len(t, calls, 1)
				outbound := roundTripRequest(t, protocol, codec.ProtocolOpenAIChat, toolHistory(t, protocol, calls))

				// 4. 核对上游原始 ID、两种工具签名和消息签名；晚到的签名只确认没有凭空出现。
				messages := outbound["messages"].([]any)
				assistant := messages[0].(map[string]any)
				call := assistant["tool_calls"].([]any)[0].(map[string]any)
				require.Equal(t, "call_original", call["id"])
				require.Equal(t, "call_original", messages[1].(map[string]any)["tool_call_id"])
				require.Equal(t, map[string]any{"name": "shell", "arguments": "{}"}, call["function"])
				if test.lostAfterStart {
					require.NotContains(t, call, "signature")
					require.NotContains(t, call, "extra_content")
					require.NotContains(t, assistant, "signature")
					return
				}
				if test.signature != "" {
					require.Equal(t, test.signature, call["signature"])
				}
				if test.thoughtSignature != "" {
					require.Equal(t, map[string]any{"google": map[string]any{"thought_signature": test.thoughtSignature}}, call["extra_content"])
				}
				if test.messageSignature != "" {
					require.Equal(t, test.messageSignature, assistant["signature"])
				}
			})
		}
	}
}

func TestChatSignatureGoogleHistory(t *testing.T) {
	// 1. 使用者直接回传 Google 扩展：ProviderData 没有模型记录，发给 Chat 上游时原样写回，重复转换后仍在。
	body := `{"model":"gemini-4-argon","messages":[{"role":"assistant","tool_calls":[{"id":"call_original","type":"function","function":{"name":"shell","arguments":"{}"},"extra_content":{"google":{"thought_signature":"google-signature"}}}]},{"role":"tool","tool_call_id":"call_original","content":"ok"}]}`
	for range 2 {
		outbound := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, body)
		messages := outbound["messages"].([]any)
		call := messages[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
		require.Equal(t, "call_original", call["id"])
		require.Equal(t, "call_original", messages[1].(map[string]any)["tool_call_id"])
		require.Equal(t, map[string]any{"google": map[string]any{"thought_signature": "google-signature"}}, call["extra_content"])
		encoded, err := json.Marshal(outbound)
		require.NoError(t, err)
		body = string(encoded)
	}
}

// encodeSignedChatResponse 把 Chat 上游响应经 Codec 交给使用者，按使用者协议取回它会保存的工具调用。
// 流式 Chat 按 index 拼接标准字段；Responses 读取最终 output；Claude 读取 tool_use 块。
func encodeSignedChatResponse(t *testing.T, protocol codec.Protocol, stream bool, upstream string) map[string]any {
	t.Helper()
	public := codec.NewCodec()
	encoded, err := public.EncodeRequest(codec.EncodeRequestInput{Target: codec.Target{Protocol: codec.ProtocolOpenAIChat, BaseURL: "https://test.example.com", Model: "gemini-4-argon"}})
	require.NoError(t, err)
	events, err := public.DecodeResponse(context.Background(), codec.DecodeResponseInput{Protocol: codec.ProtocolOpenAIChat, StatusCode: 200, Body: strings.NewReader(upstream), Stream: stream, State: encoded.State})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	require.NoError(t, getInbound(protocol).EncodeResponse(events, recorder, stream))
	if protocol == codec.ProtocolClaudeMessages {
		return map[string]any{"calls": claudeClientToolCalls(t, stream, recorder.Body.String())}
	}
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
					require.Empty(t, calls[index]["id"], "工具 ID 只能发送一次")
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

// claudeClientToolCalls 从 Claude 响应里取出 tool_use 块，转成 Chat 形状的工具调用；流式按块序号拼接 input_json_delta。
func claudeClientToolCalls(t *testing.T, stream bool, body string) []map[string]any {
	t.Helper()
	newCall := func(id, name any) map[string]any {
		return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": ""}}
	}
	calls := []map[string]any{}
	// 1. 非流式：content 里的 tool_use 块，input 序列化成参数。
	if !stream {
		var response struct {
			Content []map[string]any `json:"content"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &response))
		for _, block := range response.Content {
			if block["type"] != "tool_use" {
				continue
			}
			call := newCall(block["id"], block["name"])
			arguments, err := json.Marshal(block["input"])
			require.NoError(t, err)
			call["function"].(map[string]any)["arguments"] = string(arguments)
			calls = append(calls, call)
		}
		return calls
	}

	// 2. 流式：content_block_start 开始一个调用，input_json_delta 追加参数。
	byBlock := map[float64]map[string]any{}
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var frame map[string]any
		require.NoError(t, json.Unmarshal([]byte(data), &frame))
		index, _ := frame["index"].(float64)
		switch frame["type"] {
		case "content_block_start":
			block := frame["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				byBlock[index] = newCall(block["id"], block["name"])
				calls = append(calls, byBlock[index])
			}
		case "content_block_delta":
			delta := frame["delta"].(map[string]any)
			if call, ok := byBlock[index]; ok && delta["type"] == "input_json_delta" {
				function := call["function"].(map[string]any)
				function["arguments"] = function["arguments"].(string) + delta["partial_json"].(string)
			}
		}
	}
	require.NoError(t, scanner.Err())
	return calls
}

// toolHistory 用使用者保存的工具调用拼出下一轮请求：先是这些调用，再是每个调用的结果。
func toolHistory(t *testing.T, protocol codec.Protocol, calls []map[string]any) string {
	t.Helper()
	history := map[string]any{"model": "gemini-4-argon"}
	switch protocol {
	case codec.ProtocolOpenAIChat:
		messages := []any{map[string]any{"role": "assistant", "tool_calls": calls}}
		for _, call := range calls {
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call["id"], "content": "hi"})
		}
		history["messages"] = messages
	case codec.ProtocolOpenAIResponses:
		var input []any
		for _, call := range calls {
			function := call["function"].(map[string]any)
			input = append(input, map[string]any{"type": "function_call", "call_id": call["id"], "name": function["name"], "arguments": function["arguments"]})
		}
		for _, call := range calls {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": call["id"], "output": "hi"})
		}
		history["input"] = input
	case codec.ProtocolClaudeMessages:
		var uses, results []any
		for _, call := range calls {
			function := call["function"].(map[string]any)
			var input any
			require.NoError(t, json.Unmarshal([]byte(function["arguments"].(string)), &input))
			uses = append(uses, map[string]any{"type": "tool_use", "id": call["id"], "name": function["name"], "input": input})
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": call["id"], "content": "hi"})
		}
		history["max_tokens"] = 100
		history["messages"] = []any{
			map[string]any{"role": "assistant", "content": uses},
			map[string]any{"role": "user", "content": results},
		}
	}
	body, err := json.Marshal(history)
	require.NoError(t, err)
	return string(body)
}

// decodeToolCallProviderData 用发往 gemini-4-argon 的转换状态解码 Chat 上游响应，取最后一个工具调用事件里的 ProviderData。
func decodeToolCallProviderData(t *testing.T, stream bool, upstream string) *codec.ToolCallProviderData {
	t.Helper()
	public := codec.NewCodec()
	encoded, err := public.EncodeRequest(codec.EncodeRequestInput{Target: codec.Target{Protocol: codec.ProtocolOpenAIChat, Model: "gemini-4-argon"}})
	require.NoError(t, err)
	events, err := public.DecodeResponse(context.Background(), codec.DecodeResponseInput{Protocol: codec.ProtocolOpenAIChat, StatusCode: 200, Body: strings.NewReader(upstream), Stream: stream, State: encoded.State})
	require.NoError(t, err)
	var data *codec.ToolCallProviderData
	for event := range events {
		switch {
		case event.Type == codec.EventToolCallEnd:
			data = event.ToolCall.ProviderData
		case event.Type == codec.EventToolCallDelta && event.Delta != nil && event.Delta.ToolCall != nil:
			data = event.Delta.ToolCall.ProviderData
		}
	}
	return data
}

func sliceOrEmpty(value any) []any {
	values, _ := value.([]any)
	return values
}
