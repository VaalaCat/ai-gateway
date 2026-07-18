package native

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// capturedUpstream 记录上游 server 收到的请求,供断言。
type capturedUpstream struct {
	Path   string
	Header http.Header
	Body   map[string]any
}

// runRelayCapture 跑一次 native.Relay,返回上游实际收到的请求。
// 上游 server 固定回一个最小可解码的 openai chat 响应,保证 Relay 不在 decode 响应处早退;
// 即便响应处理出错也无妨——我们只断言"上游收到的请求"(它在发送时即被记录)。
func runRelayCapture(t *testing.T, ch *models.Channel, reqBody string, inbound codec.Protocol, realModel string) capturedUpstream {
	t.Helper()
	var cap capturedUpstream
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.Path = r.URL.Path
		cap.Header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &cap.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 最小 openai chat 非流式响应
		w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)

	ch.BaseURL = srv.URL
	rctx, _ := newNativeTestCtx(t, []byte(reqBody), inbound, false)
	b := &Backend{}
	b.Relay(rctx, state.Attempt{Channel: ch, RealModel: realModel})
	return cap
}

// chatBody 是一个最小 openai_chat 入站请求。
const chatBody = `{"model":"req-model","messages":[{"role":"user","content":"hi"}]}`

func TestChar_ModelMapping_RemapsUpstreamModel(t *testing.T) {
	ch := &models.Channel{
		ChannelCore:  models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1},
		Key:          "k",
		Models:       "real-model",
		ModelMapping: `{"real-model":"upstream-model"}`,
	}
	cap := runRelayCapture(t, ch, chatBody, codec.ProtocolOpenAIChat, "real-model")
	if cap.Body["model"] != "upstream-model" {
		t.Fatalf("upstream model = %v, want upstream-model", cap.Body["model"])
	}
}

func TestChar_ModelMapping_NoMappingKeepsRealModel(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1},
		Key:         "k",
		Models:      "real-model",
	}
	cap := runRelayCapture(t, ch, chatBody, codec.ProtocolOpenAIChat, "real-model")
	if cap.Body["model"] != "real-model" {
		t.Fatalf("upstream model = %v, want real-model (no mapping)", cap.Body["model"])
	}
}

func TestChar_SystemPrompt_Injected(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			SystemPrompt: "BE NICE"},
		Key:    "k",
		Models: "real-model",
	}
	cap := runRelayCapture(t, ch, chatBody, codec.ProtocolOpenAIChat, "real-model")
	msgs, _ := cap.Body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages in upstream body")
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("first message role = %v, want system (injected)", first["role"])
	}
}

func TestChar_RoleMapping_DefaultRemap(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			RoleMapping: `{"default":{"system":"user"}}`,
		},
		Key:    "k",
		Models: "req-model",
	}
	body := `{"model":"req-model","messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`
	cap := runRelayCapture(t, ch, body, codec.ProtocolOpenAIChat, "req-model")
	msgs, _ := cap.Body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages in upstream body")
	}
	// 原 system 消息应被改写为 user
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["content"] == "s" {
			if mm["role"] != "user" {
				t.Fatalf("original system message role = %v, want user (remapped by default mapping)", mm["role"])
			}
			return
		}
	}
	t.Fatal("could not find the original system message content 's' in upstream body")
}

func TestChar_RoleMapping_ByRequestedModel(t *testing.T) {
	// RoleMapping 按 cfg.InboundModel(入站模型名,即 realModel) 匹配,不按映射后的 upstream-model 匹配。
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			RoleMapping: `{"models":{"real-model":{"system":"developer"}}}`,
		},
		Key:          "k",
		Models:       "real-model",
		ModelMapping: `{"real-model":"upstream-model"}`,
	}
	body := `{"model":"real-model","messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`
	cap := runRelayCapture(t, ch, body, codec.ProtocolOpenAIChat, "real-model")
	msgs, _ := cap.Body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages in upstream body")
	}
	// 原 system 消息应被改写为 developer (per-model mapping matched by cfg.InboundModel=real-model, not upstream-model)
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["content"] == "s" {
			if mm["role"] != "developer" {
				t.Fatalf("original system message role = %v, want developer (per-model mapping matched by real-model)", mm["role"])
			}
			return
		}
	}
	t.Fatal("could not find the original system message content 's' in upstream body")
}

func TestChar_Thinking_StripWhenOff(t *testing.T) {
	// claude 入站带 assistant thinking block,出站 openai_chat,channel 无 thinking 规则:
	// 断言上游 assistant 消息无 reasoning_content。
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			SupportedAPITypes: `["chat-completion"]`},
		Key:    "k",
		Models: "claude-3-7",
	}
	// claude /v1/messages 格式入站,assistant 消息含 thinking block
	claudeBody := `{
		"model": "claude-3-7",
		"max_tokens": 100,
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "private thoughts"},
				{"type": "text", "text": "answer"}
			]}
		]
	}`
	cap := runRelayCapture(t, ch, claudeBody, codec.ProtocolClaude, "claude-3-7")
	msgs, _ := cap.Body["messages"].([]any)
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["role"] == "assistant" {
			if _, present := mm["reasoning_content"]; present {
				t.Fatalf("reasoning_content should be stripped when thinking is off, but found: %v", mm["reasoning_content"])
			}
			return
		}
	}
	// After stripping the thinking block, the text block remains, so the
	// assistant message must appear in the upstream body.
	t.Fatal("assistant message missing from upstream body after thinking strip — stripping may have dropped the entire message")
}

func TestChar_ParamOverride_MergedIntoBody(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			ParamOverride: `{"temperature":0.5}`,
		},
		Key:    "k",
		Models: "real-model",
	}
	cap := runRelayCapture(t, ch, chatBody, codec.ProtocolOpenAIChat, "real-model")
	temp, ok := cap.Body["temperature"].(float64)
	if !ok {
		t.Fatalf("temperature not found or wrong type in upstream body: %v", cap.Body["temperature"])
	}
	if temp != 0.5 {
		t.Fatalf("upstream temperature = %v, want 0.5", temp)
	}
}

func TestChar_HeaderOverride_SetOnRequest(t *testing.T) {
	ch := &models.Channel{
		ChannelCore:    models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1},
		Key:            "k",
		Models:         "real-model",
		HeaderOverride: `{"X-Test":"v"}`,
	}
	cap := runRelayCapture(t, ch, chatBody, codec.ProtocolOpenAIChat, "real-model")
	if cap.Header.Get("X-Test") != "v" {
		t.Fatalf("X-Test header = %q, want v", cap.Header.Get("X-Test"))
	}
}

// TestChar_ProtocolOverride 锁定 OtherSettings.model_protocol_override 驱动的
// 出站协议协商:openai_chat 入站 → 强制 claude 出站。断言上游 PATH 为 claude 的
// /v1/messages,证明协议协商 + 匹配的 outbound codec 仍把请求送过新 dataflow 路径。
//
// JSON 形状(见 upstream/overrides.go parseModelProtocolOverride / codec.go
// NegotiateOutboundProtocol):overrides 的 key/value 是 codec.Protocol 字符串值
// (openai_chat / openai_responses / claude),不是 endpoint key。
// endpoints / supportedAPITypes 都不设 → isReachable 返回 true,override 生效。
func TestChar_ProtocolOverride(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			OtherSettings: `{"model_protocol_override":[{"model":"real-model","overrides":{"openai_chat":"claude"}}]}`,
		},
		Key:    "k",
		Models: "real-model",
	}
	cap := runRelayCapture(t, ch, chatBody, codec.ProtocolOpenAIChat, "real-model")
	if cap.Path != "/v1/messages" {
		t.Fatalf("upstream path = %q, want /v1/messages (claude outbound forced by protocol override)", cap.Path)
	}
	// claude /v1/messages body 必带 max_tokens 字段(openai_chat 入站没有,
	// 由 claude outbound codec 注入默认值),进一步证明走的是 claude 出站 codec。
	if _, ok := cap.Body["max_tokens"]; !ok {
		t.Fatalf("claude outbound body should carry max_tokens, got body keys: %v", cap.Body)
	}
}

// behavior change: Chat Completions image_url content must survive the full
// native dataflow when the selected upstream protocol is Responses.
func TestChar_ChatToResponsesPreservesImageContent(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			OtherSettings: `{"model_protocol_override":[{"model":"real-model","overrides":{"openai_chat":"openai_responses"}}]}`,
		},
		Key:    "k",
		Models: "real-model",
	}

	tests := []struct {
		name      string
		content   string
		wantTypes []string
		wantURL   string
	}{
		{
			name:      "data URL after text",
			content:   `[{"type":"text","text":"Describe this image in one sentence."},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,/9j/4AAQ"}}]`,
			wantTypes: []string{"input_text", "input_image"},
			wantURL:   "data:image/jpeg;base64,/9j/4AAQ",
		},
		{
			name:      "remote URL after text",
			content:   `[{"type":"text","text":"Describe this image."},{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]`,
			wantTypes: []string{"input_text", "input_image"},
			wantURL:   "https://example.com/image.png",
		},
		{
			name:      "image only boundary",
			content:   `[{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]`,
			wantTypes: []string{"input_image"},
			wantURL:   "data:image/png;base64,iVBORw0KGgo=",
		},
		{
			name:      "malformed data URL remains visible to upstream validation",
			content:   `[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64"}}]`,
			wantTypes: []string{"input_image"},
			wantURL:   "data:image/jpeg;base64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"model":"real-model","messages":[{"role":"user","content":` + tt.content + `}]}`
			cap := runRelayCapture(t, ch, body, codec.ProtocolOpenAIChat, "real-model")
			if cap.Path != "/v1/responses" {
				t.Fatalf("upstream path = %q, want /v1/responses", cap.Path)
			}
			input, ok := cap.Body["input"].([]any)
			if !ok || len(input) != 1 {
				t.Fatalf("upstream input = %#v, want one message", cap.Body["input"])
			}
			message, ok := input[0].(map[string]any)
			if !ok {
				t.Fatalf("input[0] = %#v, want message object", input[0])
			}
			content, ok := message["content"].([]any)
			if !ok || len(content) != len(tt.wantTypes) {
				t.Fatalf("upstream content = %#v, want types %v", message["content"], tt.wantTypes)
			}
			for i, wantType := range tt.wantTypes {
				block, ok := content[i].(map[string]any)
				if !ok || block["type"] != wantType {
					t.Fatalf("content[%d] = %#v, want type %q", i, content[i], wantType)
				}
			}
			image := content[len(content)-1].(map[string]any)
			if image["image_url"] != tt.wantURL {
				t.Fatalf("image_url = %#v, want %q", image["image_url"], tt.wantURL)
			}
		})
	}
}

// behavior change: function fallback makes partial Responses implementations
// usable by converting named custom tools and dropping tools with no callable
// function identity. This reproduces the tools[4].function validation failure
// returned by SGLang-backed Responses compatibility endpoints.
func TestChar_ResponsesFunctionToolFallback(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1,
			SupportedAPITypes: `["responses"]`,
			Endpoints:         `{"responses":"/api/v1/responses"}`,
			OtherSettings:     `{"builtin_tool_fallback":"function"}`,
		},
		Key:    "k",
		Models: "glm-5.2",
	}
	body := `{
		"model":"gpt-5.5",
		"input":[
			{"type":"custom_tool_call","call_id":"call_previous","name":"apply_patch","input":"*** Begin Patch\n*** End Patch"},
			{"type":"custom_tool_call_output","call_id":"call_previous","output":"Done"},
			{"role":"user","content":[{"type":"input_text","text":"continue"}]}
		],
		"tools":[
			{"type":"function","name":"exec_command","description":"Run a command","parameters":{"type":"object"}},
			{"type":"custom","name":"apply_patch","description":"Edit files","format":{"type":"grammar","syntax":"lark","definition":"start: patch"}},
			{"type":"web_search","external_web_access":false}
		]
	}`

	cap := runRelayCapture(t, ch, body, codec.ProtocolOpenAIResponses, "glm-5.2")
	if cap.Path != "/api/v1/responses" {
		t.Fatalf("upstream path = %q, want /api/v1/responses", cap.Path)
	}
	tools, ok := cap.Body["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("upstream tools = %#v, want exec_command and converted apply_patch", cap.Body["tools"])
	}
	for i, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok || tool["type"] != "function" {
			t.Fatalf("tools[%d] = %#v, want function tool", i, raw)
		}
	}
	applyPatch := tools[1].(map[string]any)
	if applyPatch["name"] != "apply_patch" {
		t.Fatalf("converted tool name = %#v, want apply_patch", applyPatch["name"])
	}
	parameters, ok := applyPatch["parameters"].(map[string]any)
	if !ok || parameters["type"] != "object" {
		t.Fatalf("converted parameters = %#v, want object schema", applyPatch["parameters"])
	}
	input, ok := cap.Body["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("upstream input = %#v, want converted custom call/output plus user message", cap.Body["input"])
	}
	for i, wantType := range []string{"function_call", "function_call_output", "message"} {
		item, ok := input[i].(map[string]any)
		if !ok || item["type"] != wantType {
			t.Fatalf("input[%d] = %#v, want type %q", i, input[i], wantType)
		}
	}
}

func TestChar_ResponsesFunctionFallbackRestoresCustomToolCall(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[stream], func(t *testing.T) {
			var upstreamBody string
			if stream {
				upstreamBody = strings.Join([]string{
					`event: response.created`,
					`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"glm-5.2"}}`,
					``,
					`event: response.output_item.added`,
					`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_patch","name":"apply_patch","arguments":""}}`,
					``,
					`event: response.function_call_arguments.delta`,
					`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"input\":\"*** Begin Patch\\n"}`,
					``,
					`event: response.function_call_arguments.done`,
					`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"input\":\"*** Begin Patch\\n*** End Patch\"}"}`,
					``,
					`event: response.output_item.done`,
					`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_patch","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\\n*** End Patch\"}"}}`,
					``,
					`event: response.completed`,
					`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"glm-5.2"}}`,
					``,
				}, "\n")
			} else {
				upstreamBody = `{"id":"resp_1","object":"response","status":"completed","model":"glm-5.2","output":[{"type":"function_call","id":"fc_1","call_id":"call_patch","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\\n*** End Patch\"}"}]}`
			}

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(upstreamBody))
			}))
			defer upstream.Close()

			ch := &models.Channel{
				ChannelCore: models.ChannelCore{
					ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1,
					SupportedAPITypes: `["responses"]`,
					OtherSettings:     `{"builtin_tool_fallback":"function"}`,
				},
				Key: "k", Models: "glm-5.2",
			}
			body := []byte(`{"model":"gpt-5.5","stream":` + strconv.FormatBool(stream) + `,"input":"edit","tools":[{"type":"custom","name":"apply_patch","description":"Edit files","format":{"type":"grammar"}}]}`)
			rctx, recorder := newNativeTestCtx(t, body, codec.ProtocolOpenAIResponses, stream)

			result := (&Backend{}).Relay(rctx, state.Attempt{Channel: ch, RealModel: "glm-5.2"})
			if result.Err != nil {
				t.Fatalf("Relay: %v", result.Err)
			}
			got := recorder.Body.String()
			if !strings.Contains(got, `"type":"custom_tool_call"`) {
				t.Fatalf("client response did not restore custom_tool_call: %s", got)
			}
			if !strings.Contains(got, `"input":"*** Begin Patch\n*** End Patch"`) {
				t.Fatalf("client response did not unwrap custom tool input: %s", got)
			}
			if strings.Contains(got, `"type":"function_call"`) {
				t.Fatalf("client response leaked fallback function_call: %s", got)
			}
		})
	}
}

// TestChar_ResponsesInbound 通过 openai-responses 入站协议跑一次 relay,断言上游
// 请求 body 形状正确(含期望 model)。验证 responses 入站 codec 仍能在重连后的
// dataflow 路径上正常解码 + 编码。
//
// 注:responses 入站 body 形状是 {"model":...,"input":[{"role":...,"content":...}]}
// (见 codec/openai/responses_decode.go);DecodeRequest 直接读 r.Body,与 gateway
// path 无关,故 newNativeTestCtx 对 responses 用默认 path 不影响解码。
func TestChar_ResponsesInbound(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, Status: 1, Weight: 1},
		Key:         "k",
		Models:      "real-model",
	}
	respBody := `{"model":"req-model","input":[{"role":"user","content":"hi"}]}`
	cap := runRelayCapture(t, ch, respBody, codec.ProtocolOpenAIResponses, "real-model")
	if cap.Body["model"] != "real-model" {
		t.Fatalf("upstream model = %v, want real-model", cap.Body["model"])
	}
	// openai_chat 出站(channel 默认协商):body 应带 messages 数组。
	msgs, _ := cap.Body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("upstream body missing messages array, got keys: %v", cap.Body)
	}
}

// TestChar_ForwardClientHeaders_E2E 验证 StepForwardClientHeaders 在真实 Relay 链路中
// 的端到端行为:
//   - 客户端自定义头 X-Foo 透传到上游(值完整)
//   - 客户端 User-Agent 透传到上游(不被 Go runtime 默认 "Go-http-client/..." 替换)
//   - 客户端 x-api-key 被剥离(凭证不泄漏给上游)
//
// 采用与其他端到端测试相同的内联风格：直接用 newNativeTestCtx 构造 rctx，
// 在发送前向 c.Request.Header 注入入站头，再启动上游 httptest.Server 捕获请求头。
func TestChar_ForwardClientHeaders_E2E(t *testing.T) {
	var capturedHeader http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)

	// 注入入站头，模拟客户端请求携带自定义头、User-Agent 以及凭证。
	rctx.Context.Request.Header.Set("X-Foo", "bar")
	rctx.Context.Request.Header.Set("User-Agent", "my-client/1.0")
	rctx.Context.Request.Header.Set("X-Api-Key", "secret-client-key")

	b := &Backend{}
	b.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})

	if capturedHeader == nil {
		t.Fatal("upstream server was never called")
	}
	// 自定义头必须透传。
	if got := capturedHeader.Get("X-Foo"); got != "bar" {
		t.Errorf("X-Foo header = %q, want bar (custom header must be forwarded)", got)
	}
	// User-Agent 应为客户端值，不是 Go runtime 默认 "Go-http-client/..."。
	if got := capturedHeader.Get("User-Agent"); got != "my-client/1.0" {
		t.Errorf("User-Agent = %q, want my-client/1.0 (client UA must be forwarded, not Go default)", got)
	}
	// 凭证头不得透传到上游。
	if got := capturedHeader.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty (credential must be stripped)", got)
	}
}

// NOTE: onUpstreamRequest 脚本 reject → relay 中止(不发上游请求/已写回响应)的路径
// 未在此处加 TestChar,因为它需要构造完整的 Agent/cache/ScriptEngine 脚手架
// (runRelayCapture 用 &Backend{} 即 Agent=nil,engineOf 返回 nil,脚本永不运行)。
// 该 reject/abort 路径已由以下测试覆盖:
//   - scripthook 包的 TestRunUpstreamScriptsReject(脚本 reject → Rejected=true + 写回 403)
//   - dataflow 包的 TestFlow_Run_StopsOnAbort(abort step 终止 flow)
//   - dataflow 包的 TestStepUpstreamScript_NoEngineNoop(无引擎时 noop)
