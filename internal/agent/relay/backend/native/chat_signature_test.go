package native

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

func TestRelayChatSignatureSurvivesThinkingStrip(t *testing.T) {
	for _, protocol := range []llmkit.Protocol{llmkit.ProtocolOpenAIChat, llmkit.ProtocolOpenAIResponses} {
		for _, toolCall := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/tool=%t", protocol, toolCall), func(t *testing.T) {
				requests := make(chan map[string]any, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					var received map[string]any
					if err != nil || json.Unmarshal(body, &received) != nil {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					requests <- received
					w.Header().Set("Content-Type", "application/json")
					if toolCall {
						_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","signature":"text-signature","tool_calls":[{"id":"","type":"function","function":{"name":"shell","arguments":"{}"},"signature":"tool-signature"}]},"finish_reason":"tool_calls"}]}`)
						return
					}
					_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"收到","signature":"text-signature"},"finish_reason":"stop"}]}`)
				}))
				defer server.Close()
				channel := makeNativeChannel(server.URL)
				channel.SupportedAPITypes = `["chat-completion"]`
				backend := &Backend{}
				body := `{"model":"gemini-4-argon","messages":[{"role":"user","content":"hi"}]}`
				if protocol == llmkit.ProtocolOpenAIResponses {
					body = `{"model":"gemini-4-argon","input":"hi"}`
				}
				relay, recorder := newNativeTestCtx(t, []byte(body), protocol, false)
				result := backend.Relay(relay, state.Attempt{Channel: channel, RealModel: "gemini-4-argon"})
				require.NoError(t, result.Err)
				<-requests
				var response map[string]any
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				history := map[string]any{"model": "gemini-4-argon"}
				if protocol == llmkit.ProtocolOpenAIChat {
					message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
					messages := []any{message}
					if toolCall {
						call := message["tool_calls"].([]any)[0].(map[string]any)
						messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call["id"], "content": "hi"})
					}
					history["messages"] = messages
				} else {
					input := response["output"].([]any)
					if toolCall {
						for _, value := range response["output"].([]any) {
							item := value.(map[string]any)
							if item["type"] == "function_call" {
								input = append(input, map[string]any{"type": "function_call_output", "call_id": item["call_id"], "output": "hi"})
							}
						}
					}
					history["input"] = input
				}
				bodyBytes, err := json.Marshal(history)
				require.NoError(t, err)
				relay, _ = newNativeTestCtx(t, bodyBytes, protocol, false)
				result = backend.Relay(relay, state.Attempt{Channel: channel, RealModel: "gemini-4-argon"})
				require.NoError(t, result.Err)
				received := <-requests
				messages := received["messages"].([]any)
				assistant := messages[0].(map[string]any)
				require.Equal(t, "text-signature", assistant["signature"])
				if toolCall {
					call := assistant["tool_calls"].([]any)[0].(map[string]any)
					require.Equal(t, "tool-signature", call["signature"])
					require.NotEmpty(t, call["id"])
					require.Equal(t, call["id"], messages[1].(map[string]any)["tool_call_id"])
				} else {
					require.Equal(t, "收到", assistant["content"])
				}
			})
		}
	}
}

const (
	realSignature        = "real-sig"
	placeholderSignature = "context_engineering_is_the_way_to_go"
	signaturePublicModel = "gemini-4-argon"
	signatureRealModel   = "gemini-3-pro"
)

// signatureCallIDs 是假上游每次返回的并行工具调用 ID，只有第一个带签名。
var signatureCallIDs = []string{"call_g1", "call_g2", "call_g3"}

// signatureUpstream 是假的 Gemini Chat 上游：没有工具结果时返回三个并行工具调用，有工具结果时返回文本，并记下收到的每个请求体。
type signatureUpstream struct {
	*httptest.Server
	late   bool // 流式下签名在工具调用开始之后才到
	mu     sync.Mutex
	bodies [][]byte
}

func newSignatureUpstream(t *testing.T, late bool) *signatureUpstream {
	t.Helper()
	upstream := &signatureUpstream{late: late}
	upstream.Server = httptest.NewServer(http.HandlerFunc(upstream.serve))
	t.Cleanup(upstream.Close)
	return upstream
}

// requests 返回上游到目前为止收到的请求体。
func (upstream *signatureUpstream) requests() [][]byte {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	return slices.Clone(upstream.bodies)
}

func (upstream *signatureUpstream) serve(w http.ResponseWriter, r *http.Request) {
	// 1. 记下请求体。
	body, err := io.ReadAll(r.Body)
	var request struct {
		Stream   bool
		Messages []struct{ Role string }
	}
	if err != nil || json.Unmarshal(body, &request) != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	upstream.mu.Lock()
	upstream.bodies = append(upstream.bodies, body)
	upstream.mu.Unlock()

	// 2. 历史里已有工具结果就回文本，否则回工具调用。
	hasToolResult := slices.ContainsFunc(request.Messages, func(message struct{ Role string }) bool {
		return message.Role == "tool"
	})
	switch {
	case request.Stream && hasToolResult:
		upstream.writeStream(w, `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)
	case hasToolResult:
		upstream.writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	case request.Stream:
		upstream.writeStream(w, upstream.toolCallFrames()...)
	default:
		calls, _ := json.Marshal(upstream.toolCalls())
		upstream.writeJSON(w, `{"choices":[{"message":{"role":"assistant","tool_calls":`+string(calls)+`},"finish_reason":"tool_calls"}]}`)
	}
}

// toolCalls 返回三个并行工具调用，第一个带 Google 思考签名。
func (upstream *signatureUpstream) toolCalls() []map[string]any {
	calls := make([]map[string]any, 0, len(signatureCallIDs))
	for index, id := range signatureCallIDs {
		call := map[string]any{
			"index": index, "id": id, "type": "function",
			"function": map[string]any{"name": "shell", "arguments": "{}"},
		}
		if index == 0 && !upstream.late {
			call["extra_content"] = googleExtraContent(realSignature)
		}
		calls = append(calls, call)
	}
	return calls
}

// toolCallFrames 返回流式的工具调用帧；late 时签名放在所有工具调用开始之后。
func (upstream *signatureUpstream) toolCallFrames() []string {
	frames := []string{`{"choices":[{"delta":{"reasoning_content":"tool plan"}}]}`}
	for _, call := range upstream.toolCalls() {
		encoded, _ := json.Marshal(call)
		frames = append(frames, `{"choices":[{"delta":{"tool_calls":[`+string(encoded)+`]}}]}`)
	}
	if upstream.late {
		late, _ := json.Marshal(googleExtraContent(realSignature))
		frames = append(frames, `{"choices":[{"delta":{"tool_calls":[{"index":0,"extra_content":`+string(late)+`}]}}]}`)
	}
	return append(frames, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
}

func (*signatureUpstream) writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func (*signatureUpstream) writeStream(w http.ResponseWriter, frames ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, frame := range frames {
		_, _ = io.WriteString(w, "data: "+frame+"\n\n")
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func googleExtraContent(signature string) map[string]any {
	return map[string]any{"google": map[string]any{"thought_signature": signature}}
}

// newSignatureChannel 造一个指向 baseURL 的 Chat 渠道；fallback 非空时给 gemini- 模型加签名兜底规则。
func newSignatureChannel(id uint, baseURL, fallback string) *models.Channel {
	channel := makeNativeChannel(baseURL)
	channel.ID = id
	channel.SupportedAPITypes = `["chat-completion"]`
	if fallback != "" {
		channel.OtherSettings = fmt.Sprintf(`{"model_tool_schema_compatibility":[{"model_pattern":"^gemini-","profile":"none","signature_fallback":%q}]}`, fallback)
	}
	return channel
}

// clientToolCall 是使用者从响应里读到并保存的标准工具字段。
type clientToolCall struct{ ID, Name string }

// signatureClientWire 是某个协议的使用者如何读工具调用、如何把工具结果拼回历史。
type signatureClientWire struct {
	extraFields map[string]any
	toolCalls   func(t *testing.T, frames []string) []clientToolCall
	history     func(calls []clientToolCall) []any
}

var signatureClientWires = map[llmkit.Protocol]signatureClientWire{
	llmkit.ProtocolOpenAIChat: {toolCalls: chatClientToolCalls, history: chatClientHistory},
	llmkit.ProtocolClaude:     {extraFields: map[string]any{"max_tokens": 1024}, toolCalls: claudeClientToolCalls, history: claudeClientHistory},
}

// chatClientToolCalls 从 Chat 响应帧（非流式是完整响应）读出工具调用的 ID 和名字。
func chatClientToolCalls(t *testing.T, frames []string) []clientToolCall {
	type wireCalls struct {
		ToolCalls []struct {
			Index    int
			ID       string
			Function struct{ Name string }
		} `json:"tool_calls"`
	}
	var calls []clientToolCall
	for _, frame := range frames {
		var parsed struct {
			Choices []struct{ Message, Delta wireCalls }
		}
		require.NoError(t, json.Unmarshal([]byte(frame), &parsed))
		for _, choice := range parsed.Choices {
			for _, call := range choice.Delta.ToolCalls {
				if call.ID != "" {
					require.Equal(t, len(calls), call.Index)
					calls = append(calls, clientToolCall{ID: call.ID, Name: call.Function.Name})
				}
			}
			for _, call := range choice.Message.ToolCalls {
				calls = append(calls, clientToolCall{ID: call.ID, Name: call.Function.Name})
			}
		}
	}
	return calls
}

func chatClientHistory(calls []clientToolCall) []any {
	wireCalls := make([]any, 0, len(calls))
	results := make([]any, 0, len(calls))
	for _, call := range calls {
		wireCalls = append(wireCalls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": "{}"}})
		results = append(results, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": "ok"})
	}
	return append([]any{map[string]any{"role": "assistant", "tool_calls": wireCalls}}, results...)
}

// claudeClientToolCalls 从 Claude 响应帧（非流式是完整响应）读出 tool_use 块的 ID 和名字。
func claudeClientToolCalls(t *testing.T, frames []string) []clientToolCall {
	type block struct{ Type, ID, Name string }
	var calls []clientToolCall
	for _, frame := range frames {
		var parsed struct {
			Content      []block
			ContentBlock block `json:"content_block"`
		}
		require.NoError(t, json.Unmarshal([]byte(frame), &parsed))
		for _, item := range append(parsed.Content, parsed.ContentBlock) {
			if item.Type == "tool_use" {
				calls = append(calls, clientToolCall{ID: item.ID, Name: item.Name})
			}
		}
	}
	return calls
}

func claudeClientHistory(calls []clientToolCall) []any {
	uses := make([]any, 0, len(calls))
	results := make([]any, 0, len(calls))
	for _, call := range calls {
		uses = append(uses, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]any{}})
		results = append(results, map[string]any{"type": "tool_result", "tool_use_id": call.ID, "content": "ok"})
	}
	return []any{
		map[string]any{"role": "assistant", "content": uses},
		map[string]any{"role": "user", "content": results},
	}
}

// signatureClient 是只保存标准工具字段的使用者：第 1 轮拿工具调用，第 2 轮带着工具结果再发。
type signatureClient struct {
	protocol llmkit.Protocol
	stream   bool
}

// twoRounds 先在 first 上发第 1 轮，再把读到的工具调用加上工具结果在 second 上发第 2 轮，返回第 1 轮读到的工具调用和响应体。
func (client signatureClient) twoRounds(t *testing.T, first, second state.Attempt) ([]clientToolCall, string) {
	t.Helper()
	wire := signatureClientWires[client.protocol]

	// 1. 第 1 轮：只有用户消息。
	user := map[string]any{"role": "user", "content": "hi"}
	response := client.relay(t, wire, []any{user}, first)
	frames := []string{response}
	if client.stream {
		frames = sseDataFrames(response)
	}
	calls := wire.toolCalls(t, frames)
	require.Len(t, calls, len(signatureCallIDs))

	// 2. 第 2 轮：历史里是读到的工具调用和对应的工具结果。
	require.Contains(t, client.relay(t, wire, append([]any{user}, wire.history(calls)...), second), "done")
	return calls, response
}

// sseDataFrames 取出 SSE 响应里每个 data 帧的 JSON，跳过结束标记。
func sseDataFrames(response string) []string {
	var frames []string
	for _, line := range strings.Split(response, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok && data != "[DONE]" {
			frames = append(frames, data)
		}
	}
	return frames
}

func (client signatureClient) relay(t *testing.T, wire signatureClientWire, messages []any, attempt state.Attempt) string {
	t.Helper()
	request := map[string]any{"model": signaturePublicModel, "stream": client.stream, "messages": messages}
	maps.Copy(request, wire.extraFields)
	body, err := json.Marshal(request)
	require.NoError(t, err)
	relay, recorder := newNativeTestCtx(t, body, client.protocol, client.stream)
	result := (&Backend{}).Relay(relay, attempt)
	require.NoError(t, result.Err)
	return recorder.Body.String()
}

// requireClientToolCalls 断言使用者看到的第一个工具调用 ID 是否被打包、其余保持原样，且响应里没有原生签名字段。
func requireClientToolCalls(t *testing.T, calls []clientToolCall, response string, packed bool) {
	t.Helper()
	if packed {
		require.True(t, strings.HasPrefix(calls[0].ID, "call_llmkit_v1_"), "first call id %q is not packed", calls[0].ID)
	} else {
		require.Equal(t, signatureCallIDs[0], calls[0].ID)
	}
	for index, call := range calls[1:] {
		require.Equal(t, signatureCallIDs[index+1], call.ID)
	}
	for _, forbidden := range []string{"extra_content", "thought_signature", realSignature} {
		require.NotContains(t, response, forbidden)
	}
}

// requireUpstreamToolRound 断言上游收到的工具历史：调用和结果都用原始 ID，只有第一个调用可能带签名。
func requireUpstreamToolRound(t *testing.T, body []byte, wantSignature string) {
	t.Helper()
	var request struct {
		Messages []struct {
			Role      string
			ToolCalls []struct {
				ID           string
				ExtraContent json.RawMessage `json:"extra_content"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		}
	}
	require.NoError(t, json.Unmarshal(body, &request))
	var callIDs, resultIDs []string
	var extras []json.RawMessage
	for _, message := range request.Messages {
		for _, call := range message.ToolCalls {
			callIDs = append(callIDs, call.ID)
			extras = append(extras, call.ExtraContent)
		}
		if message.Role == "tool" {
			resultIDs = append(resultIDs, message.ToolCallID)
		}
	}
	require.Equal(t, signatureCallIDs, callIDs)
	require.Equal(t, signatureCallIDs, resultIDs)
	if wantSignature == "" {
		require.Empty(t, extras[0])
	} else {
		expected, err := json.Marshal(googleExtraContent(wantSignature))
		require.NoError(t, err)
		require.JSONEq(t, string(expected), string(extras[0]))
	}
	require.Empty(t, extras[1])
	require.Empty(t, extras[2])
	require.NotContains(t, string(body), "call_llmkit_")
	if wantSignature != realSignature {
		require.NotContains(t, string(body), realSignature)
	}
}

// 使用者只保存标准工具字段：第 1 轮拿到打包的 ID，第 2 轮带回，上游按规则收到原签名或兜底签名。
func TestRelayChatGoogleSignatureRoundTrip(t *testing.T) {
	rows := []struct {
		name          string
		protocol      llmkit.Protocol
		stream        bool
		late          bool
		wantSignature string
	}{
		{name: "claude", protocol: llmkit.ProtocolClaude, wantSignature: realSignature},
		{name: "claude stream", protocol: llmkit.ProtocolClaude, stream: true, wantSignature: realSignature},
		{name: "chat", protocol: llmkit.ProtocolOpenAIChat, wantSignature: realSignature},
		{name: "chat stream", protocol: llmkit.ProtocolOpenAIChat, stream: true, wantSignature: realSignature},
		// 签名比工具调用开始晚到，已发给使用者的 ID 带不上它，第 2 轮只剩兜底签名。
		{name: "claude stream late signature", protocol: llmkit.ProtocolClaude, stream: true, late: true, wantSignature: placeholderSignature},
		{name: "chat stream late signature", protocol: llmkit.ProtocolOpenAIChat, stream: true, late: true, wantSignature: placeholderSignature},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			// 1. 同一个渠道跑两轮。
			upstream := newSignatureUpstream(t, row.late)
			attempt := state.Attempt{Channel: newSignatureChannel(1, upstream.URL, "gemini"), RealModel: signatureRealModel}
			client := signatureClient{protocol: row.protocol, stream: row.stream}
			calls, response := client.twoRounds(t, attempt, attempt)

			// 2. 使用者拿到的是打包 ID；上游第 2 次收到的历史用原始 ID。
			requireClientToolCalls(t, calls, response, !row.late)
			requests := upstream.requests()
			require.Len(t, requests, 2)
			requireUpstreamToolRound(t, requests[1], row.wantSignature)
		})
	}
}

// 同一个模型名下两个渠道：第 1 轮走渠道 A，第 2 轮换到渠道 B，签名只在真实模型一致时跟过去。
func TestRelayChatSignatureAcrossChannels(t *testing.T) {
	rows := []struct {
		name          string
		realModelB    string
		fallbackB     string
		wantSignature string
	}{
		{name: "same real model", realModelB: signatureRealModel, fallbackB: "gemini", wantSignature: realSignature},
		{name: "different real model with gemini fallback", realModelB: "gemini-2.5", fallbackB: "gemini", wantSignature: placeholderSignature},
		{name: "different real model without fallback", realModelB: "gemini-2.5", wantSignature: ""},
	}
	for _, row := range rows {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", row.name, stream), func(t *testing.T) {
				// 1. 渠道 A、B 各有一个上游。
				upstreamA := newSignatureUpstream(t, false)
				upstreamB := newSignatureUpstream(t, false)
				attemptA := state.Attempt{Channel: newSignatureChannel(1, upstreamA.URL, "gemini"), RealModel: signatureRealModel}
				attemptB := state.Attempt{Channel: newSignatureChannel(2, upstreamB.URL, row.fallbackB), RealModel: row.realModelB}

				// 2. 第 1 轮走 A，第 2 轮走 B。
				client := signatureClient{protocol: llmkit.ProtocolClaude, stream: stream}
				calls, response := client.twoRounds(t, attemptA, attemptB)

				// 3. 第 2 轮只到 B，B 收到的签名按真实模型是否一致取舍。
				requireClientToolCalls(t, calls, response, true)
				require.Len(t, upstreamA.requests(), 1)
				requestsB := upstreamB.requests()
				require.Len(t, requestsB, 1)
				requireUpstreamToolRound(t, requestsB[0], row.wantSignature)
			})
		}
	}
}

// newGatewayChain 搭 AIGW1 → AIGW2 → upstreamURL 两层真实 native 中转，返回使用者面对的 AIGW1 attempt。
func newGatewayChain(t *testing.T, upstreamURL, fallback, outerModel, innerModel string) state.Attempt {
	t.Helper()
	inner := state.Attempt{Channel: newSignatureChannel(2, upstreamURL, fallback), RealModel: innerModel}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. AIGW2 把收到的请求交给 native 中转。
		body, err := io.ReadAll(r.Body)
		var request struct{ Stream bool }
		if err != nil || json.Unmarshal(body, &request) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		relay, recorder := newNativeTestCtx(t, body, llmkit.ProtocolOpenAIChat, request.Stream)
		result := (&Backend{}).Relay(relay, inner)

		// 2. 中转失败且没写出响应时回 502，否则把响应原样写回。
		if result.Err != nil && recorder.Body.Len() == 0 {
			http.Error(w, result.Err.Error(), http.StatusBadGateway)
			return
		}
		maps.Copy(w.Header(), recorder.Header())
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	t.Cleanup(gateway.Close)
	return state.Attempt{Channel: newSignatureChannel(1, gateway.URL, ""), RealModel: outerModel}
}

// 两层网关串联（使用者 → AIGW1 → AIGW2 → 假 Gemini）：每层各自核对自己的模型名，签名只在两层模型都没变时回到上游。
func TestRelayChatSignatureAcrossGateways(t *testing.T) {
	const outerModel, innerModel = "gemini-relay", signatureRealModel
	rows := []struct {
		name             string
		outerModelRound2 string
		innerModelRound2 string
		fallback         string
		wantSignature    string
	}{
		{name: "same models", outerModelRound2: outerModel, innerModelRound2: innerModel, wantSignature: realSignature},
		{name: "outer gateway switches model", outerModelRound2: "gemini-relay-other", innerModelRound2: innerModel, wantSignature: ""},
		{name: "inner gateway switches model with gemini fallback", outerModelRound2: outerModel, innerModelRound2: "gemini-2.5", fallback: "gemini", wantSignature: placeholderSignature},
	}
	for _, row := range rows {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", row.name, stream), func(t *testing.T) {
				// 1. 两轮都落到同一个假 Gemini，第 2 轮按表换某一层的真实模型。
				upstream := newSignatureUpstream(t, false)
				round1 := newGatewayChain(t, upstream.URL, row.fallback, outerModel, innerModel)
				round2 := newGatewayChain(t, upstream.URL, row.fallback, row.outerModelRound2, row.innerModelRound2)
				client := signatureClient{protocol: llmkit.ProtocolOpenAIChat, stream: stream}
				calls, response := client.twoRounds(t, round1, round2)

				// 2. 使用者拿到打包 ID；假 Gemini 第 2 次收到原始 ID，签名按两层模型是否一致取舍。
				requireClientToolCalls(t, calls, response, true)
				requests := upstream.requests()
				require.Len(t, requests, 2)
				requireUpstreamToolRound(t, requests[1], row.wantSignature)
			})
		}
	}
}

func TestRelayChatMissingToolSignatureFallback(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback=%t", enabled), func(t *testing.T) {
			// 1. 上游校验导入历史的签名和工具定义。
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var received map[string]any
				if json.NewDecoder(r.Body).Decode(&received) != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				requests <- received
				messages := received["messages"].([]any)
				calls := messages[0].(map[string]any)["tool_calls"].([]any)
				extra, _ := calls[0].(map[string]any)["extra_content"].(map[string]any)
				google, _ := extra["google"].(map[string]any)
				if google["thought_signature"] != "context_engineering_is_the_way_to_go" {
					http.Error(w, `{"error":{"message":"missing thought_signature"}}`, http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			channel := makeNativeChannel(server.URL)
			channel.SupportedAPITypes = `["chat-completion"]`
			fallback := "none"
			if enabled {
				fallback = "gemini"
			}
			channel.OtherSettings = fmt.Sprintf(`{"model_tool_schema_compatibility":[{"model_pattern":"^gemini-","profile":"gemini_compat","signature_fallback":%q}]}`, fallback)
			// 2. 回放没有签名的历史，工具结果仍使用原始 ID。
			body := `{"model":"gemini-4-argon","messages":[{"role":"assistant","reasoning_content":"old plan","tool_calls":[{"id":"call_imported","type":"function","function":{"name":"read","arguments":"{}"}},{"id":"call_parallel","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_imported","content":"ok"},{"role":"tool","tool_call_id":"call_parallel","content":"ok"}],"tools":[{"type":"function","function":{"name":"read","strict":true,"parameters":{"type":"number","exclusiveMinimum":0}}}]}`
			relay, recorder := newNativeTestCtx(t, []byte(body), llmkit.ProtocolOpenAIChat, false)
			result := (&Backend{}).Relay(relay, state.Attempt{Channel: channel, RealModel: "gemini-4-argon"})
			if enabled {
				require.NoError(t, result.Err)
				require.Contains(t, recorder.Body.String(), "done")
			} else {
				require.Error(t, result.Err)
			}
			// 3. 工具定义转换和签名兜底独立于思考文本回填。
			received := <-requests
			messages := received["messages"].([]any)
			assistant := messages[0].(map[string]any)
			require.NotContains(t, assistant, "reasoning_content")
			calls := assistant["tool_calls"].([]any)
			require.Equal(t, "call_imported", calls[0].(map[string]any)["id"])
			require.Equal(t, "call_imported", messages[1].(map[string]any)["tool_call_id"])
			require.NotContains(t, calls[1], "extra_content")
			definition := received["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
			require.NotContains(t, definition, "strict")
			require.NotContains(t, definition["parameters"], "exclusiveMinimum")
		})
	}
}
