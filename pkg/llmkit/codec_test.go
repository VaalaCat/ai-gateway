package llmkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestCodecInterfaceUsesWireInputsAndEventChannels(t *testing.T) {
	var _ Codec = (*codecImpl)(nil)

	input := DecodeRequestInput{
		Method: "POST",
		Path:   "/v1/chat/completions",
		Body:   []byte(`{"model":"m","messages":[]}`),
	}
	if input.Path == "" {
		t.Fatal("path must be retained")
	}
}

func TestNewCodecHasBuiltInEndpoints(t *testing.T) {
	codec := NewCodec()
	cases := []DecodeRequestInput{
		{Method: "POST", Path: "/v1/chat/completions", Body: []byte(`{"model":"m","messages":[]}`)},
		{Method: "POST", Path: "/v1/responses", Body: []byte(`{"model":"m","input":[]}`)},
		{Method: "POST", Path: "/v1/messages", Body: []byte(`{"model":"m","messages":[]}`)},
	}
	for _, input := range cases {
		if _, err := codec.DecodeRequest(input); err != nil {
			t.Errorf("DecodeRequest(%s): %v", input.Path, err)
		}
	}
}

func TestCodecClaudeParallelToolResults(t *testing.T) {
	// 1. 从 Claude 和 OpenAI 的并行工具调用历史解码。
	cases := []struct {
		name string
		path string
		body string
	}{
		{
			name: "Claude round trip",
			path: "/v1/messages",
			body: `{"model":"source","max_tokens":1024,"messages":[
				{"role":"assistant","content":[
					{"type":"tool_use","id":"call_1","name":"first","input":{}},
					{"type":"tool_use","id":"call_2","name":"second","input":{}}
				]},
				{"role":"user","content":[
					{"type":"tool_result","tool_use_id":"call_1","content":"first result"},
					{"type":"tool_result","tool_use_id":"call_2","content":"second result"},
					{"type":"text","text":"continue"}
				]}
			]}`,
		},
		{
			name: "OpenAI to Claude",
			path: "/v1/chat/completions",
			body: `{"model":"source","messages":[
				{"role":"assistant","tool_calls":[
					{"id":"call_1","type":"function","function":{"name":"first","arguments":"{}"}},
					{"id":"call_2","type":"function","function":{"name":"second","arguments":"{}"}}
				]},
				{"role":"tool","tool_call_id":"call_1","content":"first result"},
				{"role":"tool","tool_call_id":"call_2","content":"second result"},
				{"role":"user","content":"continue"}
			]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codec := NewCodec()
			decoded, err := codec.DecodeRequest(DecodeRequestInput{
				Method: "POST",
				Path:   tc.path,
				Body:   []byte(tc.body),
			})
			require.NoError(t, err)
			require.Len(t, decoded.Request.Messages, 4)
			require.Equal(t, RoleTool, decoded.Request.Messages[1].Role)
			require.Equal(t, RoleTool, decoded.Request.Messages[2].Role)

			// 2. 编码成 Claude，两个结果紧接工具调用，用户文本排在结果之后。
			encoded, err := codec.EncodeRequest(EncodeRequestInput{
				Request: decoded.Request,
				Target: Target{
					Protocol: ProtocolClaude,
					Model:    "claude-test",
				},
			})
			require.NoError(t, err)
			var body struct {
				Messages json.RawMessage `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &body))
			require.JSONEq(t, `[
				{"role":"assistant","content":[
					{"type":"tool_use","id":"call_1","name":"first","input":{}},
					{"type":"tool_use","id":"call_2","name":"second","input":{}}
				]},
				{"role":"user","content":[
					{"type":"tool_result","tool_use_id":"call_1","content":"first result"},
					{"type":"tool_result","tool_use_id":"call_2","content":"second result"}
				]},
				{"role":"user","content":"continue"}
			]`, string(body.Messages))
		})
	}
}

func TestNewCodecHeaderOverrideIsCaseInsensitive(t *testing.T) {
	overrideValues := []string{"caller-token"}
	multiValues := []string{"first", "second"}
	overrides := map[string][]string{
		"authorization": overrideValues,
		"x-multi":       multiValues,
	}

	encoded, err := NewCodec().EncodeRequest(EncodeRequestInput{
		Request: Request{
			Model: "gpt-5",
			Messages: []Message{{
				Role:    RoleUser,
				Content: []ContentBlock{{Type: ContentTypeText, Text: "hello"}},
			}},
		},
		Target: Target{
			Protocol: ProtocolOpenAIChat,
			APIKey:   "generated-api-key",
			Model:    "gpt-5",
			Headers:  overrides,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := encoded.Headers["Authorization"]; !reflect.DeepEqual(got, []string{"caller-token"}) {
		t.Fatalf("Authorization = %#v, want only caller-token; headers=%#v", got, encoded.Headers)
	}
	if got := encoded.Headers["X-Multi"]; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("X-Multi = %#v, want both values; headers=%#v", got, encoded.Headers)
	}
	if got := overrides["authorization"]; !reflect.DeepEqual(got, []string{"caller-token"}) {
		t.Fatalf("override values mutated: %#v", overrides)
	}
	if got := overrides["x-multi"]; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("multi-value override mutated: %#v", overrides)
	}
}

func TestCodecInstancesDoNotShareRegistryState(t *testing.T) {
	first := NewCodec()
	second := NewCodec()
	if first == second {
		t.Fatal("NewCodec must return independent instances")
	}
}

func TestCodecMapsAllFourWireDirections(t *testing.T) {
	state := &struct{ name string }{name: "request-state"}
	handler := &fakeHandler{
		endpoints: []protocol.Endpoint{{Method: "POST", Path: "/fake"}},
		decodeRequest: func(input protocol.DecodeRequestInput) (*ir.Request, error) {
			if input.Method != "POST" || input.Path != "/fake" || input.Headers["X-Test"][0] != "yes" || string(input.Body) != "request" {
				t.Fatalf("decode input = %#v", input)
			}
			return &ir.Request{Model: "decoded"}, nil
		},
		encodeRequest: func(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
			if input.Request.Model != "input" || input.Target.Model != "target" || input.Options.BuiltinToolFallback != convert.BuiltinToolFallbackDrop {
				t.Fatalf("encode input = %#v", input)
			}
			convert.RecordDroppedTools(input.Request, []convert.DroppedTool{{Type: "web_search", Reason: convert.DroppedToolReasonCrossProtocolIncompatible}})
			return protocol.EncodedRequest{Method: "POST", Path: "/upstream", Headers: map[string][]string{"X-Upstream": {"yes"}}, Body: []byte("encoded")}, state, nil
		},
		decodeResponse: func(_ context.Context, input protocol.DecodeResponseInput, gotState any) (<-chan ir.Event, error) {
			if input.StatusCode != 200 || input.Stream || gotState != state {
				t.Fatalf("decode response input=%#v state=%#v", input, gotState)
			}
			body, err := io.ReadAll(input.Body)
			if err != nil || string(body) != "response" {
				t.Fatalf("body=%q error=%v", body, err)
			}
			return irEventChannel(ir.Event{Type: ir.EventDone}), nil
		},
		encodeResponse: func(_ context.Context, input protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
			if input.Protocol != ir.ProtocolOpenAIChat || input.Stream {
				t.Fatalf("encode response input = %#v", input)
			}
			if event := <-input.Events; event.Type != ir.EventDone {
				t.Fatalf("event = %#v", event)
			}
			chunks := make(chan protocol.EncodedChunk, 1)
			chunks <- protocol.EncodedChunk{Data: []byte("chunk")}
			close(chunks)
			return chunks, nil
		},
	}
	codec := newCodecWithHandlers(map[Protocol]protocol.Handler{ProtocolOpenAIChat: handler})

	decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/fake", Headers: map[string][]string{"X-Test": {"yes"}}, Body: []byte("request")})
	if err != nil || decoded.Protocol != ProtocolOpenAIChat || decoded.Request.Model != "decoded" {
		t.Fatalf("decoded=%#v error=%v", decoded, err)
	}

	var callback []DroppedTool
	requestMetadata := map[string]any{}
	encoded, err := codec.EncodeRequest(EncodeRequestInput{
		Request: Request{Model: "input", Metadata: requestMetadata},
		Target:  Target{Protocol: ProtocolOpenAIChat, Model: "target"},
		Options: ConversionOptions{BuiltinToolFallback: BuiltinToolFallbackDrop, OnDroppedTools: func(dropped []DroppedTool) { callback = append([]DroppedTool(nil), dropped...) }},
	})
	if err != nil || encoded.Path != "/upstream" || !reflect.DeepEqual(encoded.Headers, map[string][]string{"X-Upstream": {"yes"}}) || string(encoded.Body) != "encoded" || encoded.State.value != state {
		t.Fatalf("encoded=%#v error=%v", encoded, err)
	}
	wantDropped := []DroppedTool{{Type: "web_search", Reason: convert.DroppedToolReasonCrossProtocolIncompatible}}
	if !reflect.DeepEqual(callback, wantDropped) {
		t.Fatalf("callback=%#v want=%#v", callback, wantDropped)
	}
	require.Empty(t, requestMetadata, "EncodeRequest must not mutate caller metadata")

	events, err := codec.DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolOpenAIChat, StatusCode: 200, Body: strings.NewReader("response"), State: encoded.State})
	if err != nil || (<-events).Type != EventDone {
		t.Fatalf("decode response error=%v", err)
	}

	chunks, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: ProtocolOpenAIChat, Events: irEventChannel(ir.Event{Type: ir.EventDone})})
	if err != nil {
		t.Fatal(err)
	}
	if chunk := <-chunks; string(chunk.Data) != "chunk" || chunk.Err != nil {
		t.Fatalf("chunk=%#v", chunk)
	}
}

func TestCodecBoundaries(t *testing.T) {
	handler := &fakeHandler{endpoints: []protocol.Endpoint{{Method: "POST", Path: "/fake"}}}
	codec := newCodecWithHandlers(map[Protocol]protocol.Handler{ProtocolOpenAIChat: handler})

	t.Run("unknown endpoint", func(t *testing.T) {
		_, err := codec.DecodeRequest(DecodeRequestInput{Method: "GET", Path: "/missing"})
		if !errors.Is(err, ErrUnsupportedEndpoint) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("unknown protocol", func(t *testing.T) {
		_, err := codec.EncodeRequest(EncodeRequestInput{Target: Target{Protocol: ProtocolGemini}})
		if !errors.Is(err, ErrUnsupportedProtocol) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("nil decoded request", func(t *testing.T) {
		handler.decodeRequest = func(protocol.DecodeRequestInput) (*ir.Request, error) { return nil, nil }
		_, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/fake"})
		if !errors.Is(err, ErrNilDecodedRequest) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("nil dropped callback", func(t *testing.T) {
		handler.encodeRequest = func(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
			convert.RecordDroppedTools(input.Request, []convert.DroppedTool{{Type: "web_search"}})
			return protocol.EncodedRequest{}, nil, nil
		}
		if _, err := codec.EncodeRequest(EncodeRequestInput{Target: Target{Protocol: ProtocolOpenAIChat}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty chunk channel", func(t *testing.T) {
		handler.encodeResponse = func(context.Context, protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
			chunks := make(chan protocol.EncodedChunk)
			close(chunks)
			return chunks, nil
		}
		chunks, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: ProtocolOpenAIChat})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := <-chunks; ok {
			t.Fatal("empty chunk channel emitted a chunk")
		}
	})
}

type fakeHandler struct {
	endpoints      []protocol.Endpoint
	decodeRequest  func(protocol.DecodeRequestInput) (*ir.Request, error)
	encodeRequest  func(protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error)
	decodeResponse func(context.Context, protocol.DecodeResponseInput, any) (<-chan ir.Event, error)
	encodeResponse func(context.Context, protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error)
}

func (handler *fakeHandler) DecodeRequest(input protocol.DecodeRequestInput) (*ir.Request, error) {
	if handler.decodeRequest == nil {
		return &ir.Request{}, nil
	}
	return handler.decodeRequest(input)
}

func (handler *fakeHandler) EncodeRequest(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
	if handler.encodeRequest == nil {
		return protocol.EncodedRequest{}, nil, nil
	}
	return handler.encodeRequest(input)
}

func (handler *fakeHandler) DecodeResponse(ctx context.Context, input protocol.DecodeResponseInput, state any) (<-chan ir.Event, error) {
	if handler.decodeResponse == nil {
		return irEventChannel(), nil
	}
	return handler.decodeResponse(ctx, input, state)
}

func (handler *fakeHandler) EncodeResponse(ctx context.Context, input protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
	if handler.encodeResponse == nil {
		chunks := make(chan protocol.EncodedChunk)
		close(chunks)
		return chunks, nil
	}
	return handler.encodeResponse(ctx, input)
}

func (handler *fakeHandler) Endpoints() []protocol.Endpoint { return handler.endpoints }

func irEventChannel(events ...ir.Event) <-chan ir.Event {
	channel := make(chan ir.Event, len(events))
	for _, event := range events {
		channel <- event
	}
	close(channel)
	return channel
}

// TestCodecReasoningTextPresence 检查未指定、显式清空和替换推理明文后的请求编码。
func TestCodecReasoningTextPresence(t *testing.T) {
	// 1. 同一份原始推理分别使用 nil、空切片和新明文。
	for _, tt := range []struct {
		name        string
		fragments   []string
		wantSummary string
		wantContent string
	}{
		{
			name:        "unspecified_preserves_raw_text",
			wantSummary: "old summary",
			wantContent: "old content",
		},
		{
			name:      "empty_clears_raw_text",
			fragments: []string{},
		},
		{
			name:        "specified_replaces_raw_text",
			fragments:   []string{"new text"},
			wantSummary: "new text",
			wantContent: "new text",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reasoning := &ReasoningContent{
				Summary:   tt.fragments,
				Content:   tt.fragments,
				Encrypted: "opaque",
				RawJSON:   json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"old summary"}],"content":[{"type":"reasoning_text","text":"old content"}],"encrypted_content":"opaque","provider_extra":"native"}`),
			}
			// 2. 经公开 Codec 编码，比较实际发送的明文与保留的原生数据。
			encoded, err := NewCodec().EncodeRequest(EncodeRequestInput{
				Request: Request{
					Messages: []Message{
						{
							Role: RoleAssistant,
							Content: []ContentBlock{
								{
									Type:      ContentTypeThinking,
									Reasoning: reasoning,
								},
							},
						},
					},
				},
				Target: Target{
					Protocol: ProtocolOpenAIResponses,
					Model:    "m",
				},
			})
			require.NoError(t, err)
			var body struct {
				Input []struct {
					Type             string                  `json:"type"`
					Summary          []struct{ Text string } `json:"summary"`
					Content          []struct{ Text string } `json:"content"`
					EncryptedContent string                  `json:"encrypted_content"`
					ProviderExtra    string                  `json:"provider_extra"`
				} `json:"input"`
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &body))
			require.Len(t, body.Input, 1)
			item := body.Input[0]
			require.Equal(t, "reasoning", item.Type)
			var summary, content string
			for _, part := range item.Summary {
				summary += part.Text
			}
			for _, part := range item.Content {
				content += part.Text
			}
			require.Equal(t, tt.wantSummary, summary)
			require.Equal(t, tt.wantContent, content)
			require.Equal(t, "opaque", item.EncryptedContent)
			require.Equal(t, "native", item.ProviderExtra)
			require.Equal(t, tt.fragments, reasoning.Summary)
			require.Equal(t, tt.fragments, reasoning.Content)
		})
	}
}

func TestCodecOrdinaryToolHistoryPreservesNativeReasoning(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, reasoning string
		protocol                    Protocol
	}{
		{
			name: "responses", path: "/v1/responses", protocol: ProtocolOpenAIResponses,
			body:      `{"model":"m","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-provider-reasoning","summary":[],"content":[],"provider_extra":{"signature":"nested-native-state"}},{"type":"function_call","call_id":"call_1","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			reasoning: `{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-provider-reasoning","summary":[],"content":[],"provider_extra":{"signature":"nested-native-state"}}`,
		},
		{
			name: "claude", path: "/v1/messages", protocol: ProtocolClaudeMessages,
			body:      `{"model":"m","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"opaque-provider-signature","cache_control":{"type":"ephemeral"}},{"type":"tool_use","id":"call_1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}]}`,
			reasoning: `{"type":"thinking","thinking":"plan","signature":"opaque-provider-signature","cache_control":{"type":"ephemeral"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codec := NewCodec()
			decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: tc.path, Body: []byte(tc.body)})
			require.NoError(t, err)
			before := cloneRequest(decoded.Request)
			encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: tc.protocol, Model: "m"}})
			require.NoError(t, err)
			require.Equal(t, before, decoded.Request)
			var wire struct {
				Input    []json.RawMessage `json:"input"`
				Messages []struct {
					Content []json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &wire))
			blocks := wire.Input
			if tc.protocol == ProtocolClaudeMessages {
				require.NotEmpty(t, wire.Messages)
				blocks = wire.Messages[0].Content
			}
			require.NotEmpty(t, blocks)
			require.JSONEq(t, tc.reasoning, string(blocks[0]))
			replayed, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: tc.path, Body: encoded.Body})
			require.NoError(t, err)
			var callIDs, resultIDs []string
			for _, message := range replayed.Request.Messages {
				for _, call := range message.ToolCalls {
					callIDs = append(callIDs, call.ID)
					require.Equal(t, "read", call.Name)
					require.JSONEq(t, "{}", call.Arguments)
				}
				if message.Role == RoleTool {
					resultIDs = append(resultIDs, message.ToolCallID)
					require.Equal(t, "ok", message.Content[0].Text)
				}
			}
			require.Equal(t, []string{"call_1"}, callIDs)
			require.Equal(t, callIDs, resultIDs)
			require.NotContains(t, string(encoded.Body), "context_engineering_is_the_way_to_go")
		})
	}
}

// 工具轮次的消息签名只来自工具调用的 ProviderData，思考内容里的签名不会在换模型后写回。
func TestCodecToolRoundMessageSignatureFromProviderData(t *testing.T) {
	// 1. 第一轮：model-a 返回带消息签名的工具调用，以 Responses 格式交给使用者。
	codec := NewCodec()
	ctx := context.Background()
	seed, err := codec.EncodeRequest(EncodeRequestInput{Target: Target{Protocol: ProtocolOpenAIChat, Model: "model-a"}})
	require.NoError(t, err)
	events, err := codec.DecodeResponse(ctx, DecodeResponseInput{Protocol: ProtocolOpenAIChat, StatusCode: 200, State: seed.State, Body: strings.NewReader(`{"choices":[{"message":{"role":"assistant","reasoning_content":"thinking","signature":"a-signature","tool_calls":[{"id":"id1","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)})
	require.NoError(t, err)
	chunks, err := codec.EncodeResponse(ctx, EncodeResponseInput{Protocol: ProtocolOpenAIResponses, Events: events})
	require.NoError(t, err)
	var responseBytes bytes.Buffer
	for chunk := range chunks {
		responseBytes.Write(chunk.Data)
	}
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(responseBytes.Bytes(), &response))
	body, err := json.Marshal(map[string]any{"model": "model-b", "input": response["output"]})
	require.NoError(t, err)
	decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: body})
	require.NoError(t, err)

	// 2. 第二轮分别发给 model-b 和 model-a：只有模型对得上时才写回消息签名。
	before := cloneRequest(decoded.Request)
	for _, tt := range []struct {
		model         string
		wantSignature bool
	}{
		{model: "model-b", wantSignature: false},
		{model: "model-a", wantSignature: true},
	} {
		t.Run(tt.model, func(t *testing.T) {
			encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolOpenAIChat, Model: tt.model}})
			require.NoError(t, err)
			require.Equal(t, tt.wantSignature, strings.Contains(string(encoded.Body), "a-signature"))
			require.Contains(t, string(encoded.Body), "thinking")
			require.Equal(t, before, decoded.Request)
		})
	}
}

// 写给上游时按 ProviderData 的模型记录决定写原生字段、写包还是只写原始 ID。
func TestCodecResponsesTruncatesUnpackedCallIDs(t *testing.T) {
	// 1. 使用实际超长 ID，分别覆盖模型切换、最后一层解包和下一层网关转发。
	const rawID = "call-ff0d9a76-533e-4280-9d31-394ef8a9044d-24__fc_91105360-b333-9abb-9d7b-4dbf05945257_0"
	for _, tt := range []struct {
		name   string
		models []string
		packed bool
	}{
		{name: "model switched", models: []string{"grok"}},
		{name: "last gateway", models: []string{"gpt"}},
		{name: "next gateway", models: []string{"inner", "gpt"}, packed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id := convert.PackToolCallID(rawID, &ToolCallProviderData{
				Protocol: ProtocolOpenAIChat,
				Models:   tt.models,
				Fields:   map[string]json.RawMessage{"signature": json.RawMessage(`"original-signature"`)},
			})
			req := Request{Messages: []Message{
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: id, Name: "read", Arguments: "{}"}}},
				{Role: RoleTool, ToolCallID: id, Content: []ContentBlock{{Type: ContentTypeText, Text: "done"}}},
			}}
			before, err := json.Marshal(req)
			require.NoError(t, err)
			// 2. 通过公开 Codec 编码，解包后的 ID 截断，下一层所需签名包保持完整。
			encoded, err := NewCodec().EncodeRequest(EncodeRequestInput{
				Request: req,
				Target:  Target{Protocol: ProtocolOpenAIResponses, Model: "gpt", BaseURL: "https://example.com"},
			})
			require.NoError(t, err)
			var body struct {
				Input []struct {
					CallID string `json:"call_id"`
				} `json:"input"`
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &body))
			require.Len(t, body.Input, 2)
			require.Equal(t, body.Input[0].CallID, body.Input[1].CallID)
			if tt.packed {
				unpackedID, data := convert.UnpackToolCallID(body.Input[0].CallID)
				require.Equal(t, rawID, unpackedID)
				require.NotNil(t, data)
				require.Equal(t, []string{"inner"}, data.Models)
				require.JSONEq(t, `"original-signature"`, string(data.Fields["signature"]))
			} else {
				require.Equal(t, rawID[:64], body.Input[0].CallID)
			}
			after, err := json.Marshal(req)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after))
		})
	}
}

func TestCodecToolProviderDataUpstream(t *testing.T) {
	const googleExtra = `{"google":{"thought_signature":"google-sig"}}`
	const placeholderExtra = `{"google":{"thought_signature":"context_engineering_is_the_way_to_go"}}`
	providerData := func(models []string, fields map[string]string) *ToolCallProviderData {
		raw := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			raw[key] = json.RawMessage(value)
		}
		return &ToolCallProviderData{Protocol: ProtocolOpenAIChat, Models: models, Fields: raw}
	}
	call := func(id string, data *ToolCallProviderData) ToolCall {
		return ToolCall{ID: id, Name: "read", Arguments: "{}", ProviderData: data}
	}
	result := func(id string) Message {
		return Message{Role: RoleTool, ToolCallID: id, Content: []ContentBlock{{Type: ContentTypeText, Text: "ok"}}}
	}
	toolRound := func(calls ...ToolCall) []Message {
		messages := []Message{{Role: RoleAssistant, ToolCalls: calls}}
		for _, call := range calls {
			messages = append(messages, result(call.ID))
		}
		return messages
	}
	fixed := func(messages ...Message) func(*testing.T) Request {
		return func(*testing.T) Request { return Request{Model: "client-model", Messages: messages} }
	}
	packedFromBoth := convert.PackToolCallID("call_1", providerData([]string{"m"}, map[string]string{"extra_content": `{"google":{"thought_signature":"from-package"}}`}))

	tests := []struct {
		name     string
		request  func(*testing.T) Request
		target   Target
		fallback ToolSignatureFallback
		assert   func(t *testing.T, body string, wire chatWire)
	}{
		{
			name:    "client supplied to chat",
			request: fixed(toolRound(call("call_1", providerData(nil, map[string]string{"extra_content": googleExtra})))...),
			target:  Target{Protocol: ProtocolOpenAIChat, Model: "gemini-3-pro"},
			assert: func(t *testing.T, _ string, wire chatWire) {
				require.Equal(t, "call_1", wire.Messages[0].ToolCalls[0].ID)
				require.JSONEq(t, googleExtra, string(wire.Messages[0].ToolCalls[0].ExtraContent))
				require.Equal(t, "call_1", wire.Messages[1].ToolCallID)
			},
		},
		{
			name:    "client supplied to claude",
			request: fixed(toolRound(call("call_1", providerData(nil, map[string]string{"extra_content": googleExtra})))...),
			target:  Target{Protocol: ProtocolClaudeMessages, Model: "gemini-3-pro"},
			assert: func(t *testing.T, body string, _ chatWire) {
				require.Contains(t, body, `"id":"call_1"`)
				require.Contains(t, body, `"tool_use_id":"call_1"`)
				require.NotContains(t, body, "google-sig")
			},
		},
		{
			name:    "model switched",
			request: fixed(toolRound(call("call_1", providerData([]string{"gemini-3-pro"}, map[string]string{"extra_content": googleExtra})))...),
			target:  Target{Protocol: ProtocolOpenAIChat, Model: "gpt-5"},
			assert: func(t *testing.T, body string, wire chatWire) {
				require.Equal(t, "call_1", wire.Messages[0].ToolCalls[0].ID)
				require.Equal(t, "call_1", wire.Messages[1].ToolCallID)
				require.NotContains(t, body, "extra_content")
			},
		},
		{
			name:    "next hop is another layer",
			request: fixed(toolRound(call("call_1", providerData([]string{"m3", "m2"}, map[string]string{"extra_content": googleExtra})))...),
			target:  Target{Protocol: ProtocolOpenAIChat, Model: "m2"},
			assert: func(t *testing.T, body string, wire chatWire) {
				id := wire.Messages[0].ToolCalls[0].ID
				rawID, data := convert.UnpackToolCallID(id)
				require.Equal(t, "call_1", rawID)
				require.NotNil(t, data)
				require.Equal(t, []string{"m3"}, data.Models)
				require.JSONEq(t, googleExtra, string(data.Fields["extra_content"]))
				require.Equal(t, id, wire.Messages[1].ToolCallID)
				require.NotContains(t, body, `"extra_content"`)
			},
		},
		{
			name:     "next hop is another layer so the fallback adds no placeholder",
			request:  fixed(toolRound(call("call_1", providerData([]string{"m3", "m2"}, map[string]string{"extra_content": googleExtra})))...),
			target:   Target{Protocol: ProtocolOpenAIChat, Model: "m2"},
			fallback: ToolSignatureFallbackGemini,
			assert: func(t *testing.T, body string, wire chatWire) {
				rawID, data := convert.UnpackToolCallID(wire.Messages[0].ToolCalls[0].ID)
				require.Equal(t, "call_1", rawID)
				require.NotNil(t, data)
				require.Equal(t, []string{"m3"}, data.Models)
				require.Empty(t, wire.Messages[0].ToolCalls[0].ExtraContent)
				require.NotContains(t, body, "context_engineering_is_the_way_to_go")
			},
		},
		{
			name: "last layer writes native fields",
			request: fixed(toolRound(call("call_1", providerData([]string{"m3"}, map[string]string{
				"extra_content": googleExtra, "signature": `"tool-sig"`, "message_signature": `"message-sig"`,
			})))...),
			target: Target{Protocol: ProtocolOpenAIChat, Model: "m3"},
			assert: func(t *testing.T, _ string, wire chatWire) {
				toolCall := wire.Messages[0].ToolCalls[0]
				require.Equal(t, "call_1", toolCall.ID)
				require.JSONEq(t, googleExtra, string(toolCall.ExtraContent))
				require.Equal(t, "tool-sig", toolCall.Signature)
				require.Equal(t, "message-sig", wire.Messages[0].Signature)
				require.Equal(t, "call_1", wire.Messages[1].ToolCallID)
			},
		},
		{
			name:     "fallback signs only the first call",
			request:  fixed(toolRound(call("call_1", nil), call("call_2", nil))...),
			target:   Target{Protocol: ProtocolOpenAIChat, Model: "gemini-3-pro"},
			fallback: ToolSignatureFallbackGemini,
			assert: func(t *testing.T, _ string, wire chatWire) {
				calls := wire.Messages[0].ToolCalls
				require.JSONEq(t, `{"google":{"thought_signature":"context_engineering_is_the_way_to_go"}}`, string(calls[0].ExtraContent))
				require.Empty(t, calls[1].ExtraContent)
				require.Equal(t, []string{"call_1", "call_2"}, []string{calls[0].ID, calls[1].ID})
			},
		},
		{
			name: "parallel calls with only the first signed",
			request: fixed(toolRound(
				call("call_1", providerData([]string{"m"}, map[string]string{"extra_content": googleExtra})),
				call("call_2", nil),
			)...),
			target:   Target{Protocol: ProtocolOpenAIChat, Model: "m"},
			fallback: ToolSignatureFallbackGemini,
			assert: func(t *testing.T, body string, wire chatWire) {
				calls := wire.Messages[0].ToolCalls
				require.JSONEq(t, googleExtra, string(calls[0].ExtraContent))
				require.Equal(t, "call_2", calls[1].ID)
				require.Empty(t, calls[1].ExtraContent)
				require.NotContains(t, body, "context_engineering_is_the_way_to_go")
			},
		},
		{
			name:     "model mismatch drops the signature and triggers the fallback",
			request:  fixed(toolRound(call("call_1", providerData([]string{"gemini-3-pro"}, map[string]string{"extra_content": googleExtra})))...),
			target:   Target{Protocol: ProtocolOpenAIChat, Model: "gemini-2.5"},
			fallback: ToolSignatureFallbackGemini,
			assert: func(t *testing.T, body string, wire chatWire) {
				calls := wire.Messages[0].ToolCalls
				require.Equal(t, "call_1", calls[0].ID)
				require.JSONEq(t, placeholderExtra, string(calls[0].ExtraContent))
				require.Equal(t, "call_1", wire.Messages[1].ToolCallID)
				require.NotContains(t, body, "google-sig")
			},
		},
		{
			name: "fallback is decided per round",
			request: fixed(append(
				toolRound(call("call_1", providerData([]string{"m"}, map[string]string{"extra_content": googleExtra}))),
				toolRound(call("call_2", nil))...,
			)...),
			target:   Target{Protocol: ProtocolOpenAIChat, Model: "m"},
			fallback: ToolSignatureFallbackGemini,
			assert: func(t *testing.T, body string, wire chatWire) {
				require.Len(t, wire.Messages, 4)
				require.JSONEq(t, googleExtra, string(wire.Messages[0].ToolCalls[0].ExtraContent))
				require.JSONEq(t, placeholderExtra, string(wire.Messages[2].ToolCalls[0].ExtraContent))
				require.Equal(t, 1, strings.Count(body, "context_engineering_is_the_way_to_go"))
			},
		},
		{
			name: "raw id reused across rounds",
			request: fixed(append(
				toolRound(call("call_reused", providerData([]string{"m3", "m2"}, map[string]string{"signature": `"round-1"`}))),
				toolRound(call("call_reused", providerData([]string{"m3", "m2"}, map[string]string{"signature": `"round-2"`})))...,
			)...),
			target: Target{Protocol: ProtocolOpenAIChat, Model: "m2"},
			assert: func(t *testing.T, _ string, wire chatWire) {
				require.Len(t, wire.Messages, 4)
				first, second := wire.Messages[0].ToolCalls[0].ID, wire.Messages[2].ToolCalls[0].ID
				require.NotEqual(t, first, second)
				require.Equal(t, first, wire.Messages[1].ToolCallID)
				require.Equal(t, second, wire.Messages[3].ToolCallID)
				for id, signature := range map[string]string{first: `"round-1"`, second: `"round-2"`} {
					rawID, data := convert.UnpackToolCallID(id)
					require.Equal(t, "call_reused", rawID)
					require.JSONEq(t, signature, string(data.Fields["signature"]))
				}
			},
		},
		{
			name: "package and native fields sent back together",
			request: func(t *testing.T) Request {
				body := fmt.Sprintf(`{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":%q,"type":"function","function":{"name":"read","arguments":"{}"},"extra_content":{"google":{"thought_signature":"from-native"}}}]},{"role":"tool","tool_call_id":%q,"content":"ok"}]}`, packedFromBoth, packedFromBoth)
				decoded, err := NewCodec().DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/chat/completions", Body: []byte(body)})
				require.NoError(t, err)
				return decoded.Request
			},
			target: Target{Protocol: ProtocolOpenAIChat, Model: "m"},
			assert: func(t *testing.T, body string, wire chatWire) {
				require.Equal(t, 1, strings.Count(body, "thought_signature"))
				require.Contains(t, body, "from-package")
				require.NotContains(t, body, "from-native")
				require.Equal(t, "call_1", wire.Messages[0].ToolCalls[0].ID)
				require.Equal(t, "call_1", wire.Messages[1].ToolCallID)
			},
		},
		{
			name: "empty raw id only maps tool results",
			request: fixed(
				Message{Role: RoleAssistant, ToolCalls: []ToolCall{call("", providerData([]string{"m3", "m2"}, map[string]string{"signature": `"s"`}))}},
				result(""),
				Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "next"}}},
			),
			target: Target{Protocol: ProtocolOpenAIChat, Model: "m2"},
			assert: func(t *testing.T, _ string, wire chatWire) {
				require.Len(t, wire.Messages, 3)
				packed := wire.Messages[0].ToolCalls[0].ID
				require.True(t, strings.HasPrefix(packed, "call_llmkit_v1_"), packed)
				require.Equal(t, packed, wire.Messages[1].ToolCallID)
				require.Empty(t, wire.Messages[2].ToolCallID)
			},
		},
		{
			name: "responses structured tool output follows its call",
			request: func(t *testing.T) Request {
				packed := convert.PackToolCallID("call_1", providerData([]string{"gemini-3-pro"}, map[string]string{"extra_content": googleExtra}))
				body := fmt.Sprintf(`{"model":"gpt-5","input":[{"type":"function_call","call_id":%q,"name":"view","arguments":"{}"},{"type":"function_call_output","call_id":%q,"output":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`, packed, packed)
				decoded, err := NewCodec().DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: []byte(body)})
				require.NoError(t, err)
				return decoded.Request
			},
			target: Target{Protocol: ProtocolOpenAIResponses, Model: "gpt-5"},
			assert: func(t *testing.T, body string, _ chatWire) {
				var wire struct {
					Input []struct {
						Type   string          `json:"type"`
						CallID string          `json:"call_id"`
						Output json.RawMessage `json:"output"`
					} `json:"input"`
				}
				require.NoError(t, json.Unmarshal([]byte(body), &wire))
				require.Len(t, wire.Input, 2)
				require.Equal(t, "function_call", wire.Input[0].Type)
				require.Equal(t, "call_1", wire.Input[0].CallID)
				require.Equal(t, "function_call_output", wire.Input[1].Type)
				require.Equal(t, "call_1", wire.Input[1].CallID)
				require.Contains(t, string(wire.Input[1].Output), "input_image")
				require.NotContains(t, body, "call_llmkit_")
			},
		},
		{
			name:    "result without its call",
			request: fixed(Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "continue"}}}, result(packedFromBoth)),
			target:  Target{Protocol: ProtocolOpenAIChat, Model: "m"},
			assert: func(t *testing.T, _ string, wire chatWire) {
				require.Equal(t, "call_1", wire.Messages[1].ToolCallID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := tt.request(t)
			before := cloneRequest(request)
			encoded, err := NewCodec().EncodeRequest(EncodeRequestInput{
				Request: request, Target: tt.target,
				Options: ConversionOptions{ToolSignatureFallback: tt.fallback},
			})
			require.NoError(t, err)
			require.Equal(t, before, request, "EncodeRequest must not change the caller's request")
			var wire chatWire
			if tt.target.Protocol == ProtocolOpenAIChat {
				require.NoError(t, json.Unmarshal(encoded.Body, &wire))
			}
			tt.assert(t, string(encoded.Body), wire)
		})
	}
}

// chatWire 是测试里读取 Chat 上游请求体用的结构。
type chatWire struct {
	Messages []struct {
		Signature  string `json:"signature"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			ID           string          `json:"id"`
			Signature    string          `json:"signature"`
			ExtraContent json.RawMessage `json:"extra_content"`
		} `json:"tool_calls"`
	} `json:"messages"`
}

// fakeGemini 是链最里面的上游，说 OpenAI Chat：第 1 次请求返回带签名的工具调用，之后返回文本，并记下每次收到的请求体。
type fakeGemini struct {
	stream   bool
	received [][]byte
}

const (
	fakeGeminiToolCall = `{"index":0,"id":"call_g1","type":"function","function":{"name":"read","arguments":"{}"},"extra_content":{"google":{"thought_signature":"real-sig"}}}`
	fakeGeminiTextDone = "done"
)

func (g *fakeGemini) serve(_ *testing.T, request EncodedRequest) []byte {
	g.received = append(g.received, request.Body)
	switch {
	case len(g.received) == 1 && g.stream:
		return []byte("data: {\"model\":\"m3\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[" + fakeGeminiToolCall + "]}}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	case len(g.received) == 1:
		return []byte(`{"model":"m3","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[` + fakeGeminiToolCall + `]},"finish_reason":"tool_calls"}]}`)
	case g.stream:
		return []byte("data: {\"model\":\"m3\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"" + fakeGeminiTextDone + "\"}}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	default:
		return []byte(`{"model":"m3","choices":[{"index":0,"message":{"role":"assistant","content":"` + fakeGeminiTextDone + `"},"finish_reason":"stop"}]}`)
	}
}

// twoLayerHop 是某一层把请求发给下一跳并取回响应体的方式。
type twoLayerHop func(t *testing.T, request EncodedRequest) []byte

// gatewayLayer 是一层网关：收使用者协议的请求，用本层的 Target.Model 转给下一跳，再把响应写回使用者协议。
type gatewayLayer struct {
	codec     Codec
	nextProto Protocol
	model     string
	fallback  ToolSignatureFallback
	forward   twoLayerHop
	toClient  [][]byte // 每次写给使用者的响应体，按轮次排列
}

func (l *gatewayLayer) serve(t *testing.T, request EncodedRequest) []byte {
	t.Helper()
	ctx := context.Background()

	// 1. 读使用者请求，按本层模型和下一跳协议重新编码后转发。
	decoded, err := l.codec.DecodeRequest(DecodeRequestInput{Method: request.Method, Path: request.Path, Body: request.Body})
	require.NoError(t, err)
	encoded, err := l.codec.EncodeRequest(EncodeRequestInput{
		Request: decoded.Request,
		Target:  Target{Protocol: l.nextProto, Model: l.model},
		Options: ConversionOptions{ToolSignatureFallback: l.fallback},
	})
	require.NoError(t, err)

	// 2. 读下一跳响应，再写回使用者协议。
	events, err := l.codec.DecodeResponse(ctx, DecodeResponseInput{
		Protocol: l.nextProto, StatusCode: 200, Body: bytes.NewReader(l.forward(t, encoded)),
		Stream: decoded.Request.Stream, State: encoded.State,
	})
	require.NoError(t, err)
	chunks, err := l.codec.EncodeResponse(ctx, EncodeResponseInput{Protocol: decoded.Protocol, Events: events, Stream: decoded.Request.Stream})
	require.NoError(t, err)
	var body bytes.Buffer
	for chunk := range chunks {
		require.NoError(t, chunk.Err)
		body.Write(chunk.Data)
	}
	l.toClient = append(l.toClient, body.Bytes())
	return body.Bytes()
}

// toolChain 是 OV → AIGW1 → AIGW2 → 假 Gemini。OV 按 llmkit Client 的方式直接用 IR 调用，自己保存 IR 历史。
type toolChain struct {
	ovCodec   Codec
	protocols [3]Protocol // OV→AIGW1、AIGW1→AIGW2、AIGW2→假 Gemini
	stream    bool
	ovModel   string
	history   []Message
	outer     *gatewayLayer // AIGW1
	inner     *gatewayLayer // AIGW2
	gemini    *fakeGemini
}

func newToolChain(protocols [3]Protocol, stream bool) *toolChain {
	gemini := &fakeGemini{stream: stream}
	inner := &gatewayLayer{codec: NewCodec(), nextProto: protocols[2], model: "m3", forward: func(t *testing.T, request EncodedRequest) []byte { return gemini.serve(t, request) }}
	outer := &gatewayLayer{codec: NewCodec(), nextProto: protocols[1], model: "m2", forward: inner.serve}
	return &toolChain{ovCodec: NewCodec(), protocols: protocols, stream: stream, ovModel: "m1", outer: outer, inner: inner, gemini: gemini}
}

// round 让 OV 用当前 IR 历史发一轮请求，把解码出的助手消息追加进历史并返回。
func (c *toolChain) round(t *testing.T) Message {
	t.Helper()
	ctx := context.Background()
	encoded, err := c.ovCodec.EncodeRequest(EncodeRequestInput{
		Request: Request{Messages: c.history, Stream: c.stream, MaxTokens: 1024},
		Target:  Target{Protocol: c.protocols[0], Model: c.ovModel},
	})
	require.NoError(t, err)
	events, err := c.ovCodec.DecodeResponse(ctx, DecodeResponseInput{
		Protocol: c.protocols[0], StatusCode: 200, Body: bytes.NewReader(c.outer.serve(t, encoded)),
		Stream: c.stream, State: encoded.State,
	})
	require.NoError(t, err)
	assistant := assistantFromEvents(t, events)
	c.history = append(c.history, assistant)
	return assistant
}

// assistantFromEvents 把响应事件拼成助手消息，工具调用的 ProviderData 以 ToolCallEnd 为准。
func assistantFromEvents(t *testing.T, events <-chan Event) Message {
	t.Helper()
	var text strings.Builder
	var order []string
	calls := map[string]*ToolCall{}
	callFor := func(id string) *ToolCall {
		if call, ok := calls[id]; ok {
			return call
		}
		calls[id] = &ToolCall{ID: id}
		order = append(order, id)
		return calls[id]
	}
	for event := range events {
		switch {
		case event.Type == EventError:
			t.Fatalf("response event error: %+v", event.Error)
		case event.Type == EventContentDelta && event.Delta != nil:
			text.WriteString(event.Delta.Text)
		case event.Type == EventToolCallStart && event.ToolCall != nil:
			call := callFor(event.ToolCall.CallID)
			call.Name = event.ToolCall.Name
			call.ProviderData = latestProviderData(call.ProviderData, event.ToolCall.ProviderData)
		case event.Type == EventToolCallArgumentsDelta && event.ToolCall != nil:
			callFor(event.ToolCall.CallID).Arguments += event.ToolCall.Arguments
		case event.Type == EventToolCallEnd && event.ToolCall != nil:
			call := callFor(event.ToolCall.CallID)
			if event.ToolCall.Arguments != "" {
				call.Arguments = event.ToolCall.Arguments
			}
			call.ProviderData = latestProviderData(call.ProviderData, event.ToolCall.ProviderData)
		case event.Type == EventToolCallDelta && event.Delta != nil && event.Delta.ToolCall != nil:
			delta := event.Delta.ToolCall
			call := callFor(delta.ID)
			if delta.Name != "" {
				call.Name = delta.Name
			}
			call.Arguments += delta.Arguments
			call.ProviderData = latestProviderData(call.ProviderData, delta.ProviderData)
		}
	}
	message := Message{Role: RoleAssistant}
	if text.Len() > 0 {
		message.Content = []ContentBlock{{Type: ContentTypeText, Text: text.String()}}
	}
	for _, id := range order {
		message.ToolCalls = append(message.ToolCalls, *calls[id])
	}
	return message
}

// latestProviderData 返回更新后的 ProviderData：新事件带了就用新的，没带保持原值。
func latestProviderData(current, incoming *ToolCallProviderData) *ToolCallProviderData {
	if incoming != nil {
		return incoming
	}
	return current
}

// geminiToolHistory 读出假 Gemini 收到的请求里第一个工具调用，以及对应工具结果的 ID。
func geminiToolHistory(t *testing.T, body []byte) (callID string, extraContent string, resultID string) {
	t.Helper()
	var wire chatWire
	require.NoError(t, json.Unmarshal(body, &wire))
	for _, message := range wire.Messages {
		if len(message.ToolCalls) > 0 && callID == "" {
			callID, extraContent = message.ToolCalls[0].ID, string(message.ToolCalls[0].ExtraContent)
		}
		if message.ToolCallID != "" && resultID == "" {
			resultID = message.ToolCallID
		}
	}
	return callID, extraContent, resultID
}

// requireClientFacingPacked 断言写给使用者的响应只有包装后的 ID，不带任何原生签名字段。
func requireClientFacingPacked(t *testing.T, wireProtocol Protocol, body []byte) {
	t.Helper()
	text := string(body)
	require.Contains(t, text, "call_llmkit_v1_")
	for _, forbidden := range []string{"extra_content", "thought_signature", "real-sig"} {
		require.NotContains(t, text, forbidden)
	}
	if wireProtocol == ProtocolOpenAIChat {
		require.NotContains(t, text, `"signature"`)
	}
}

// 用 Codec 串两层网关，逐层核对 ProviderData.Models：签名只在模型对得上的时候回到假 Gemini。
func TestCodecToolProviderDataTwoLayers(t *testing.T) {
	const googleSignature = `{"google":{"thought_signature":"real-sig"}}`
	const placeholderSignature = `{"google":{"thought_signature":"context_engineering_is_the_way_to_go"}}`
	chat, claude, responses := ProtocolOpenAIChat, ProtocolClaudeMessages, ProtocolOpenAIResponses

	chains := []struct {
		name      string
		protocols [3]Protocol
	}{
		{name: "chat chat chat", protocols: [3]Protocol{chat, chat, chat}},
		{name: "claude chat chat", protocols: [3]Protocol{claude, chat, chat}},
		{name: "chat claude chat", protocols: [3]Protocol{chat, claude, chat}},
		{name: "responses chat chat", protocols: [3]Protocol{responses, chat, chat}},
		{name: "chat responses chat", protocols: [3]Protocol{chat, responses, chat}},
		{name: "claude claude chat", protocols: [3]Protocol{claude, claude, chat}},
		{name: "responses claude chat", protocols: [3]Protocol{responses, claude, chat}},
		{name: "claude responses chat", protocols: [3]Protocol{claude, responses, chat}},
	}
	// 第 2 轮：改动某一层的模型或兜底，再看假 Gemini 收到什么。
	secondRounds := []struct {
		name          string
		ovModel       string
		innerModel    string
		innerFallback ToolSignatureFallback
		wantExtra     string // 假 Gemini 收到的调用 extra_content；空表示没有
	}{
		{name: "same models", wantExtra: googleSignature},
		{name: "ov switches model", ovModel: "gpt-5"},
		{name: "inner layer switches model with gemini fallback", innerModel: "m3-other", innerFallback: ToolSignatureFallbackGemini, wantExtra: placeholderSignature},
	}

	for _, chain := range chains {
		for _, stream := range []bool{false, true} {
			for _, second := range secondRounds {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", chain.name, stream, second.name), func(t *testing.T) {
					tc := newToolChain(chain.protocols, stream)
					tc.history = []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "go"}}}}

					// 1. 第 1 轮：OV 的 IR 历史里工具调用 ID 是原始 ID，签名在 ProviderData 里，模型名逐层记录。
					first := tc.round(t)
					require.Len(t, first.ToolCalls, 1)
					call := first.ToolCalls[0]
					require.Equal(t, "call_g1", call.ID)
					require.NotNil(t, call.ProviderData)
					require.Equal(t, ProtocolOpenAIChat, call.ProviderData.Protocol)
					require.Equal(t, []string{"m3", "m2", "m1"}, call.ProviderData.Models)
					require.JSONEq(t, googleSignature, string(call.ProviderData.Fields["extra_content"]))

					// 2. 每一层写给使用者的响应都只有包装后的 ID，没有原生签名字段。
					requireClientFacingPacked(t, chain.protocols[0], tc.outer.toClient[0])
					requireClientFacingPacked(t, chain.protocols[1], tc.inner.toClient[0])

					// 3. 第 2 轮：OV 带回工具结果，按表改动模型或兜底。
					tc.history = append(tc.history, Message{Role: RoleTool, ToolCallID: call.ID, Content: []ContentBlock{{Type: ContentTypeText, Text: "ok"}}})
					if second.ovModel != "" {
						tc.ovModel = second.ovModel
					}
					if second.innerModel != "" {
						tc.inner.model = second.innerModel
					}
					tc.inner.fallback = second.innerFallback
					last := tc.round(t)
					require.Equal(t, fakeGeminiTextDone, last.Content[0].Text)

					// 4. 假 Gemini 第 2 次收到的请求：ID 是原始 ID，签名按规则取舍。
					require.Len(t, tc.gemini.received, 2)
					callID, extra, resultID := geminiToolHistory(t, tc.gemini.received[1])
					require.Equal(t, "call_g1", callID)
					require.Equal(t, "call_g1", resultID)
					if second.wantExtra == "" {
						require.Empty(t, extra)
					} else {
						require.JSONEq(t, second.wantExtra, extra)
					}
					require.NotContains(t, string(tc.gemini.received[1]), "call_llmkit_")
					if second.wantExtra != googleSignature {
						require.NotContains(t, string(tc.gemini.received[1]), "real-sig")
					}
				})
			}
		}
	}
}
