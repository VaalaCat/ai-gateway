package codec_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

// TestRegression_CodexParallelToolCallsResponsesToChat 验证 Codex 场景：
// Responses API 输入中多个并行 function_call（无对应 function_call_output 紧跟）出现时，
// 转换为 Chat Completions 格式后，每个 assistant{tool_calls} 消息必须紧跟覆盖其所有
// tool_call_id 的 tool 消息，不得有任何其他 assistant 消息夹在中间。
func TestRegression_CodexParallelToolCallsResponsesToChat(t *testing.T) {
	body, err := os.ReadFile("testdata/regressions/codex_parallel_tool_calls_responses_to_chat.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	inbound := codec.GetInbound(codec.ProtocolOpenAIResponses)
	outbound := codec.GetOutbound(codec.ProtocolOpenAIChat)
	if inbound == nil || outbound == nil {
		t.Fatal("codecs not registered")
	}

	irReq, err := inbound.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "http://stub",
		APIKey:  "k",
		Model:   irReq.Model,
	}
	outHTTPReq, err := outbound.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	outBody, _ := io.ReadAll(outHTTPReq.Body)

	var decoded struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(outBody, &decoded); err != nil {
		t.Fatalf("outbound body not valid JSON: %v\n%s", err, outBody)
	}

	msgs := decoded.Messages

	// 断言：每个 role=assistant 且包含 tool_calls 的消息，其后必须紧跟覆盖
	// 所有 tool_call_id 的 tool 消息（不得有任何中间消息打断）。
	for i, msg := range msgs {
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		toolCalls, _ := msg["tool_calls"].([]any)
		if len(toolCalls) == 0 {
			continue
		}
		// 收集所有期望的 tool_call_id
		expectedIDs := map[string]bool{}
		for _, tc := range toolCalls {
			tcMap, _ := tc.(map[string]any)
			if id, ok := tcMap["id"].(string); ok {
				expectedIDs[id] = true
			}
		}
		// 检查后续消息是否紧跟对应 tool 消息
		j := i + 1
		coveredIDs := map[string]bool{}
		for j < len(msgs) {
			nextRole, _ := msgs[j]["role"].(string)
			if nextRole != "tool" {
				break
			}
			callID, _ := msgs[j]["tool_call_id"].(string)
			if expectedIDs[callID] {
				coveredIDs[callID] = true
			}
			j++
		}
		// 确认中间没有其他角色（即 i+1..j-1 均为 tool）
		for k := i + 1; k < j; k++ {
			if r, _ := msgs[k]["role"].(string); r != "tool" {
				t.Errorf("messages[%d] (assistant with tool_calls) is followed by non-tool message at index %d (role=%q)", i, k, r)
			}
		}
		// 检查所有 tool_call_id 均被覆盖
		for id := range expectedIDs {
			if !coveredIDs[id] {
				t.Errorf("tool_call_id %q has no corresponding tool message immediately after assistant[%d]", id, i)
			}
		}
		// 断言：在当前 assistant{tool_calls} 的 tool 覆盖窗口（i+1..j-1）内，
		// 不得出现其他 role=assistant 且含 tool_calls 的消息（并行调用未合并的回归）。
		for k := i + 1; k < j; k++ {
			r, _ := msgs[k]["role"].(string)
			if r == "assistant" {
				tcs, _ := msgs[k]["tool_calls"].([]any)
				if len(tcs) > 0 {
					t.Errorf("messages[%d] (assistant with tool_calls) is followed by another assistant with tool_calls at index %d within coverage window [%d,%d) — parallel tool calls not merged", i, k, i+1, j)
				}
			}
		}
	}
}

// TestRegression_CodexPreambleTextInToolCall 验证 Codex 场景：
// Responses API 输入中 function_call 和 function_call_output 之间夹着一个 assistant 文本消息（前言），
// 转换为 Chat Completions 格式后消息顺序必须合法：
// assistant{tool_calls} 必须紧跟对应的 tool 消息，不得有任何中间消息打断。
func TestRegression_CodexPreambleTextInToolCall(t *testing.T) {
	body, err := os.ReadFile("testdata/regressions/codex_preamble_text_in_tool_call_responses_to_chat.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	inbound := codec.GetInbound(codec.ProtocolOpenAIResponses)
	outbound := codec.GetOutbound(codec.ProtocolOpenAIChat)
	if inbound == nil || outbound == nil {
		t.Fatal("codecs not registered")
	}

	irReq, err := inbound.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "http://stub",
		APIKey:  "k",
		Model:   irReq.Model,
	}
	outHTTPReq, err := outbound.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	outBody, _ := io.ReadAll(outHTTPReq.Body)

	var decoded struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(outBody, &decoded); err != nil {
		t.Fatalf("outbound body not valid JSON: %v\n%s", err, outBody)
	}

	msgs := decoded.Messages

	// 断言 1：每个 role=assistant 且包含 tool_calls 的消息，其后必须紧跟覆盖
	// 所有 tool_call_id 的 tool 消息（不得有任何中间消息打断）。
	for i, msg := range msgs {
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		toolCalls, _ := msg["tool_calls"].([]any)
		if len(toolCalls) == 0 {
			continue
		}
		// 收集所有期望的 tool_call_id
		expectedIDs := map[string]bool{}
		for _, tc := range toolCalls {
			tcMap, _ := tc.(map[string]any)
			if id, ok := tcMap["id"].(string); ok {
				expectedIDs[id] = true
			}
		}
		// 检查后续消息是否紧跟对应 tool 消息
		j := i + 1
		coveredIDs := map[string]bool{}
		for j < len(msgs) {
			nextRole, _ := msgs[j]["role"].(string)
			if nextRole != "tool" {
				break
			}
			callID, _ := msgs[j]["tool_call_id"].(string)
			if expectedIDs[callID] {
				coveredIDs[callID] = true
			}
			j++
		}
		// 确认中间没有其他角色（即 j 之前均为 tool）
		for k := i + 1; k < j; k++ {
			if r, _ := msgs[k]["role"].(string); r != "tool" {
				t.Errorf("messages[%d] (assistant with tool_calls) is followed by non-tool message at index %d (role=%q)", i, k, r)
			}
		}
		// 检查所有 tool_call_id 均被覆盖
		for id := range expectedIDs {
			if !coveredIDs[id] {
				t.Errorf("tool_call_id %q has no corresponding tool message immediately after assistant[%d]", id, i)
			}
		}
	}

	// 断言 2：合并后的前言文本应出现在对应 assistant{tool_calls} 消息的 content 中
	found := false
	for _, msg := range msgs {
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		toolCalls, _ := msg["tool_calls"].([]any)
		if len(toolCalls) == 0 {
			continue
		}
		// 检查这个 assistant 消息的 content 是否包含前言文本
		content := msg["content"]
		contentStr := ""
		switch v := content.(type) {
		case string:
			contentStr = v
		case []any:
			for _, block := range v {
				blockMap, _ := block.(map[string]any)
				if blockMap != nil {
					if text, ok := blockMap["text"].(string); ok {
						contentStr += text
					}
				}
			}
		}
		if strings.Contains(contentStr, "README is missing") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("preamble text 'README is missing' not found in any assistant{tool_calls} message content\nMessages: %s", outBody)
	}
}

func TestRegression_CodexWebSearchResponsesToChat(t *testing.T) {
	body, err := os.ReadFile("testdata/regressions/codex_web_search_responses_to_chat.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	inbound := codec.GetInbound(codec.ProtocolOpenAIResponses)
	outbound := codec.GetOutbound(codec.ProtocolOpenAIChat)
	if inbound == nil || outbound == nil {
		t.Fatal("codecs not registered")
	}

	irReq, err := inbound.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "http://stub",
		APIKey:  "k",
		Model:   irReq.Model,
		// BuiltinToolFallback 空 → 归一到 drop（默认）
	}
	outReq, err := outbound.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	outBody, _ := io.ReadAll(outReq.Body)

	var decoded struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(outBody, &decoded); err != nil {
		t.Fatalf("outbound body not valid JSON: %v\n%s", err, outBody)
	}

	// 核心断言 1：没有任何 type=function 的条目 function.name 为空
	for i, tool := range decoded.Tools {
		typ, _ := tool["type"].(string)
		if typ != "function" {
			continue
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			t.Errorf("tool[%d] missing function object: %v", i, tool)
			continue
		}
		name, _ := fn["name"].(string)
		if strings.TrimSpace(name) == "" {
			t.Fatalf("tool[%d] has empty function.name — regression: %s", i, outBody)
		}
	}

	// 核心断言 2：req.Metadata["dropped_tools"] 含恰好 1 条 web_search
	dropped, ok := irReq.Metadata["dropped_tools"].([]codec.DroppedTool)
	if !ok {
		t.Fatalf("expected dropped_tools metadata, got %v", irReq.Metadata)
	}
	if len(dropped) != 1 || dropped[0].Type != "web_search" {
		t.Errorf("want exactly one dropped web_search tool, got %+v", dropped)
	}
}

// TestRegression_CodexToolCallChatStreamToResponses 验证 Codex bug 报告中的场景：
// chat 协议上游以流式返回 tool_call（function call），转换到 Responses SSE 输出后
// 事件序列必须正确（有 output_item.added / function_call_arguments.delta /
// output_item.done，且无空 output_text.delta、无无名 message 输出项）。
func TestRegression_CodexToolCallChatStreamToResponses(t *testing.T) {
	const (
		fixtureDir  = "testdata/regressions"
		requestFile = "codex_tool_call_chat_stream_to_responses.request.json"
		upstreamSSE = "codex_tool_call_chat_stream_to_responses.upstream.sse"

		wantCallID   = "call_00_N004LkQOxnskHCBWvmvpsp3E"
		wantFuncName = "exec_command"
		wantArgs     = `{"cmd":"uptime"}`
	)

	// 1. 加载 inbound 请求 fixture（Responses 格式）并解析 IR 请求。
	reqBody, err := os.ReadFile(fixtureDir + "/" + requestFile)
	if err != nil {
		t.Fatalf("read request fixture: %v", err)
	}
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	respInbound := codec.GetInbound(codec.ProtocolOpenAIResponses)
	chatOutbound := codec.GetOutbound(codec.ProtocolOpenAIChat)
	if respInbound == nil || chatOutbound == nil {
		t.Fatal("codecs not registered")
	}

	irReq, err := respInbound.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	_ = irReq // irReq used for DecodeRequest validation only

	// 2. 将上游 chat SSE fixture 喂给 chat outbound codec 解码为 IR 事件流。
	sseBytes, err := os.ReadFile(fixtureDir + "/" + upstreamSSE)
	if err != nil {
		t.Fatalf("read upstream SSE fixture: %v", err)
	}
	upstreamResp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(sseBytes)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	eventsCh, err := chatOutbound.DecodeResponse(upstreamResp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var irEvents []codec.Event
	for ev := range eventsCh {
		irEvents = append(irEvents, ev)
	}

	// 3. 核心断言：IR 事件流满足 tool_call 不变式（Start → ArgsDelta* → End）。
	if err := codec.AssertStreamingToolCallInvariant(irEvents); err != nil {
		t.Fatalf("AssertStreamingToolCallInvariant: %v", err)
	}

	// 4. 将 IR 事件重新编码为 Responses SSE 输出。
	replayCh := make(chan codec.Event, len(irEvents))
	for _, ev := range irEvents {
		replayCh <- ev
	}
	close(replayCh)

	rec := &flushRecorder{httptest.NewRecorder(), 0}
	if err := respInbound.EncodeResponse(replayCh, rec, true); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	outputSSE := rec.Body.String()

	// 5. 解析输出 SSE 事件以便逐条断言。
	type sseEvent struct {
		eventType string
		data      map[string]any
	}
	var outputEvents []sseEvent
	scanner := bufio.NewScanner(strings.NewReader(outputSSE))
	var curEventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			curEventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			raw := strings.TrimPrefix(line, "data: ")
			var obj map[string]any
			if err := json.Unmarshal([]byte(raw), &obj); err != nil {
				t.Errorf("invalid JSON in SSE data line: %s", raw)
				continue
			}
			outputEvents = append(outputEvents, sseEvent{eventType: curEventType, data: obj})
			curEventType = ""
		}
	}

	// 辅助：按事件类型筛选
	filterEvents := func(typ string) []sseEvent {
		var out []sseEvent
		for _, ev := range outputEvents {
			if ev.eventType == typ {
				out = append(out, ev)
			}
		}
		return out
	}

	// ---------- 断言 A：恰好 1 个 response.output_item.added（function_call）----------
	addedEvents := filterEvents("response.output_item.added")
	var funcCallAdded []sseEvent
	for _, ev := range addedEvents {
		item, _ := ev.data["item"].(map[string]any)
		if item == nil {
			continue
		}
		if item["type"] == "function_call" {
			funcCallAdded = append(funcCallAdded, ev)
		}
	}
	if len(funcCallAdded) != 1 {
		t.Fatalf("response.output_item.added for function_call: got %d, want 1\nSSE output:\n%s", len(funcCallAdded), outputSSE)
	}
	addedItem, _ := funcCallAdded[0].data["item"].(map[string]any)
	if gotCallID, _ := addedItem["call_id"].(string); gotCallID != wantCallID {
		t.Errorf("output_item.added call_id: got %q, want %q", gotCallID, wantCallID)
	}
	if gotName, _ := addedItem["name"].(string); gotName != wantFuncName {
		t.Errorf("output_item.added name: got %q, want %q", gotName, wantFuncName)
	}
	// fc_ id 不应以空后缀结尾（如 "fc_"）
	if fcID, _ := addedItem["id"].(string); fcID == "fc_" || fcID == "" {
		t.Errorf("output_item.added item.id is empty or bare 'fc_': %q", fcID)
	}
	t.Logf("output_item.added: call_id=%q name=%q id=%q", addedItem["call_id"], addedItem["name"], addedItem["id"])

	// ---------- 断言 B：恰好 1 个 response.output_item.done（function_call，status=completed）----------
	doneEvents := filterEvents("response.output_item.done")
	var funcCallDone []sseEvent
	for _, ev := range doneEvents {
		item, _ := ev.data["item"].(map[string]any)
		if item == nil {
			continue
		}
		if item["type"] == "function_call" {
			funcCallDone = append(funcCallDone, ev)
		}
	}
	if len(funcCallDone) != 1 {
		t.Fatalf("response.output_item.done for function_call: got %d, want 1", len(funcCallDone))
	}
	doneItem, _ := funcCallDone[0].data["item"].(map[string]any)
	if status, _ := doneItem["status"].(string); status != "completed" {
		t.Errorf("output_item.done status: got %q, want completed", status)
	}
	t.Logf("output_item.done: status=%q", doneItem["status"])

	// ---------- 断言 C：至少 1 个 response.function_call_arguments.delta ----------
	argsDelta := filterEvents("response.function_call_arguments.delta")
	if len(argsDelta) < 1 {
		t.Fatalf("response.function_call_arguments.delta: got 0, want >= 1\nSSE:\n%s", outputSSE)
	}
	t.Logf("response.function_call_arguments.delta count = %d", len(argsDelta))

	// ---------- 断言 D：恰好 1 个 response.function_call_arguments.done ----------
	argsDoneEvents := filterEvents("response.function_call_arguments.done")
	if len(argsDoneEvents) != 1 {
		t.Fatalf("response.function_call_arguments.done: got %d, want 1", len(argsDoneEvents))
	}

	// ---------- 断言 E：累积参数 delta == wantArgs，且是合法 JSON ----------
	var cumArgs strings.Builder
	for _, ev := range argsDelta {
		delta, _ := ev.data["delta"].(map[string]any)
		if delta != nil {
			if text, ok := delta["text"].(string); ok {
				cumArgs.WriteString(text)
			}
		}
	}
	gotArgs := cumArgs.String()
	if gotArgs != wantArgs {
		t.Errorf("cumulative arguments delta: got %q, want %q", gotArgs, wantArgs)
	}
	var argsObj any
	if err := json.Unmarshal([]byte(gotArgs), &argsObj); err != nil {
		t.Errorf("cumulative arguments delta is not valid JSON: %v — args=%q", err, gotArgs)
	}
	t.Logf("cumulative arguments delta = %q", gotArgs)

	// ---------- 断言 F：没有空文本的 response.output_text.delta ----------
	textDelta := filterEvents("response.output_text.delta")
	for i, ev := range textDelta {
		delta, _ := ev.data["delta"].(map[string]any)
		text := ""
		if delta != nil {
			text, _ = delta["text"].(string)
		}
		if text == "" {
			t.Errorf("output_text.delta[%d] has empty text — regression", i)
		}
	}

	// ---------- 断言 G：没有 output_item.added 类型为 message（纯 tool_call 响应）----------
	for _, ev := range addedEvents {
		item, _ := ev.data["item"].(map[string]any)
		if item == nil {
			continue
		}
		if item["type"] == "message" {
			t.Errorf("unexpected output_item.added with type=message in pure tool_call response: %v", ev.data)
		}
	}
}

// TestRegression_ProtocolOverride_ResponsesToClaude 验证 protocol override 路径：
// inbound openai_responses → IR → outbound claude 的端到端转换必须保留 messages
// 与 tools；尤其是 tool 元素的 name 字段不得为空（防止 7d0ee4a / 95d31eb / 625be6c
// 这一类 assistant tool_calls / function name 丢失的回归）。
func TestRegression_ProtocolOverride_ResponsesToClaude(t *testing.T) {
	body, err := os.ReadFile("testdata/regressions/protocol_override_responses_to_claude.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	inbound := codec.GetInbound(codec.ProtocolOpenAIResponses)
	if inbound == nil {
		t.Fatal("inbound responses codec not registered")
	}
	irReq, err := inbound.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	outbound := codec.GetOutbound(codec.ProtocolClaude)
	if outbound == nil {
		t.Fatal("outbound claude codec not registered")
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "http://stub",
		APIKey:  "k",
		Model:   "test-model",
	}
	outHTTPReq, err := outbound.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if outHTTPReq == nil || outHTTPReq.Body == nil {
		t.Fatal("nil outbound request/body")
	}

	outBody, err := io.ReadAll(outHTTPReq.Body)
	if err != nil {
		t.Fatalf("read out body: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(outBody, &got); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, string(outBody))
	}

	msgs, ok := got["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("messages missing or empty: %v", got)
	}

	tools, ok := got["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools missing or empty: %v", got)
	}
	for i, tl := range tools {
		m, ok := tl.(map[string]any)
		if !ok {
			t.Fatalf("tools[%d] not an object: %v", i, tl)
		}
		name, _ := m["name"].(string)
		if name == "" {
			t.Fatalf("tools[%d].name is empty: %v", i, m)
		}
	}
}

// TestRegression_ModelProtocolOverride_ExactResponsesToChat 验证模型级 override
// 路径：精确模型 'gpt-4o' inbound openai_responses → IR → outbound openai_chat
// 转换链路上 messages 与 tools 不丢，function name 不为空。
func TestRegression_ModelProtocolOverride_ExactResponsesToChat(t *testing.T) {
	body, err := os.ReadFile("testdata/regressions/model_protocol_override_exact_responses_to_chat.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	inbound := codec.GetInbound(codec.ProtocolOpenAIResponses)
	if inbound == nil {
		t.Fatal("inbound responses codec not registered")
	}
	irReq, err := inbound.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	outbound := codec.GetOutbound(codec.ProtocolOpenAIChat)
	if outbound == nil {
		t.Fatal("outbound chat codec not registered")
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "gpt-4o"}
	outHTTPReq, err := outbound.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if outHTTPReq == nil || outHTTPReq.Body == nil {
		t.Fatal("nil outbound request/body")
	}

	outBody, err := io.ReadAll(outHTTPReq.Body)
	if err != nil {
		t.Fatalf("read out body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(outBody, &got); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, string(outBody))
	}

	msgs, ok := got["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("messages missing or empty: %v", got)
	}
	tools, ok := got["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools missing or empty: %v", got)
	}
	for i, tl := range tools {
		m, ok := tl.(map[string]any)
		if !ok {
			t.Fatalf("tools[%d] not an object: %v", i, tl)
		}
		// OpenAI Chat 协议 tool 结构是 {type: function, function: {name, ...}}
		fn, _ := m["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "" {
			t.Fatalf("tools[%d].function.name is empty: %v", i, m)
		}
	}
}

// TestRegression_ModelProtocolOverride_RegexWildcardToClaude 验证：
// 正则模型 'deepseek-.*' + wildcard inbound 命中 'deepseek-r1' 后，
// inbound openai_chat → outbound claude 的转换链路上 messages 与 tools
// 不丢，且最终 body 是合法 Anthropic Messages 请求体。
func TestRegression_ModelProtocolOverride_RegexWildcardToClaude(t *testing.T) {
	body, err := os.ReadFile("testdata/regressions/model_protocol_override_regex_wildcard_to_claude.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	inbound := codec.GetInbound(codec.ProtocolOpenAIChat)
	if inbound == nil {
		t.Fatal("inbound chat codec not registered")
	}
	irReq, err := inbound.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	outbound := codec.GetOutbound(codec.ProtocolClaude)
	if outbound == nil {
		t.Fatal("outbound claude codec not registered")
	}
	cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: "deepseek-r1"}
	outHTTPReq, err := outbound.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	outBody, err := io.ReadAll(outHTTPReq.Body)
	if err != nil {
		t.Fatalf("read out body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(outBody, &got); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, string(outBody))
	}
	msgs, ok := got["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("messages missing or empty: %v", got)
	}
	tools, ok := got["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools missing or empty: %v", got)
	}
	for i, tl := range tools {
		m, ok := tl.(map[string]any)
		if !ok {
			t.Fatalf("tools[%d] not an object: %v", i, tl)
		}
		// Anthropic 协议 tool 结构 top-level: {name, description, input_schema}
		name, _ := m["name"].(string)
		if name == "" {
			t.Fatalf("tools[%d].name is empty: %v", i, m)
		}
	}
}
