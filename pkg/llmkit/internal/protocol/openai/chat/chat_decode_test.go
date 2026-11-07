package chat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"github.com/stretchr/testify/require"
)

// collectStreamEvents runs the chat stream decoder on the given SSE string and
// returns all emitted events. This is a test helper used by streaming tests.
func collectStreamEvents(t *testing.T, sseData string) []ir.Event {
	t.Helper()
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}
	c := &handler{}
	ch, err := c.decodeHTTPResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var events []ir.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

func reasoningEvents(events []ir.Event) []ir.Event {
	result := make([]ir.Event, 0)
	for _, event := range events {
		switch event.Type {
		case ir.EventReasoningSummaryDelta,
			ir.EventReasoningContentDelta,
			ir.EventThinkingDelta,
			ir.EventReasoningDone,
			ir.EventContentDelta,
			ir.EventToolCallStart:
			result = append(result, event)
		}
	}
	return result
}

func eventTypesOf(events []ir.Event) []ir.EventType {
	types := make([]ir.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func indexEvent(events []ir.Event, eventType ir.EventType) int {
	for index, event := range events {
		if event.Type == eventType {
			return index
		}
	}
	return -1
}

func countEvents(events []ir.Event, eventType ir.EventType) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func findEvent(t *testing.T, events []ir.Event, eventType ir.EventType) ir.Event {
	t.Helper()
	index := indexEvent(events, eventType)
	require.NotEqual(t, -1, index)
	return events[index]
}

func collectEventChannel(events <-chan ir.Event) []ir.Event {
	var result []ir.Event
	for event := range events {
		result = append(result, event)
	}
	return result
}

func TestChatDecodeStreamToolReadError(t *testing.T) {
	const partialCall = "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"shell","arguments":"{\"cmd\":"}}]}}]}` + "\n\n"
	const finishedCall = "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"shell","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"
	tests := []struct {
		name       string
		sse        string
		wantArgs   string
		wantEnds   int
		wantFinish string
	}{
		{
			name:       "断流时工具参数只收到一半",
			sse:        partialCall,
			wantArgs:   `{"cmd":`,
			wantEnds:   0,
			wantFinish: "",
		},
		{
			name:       "断流前工具调用已经随 finish_reason 结束",
			sse:        finishedCall,
			wantArgs:   `{}`,
			wantEnds:   1,
			wantFinish: "tool_calls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1. 上游在发送工具调用后断开流。
			response := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(io.MultiReader(strings.NewReader(tt.sse), iotest.ErrReader(io.ErrUnexpectedEOF))),
			}
			events, err := (&handler{}).decodeHTTPResponse(response, true)
			require.NoError(t, err)
			got := collectEventChannel(events)

			// 2. 错误前保留已收到的工具片段，没结束的工具不标记为完成，错误之后也不补 tool_calls 结束原因。
			require.Less(t, indexEvent(got, ir.EventToolCallStart), indexEvent(got, ir.EventError))
			require.Less(t, indexEvent(got, ir.EventToolCallArgumentsDelta), indexEvent(got, ir.EventError))
			require.Equal(t, tt.wantArgs, findEvent(t, got, ir.EventToolCallArgumentsDelta).ToolCall.Arguments)
			require.Equal(t, tt.wantEnds, countEvents(got, ir.EventToolCallEnd))
			require.Equal(t, tt.wantFinish, findEvent(t, got, ir.EventDone).FinishReason)
		})
	}
}

func TestChatDecodeStreamToolTextOrder(t *testing.T) {
	// 1. 上游在工具参数分片之间输出文本。
	sse := "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"shell","arguments":"{"}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{"content":"working"}}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	got := collectStreamEvents(t, sse)

	// 2. 文本与工具调用的事件按到达顺序发出。
	require.Equal(t, []ir.EventType{
		ir.EventStreamStart, ir.EventToolCallStart, ir.EventToolCallArgumentsDelta,
		ir.EventContentDelta, ir.EventToolCallArgumentsDelta, ir.EventToolCallEnd, ir.EventDone,
	}, eventTypesOf(got))
	require.Equal(t, "working", findEvent(t, got, ir.EventContentDelta).Delta.Text)
}

// 流式工具调用收到第一块就发 Start，参数片段和后续文本立刻发，后到的签名并进该调用并随 End 带出。
func TestChatStreamToolCallEmitsOnFirstChunk(t *testing.T) {
	const (
		firstChunk  = `{"index":0,"id":"call_1","function":{"name":"write","arguments":"{\"a\":"}}`
		bigNumber   = `{"google":{"thought_signature":"sig-1"},"vendor":{"n":9007199254740993}}`
		finishChunk = `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`
	)
	toolChunk := func(call string) string {
		return `{"choices":[{"delta":{"tool_calls":[` + call + `]}}]}`
	}
	extraChunk := func(extra string) string {
		return toolChunk(`{"index":0,"function":{"arguments":""},"extra_content":` + extra + `}`)
	}
	extraFields := func(extra string) map[string]string { return map[string]string{"extra_content": extra} }
	providerData := func(fields map[string]string) *ir.ToolCallProviderData {
		raw := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			raw[key] = json.RawMessage(value)
		}
		return &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat, Fields: raw}
	}
	toolEvents := func(events []ir.Event, eventType ir.EventType) []ir.Event {
		var result []ir.Event
		for _, event := range events {
			if event.Type == eventType {
				result = append(result, event)
			}
		}
		return result
	}

	tests := []struct {
		name   string
		chunks []string
		// finish 之前 300ms 内必须收到的事件类型，按到达顺序。
		wantLive []ir.EventType
		check    func(t *testing.T, all []ir.Event)
	}{
		{
			name:     "first chunk emits start and arguments before finish",
			chunks:   []string{toolChunk(firstChunk)},
			wantLive: []ir.EventType{ir.EventStreamStart, ir.EventToolCallStart, ir.EventToolCallArgumentsDelta},
			check: func(t *testing.T, all []ir.Event) {
				start := findEvent(t, all, ir.EventToolCallStart).ToolCall
				require.Equal(t, "call_1", start.CallID)
				require.Equal(t, "write", start.Name)
				require.Nil(t, start.ProviderData)
				require.Equal(t, `{"a":`, findEvent(t, all, ir.EventToolCallArgumentsDelta).ToolCall.Arguments)
				end := findEvent(t, all, ir.EventToolCallEnd).ToolCall
				require.Equal(t, "call_1", end.CallID)
				require.Equal(t, `{"a":`, end.Arguments)
				require.Nil(t, end.ProviderData)
			},
		},
		{
			name: "signature in the first chunk is on start and end",
			chunks: []string{toolChunk(`{"index":0,"id":"call_1","function":{"name":"write","arguments":"{}"},` +
				`"extra_content":{"google":{"thought_signature":"sig-1"}}}`)},
			wantLive: []ir.EventType{ir.EventStreamStart, ir.EventToolCallStart, ir.EventToolCallArgumentsDelta},
			check: func(t *testing.T, all []ir.Event) {
				want := providerData(extraFields(`{"google":{"thought_signature":"sig-1"}}`))
				requireProviderDataEqual(t, want, findEvent(t, all, ir.EventToolCallStart).ToolCall.ProviderData)
				requireProviderDataEqual(t, want, findEvent(t, all, ir.EventToolCallEnd).ToolCall.ProviderData)
			},
		},
		{
			name: "signature in the third chunk only reaches end",
			chunks: []string{
				toolChunk(firstChunk),
				toolChunk(`{"index":0,"function":{"arguments":"1"}}`),
				toolChunk(`{"index":0,"function":{"arguments":"}"},"signature":"t-sig","extra_content":{"google":{"thought_signature":"sig-3"}}}`),
			},
			wantLive: []ir.EventType{
				ir.EventStreamStart, ir.EventToolCallStart,
				ir.EventToolCallArgumentsDelta, ir.EventToolCallArgumentsDelta, ir.EventToolCallArgumentsDelta,
			},
			check: func(t *testing.T, all []ir.Event) {
				require.Len(t, toolEvents(all, ir.EventToolCallStart), 1)
				require.Nil(t, findEvent(t, all, ir.EventToolCallStart).ToolCall.ProviderData)
				end := findEvent(t, all, ir.EventToolCallEnd).ToolCall
				require.Equal(t, `{"a":1}`, end.Arguments)
				requireProviderDataEqual(t, providerData(map[string]string{
					"extra_content": `{"google":{"thought_signature":"sig-3"}}`,
					"signature":     `"t-sig"`,
				}), end.ProviderData)
			},
		},
		{
			name: "empty id on the first chunk keeps the generated id",
			chunks: []string{
				toolChunk(`{"index":0,"function":{"name":"write","arguments":"{"}}`),
				toolChunk(`{"index":0,"id":"call_late","function":{"arguments":"}"}}`),
			},
			wantLive: []ir.EventType{
				ir.EventStreamStart, ir.EventToolCallStart,
				ir.EventToolCallArgumentsDelta, ir.EventToolCallArgumentsDelta,
			},
			check: func(t *testing.T, all []ir.Event) {
				startID := findEvent(t, all, ir.EventToolCallStart).ToolCall.CallID
				require.True(t, strings.HasPrefix(startID, "call_") && len(startID) > len("call_"), startID)
				require.NotEqual(t, "call_late", startID)
				for _, delta := range toolEvents(all, ir.EventToolCallArgumentsDelta) {
					require.Equal(t, startID, delta.ToolCall.CallID)
				}
				require.Equal(t, startID, findEvent(t, all, ir.EventToolCallEnd).ToolCall.CallID)
				require.NoError(t, convert.AssertStreamingToolCallInvariant(all))
			},
		},
		{
			name: "text after a tool call is emitted immediately",
			chunks: []string{
				toolChunk(firstChunk),
				`{"choices":[{"delta":{"content":"working"}}]}`,
			},
			wantLive: []ir.EventType{
				ir.EventStreamStart, ir.EventToolCallStart, ir.EventToolCallArgumentsDelta, ir.EventContentDelta,
			},
			check: func(t *testing.T, all []ir.Event) {
				require.Equal(t, "working", findEvent(t, all, ir.EventContentDelta).Delta.Text)
				require.Less(t, indexEvent(all, ir.EventContentDelta), indexEvent(all, ir.EventToolCallEnd))
			},
		},
		{
			name: "parallel calls each start on their first chunk",
			chunks: []string{
				toolChunk(`{"index":0,"id":"call_a","function":{"name":"a","arguments":"{}"}}`),
				toolChunk(`{"index":1,"id":"call_b","function":{"name":"b","arguments":"{}"}}`),
			},
			wantLive: []ir.EventType{
				ir.EventStreamStart,
				ir.EventToolCallStart, ir.EventToolCallArgumentsDelta,
				ir.EventToolCallStart, ir.EventToolCallArgumentsDelta,
			},
			check: func(t *testing.T, all []ir.Event) {
				var endIDs []string
				for _, end := range toolEvents(all, ir.EventToolCallEnd) {
					endIDs = append(endIDs, end.ToolCall.CallID)
				}
				require.Equal(t, []string{"call_a", "call_b"}, endIDs)
				require.NoError(t, convert.AssertStreamingToolCallInvariant(all))
			},
		},
		{
			name: "later partial google object keeps the earlier thought signature",
			chunks: []string{
				toolChunk(`{"index":0,"id":"call_1","function":{"name":"write","arguments":"{}"},"extra_content":` + bigNumber + `}`),
				extraChunk(`{"google":{"note":"x"}}`),
			},
			wantLive: []ir.EventType{ir.EventStreamStart, ir.EventToolCallStart, ir.EventToolCallArgumentsDelta},
			check: func(t *testing.T, all []ir.Event) {
				requireProviderDataEqual(t,
					providerData(extraFields(`{"google":{"thought_signature":"sig-1","note":"x"},"vendor":{"n":9007199254740993}}`)),
					findEvent(t, all, ir.EventToolCallEnd).ToolCall.ProviderData)
			},
		},
		{
			name: "later empty value does not erase the thought signature",
			chunks: []string{
				toolChunk(`{"index":0,"id":"call_1","function":{"name":"write","arguments":"{}"},"extra_content":` + bigNumber + `}`),
				extraChunk(`{"google":{"thought_signature":""}}`),
			},
			wantLive: []ir.EventType{ir.EventStreamStart, ir.EventToolCallStart, ir.EventToolCallArgumentsDelta},
			check: func(t *testing.T, all []ir.Event) {
				requireProviderDataEqual(t, providerData(extraFields(bigNumber)), findEvent(t, all, ir.EventToolCallEnd).ToolCall.ProviderData)
			},
		},
		{
			name: "later non-empty value overrides the thought signature",
			chunks: []string{
				toolChunk(`{"index":0,"id":"call_1","function":{"name":"write","arguments":"{}"},"extra_content":` + bigNumber + `}`),
				extraChunk(`{"google":{"thought_signature":"sig-2"},"other":{"k":1}}`),
			},
			wantLive: []ir.EventType{ir.EventStreamStart, ir.EventToolCallStart, ir.EventToolCallArgumentsDelta},
			check: func(t *testing.T, all []ir.Event) {
				requireProviderDataEqual(t,
					providerData(extraFields(`{"google":{"thought_signature":"sig-2"},"vendor":{"n":9007199254740993},"other":{"k":1}}`)),
					findEvent(t, all, ir.EventToolCallEnd).ToolCall.ProviderData)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1. 用 io.Pipe 逐块写入，不写 finish。
			reader, writer := io.Pipe()
			defer reader.Close()
			events, err := (&handler{}).decodeHTTPResponse(&http.Response{StatusCode: http.StatusOK, Body: reader}, true)
			require.NoError(t, err)
			for _, chunk := range tt.chunks {
				_, err := writer.Write([]byte("data: " + chunk + "\n\n"))
				require.NoError(t, err)
			}

			// 2. 300ms 内应已收到工具调用的事件，不等 finish。
			var all []ir.Event
			for index, want := range tt.wantLive {
				select {
				case event := <-events:
					require.Equal(t, want, event.Type, "live event %d", index)
					all = append(all, event)
				case <-time.After(300 * time.Millisecond):
					require.FailNow(t, "event not emitted before finish", "want %v at %d, got %v", want, index, eventTypesOf(all))
				}
			}

			// 3. 写入 finish 收尾，取回剩余事件后检查。
			_, err = writer.Write([]byte("data: " + finishChunk + "\n\ndata: [DONE]\n\n"))
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			for event := range events {
				all = append(all, event)
			}
			tt.check(t, all)
		})
	}
}

// extra_content 合并按命名空间内的字段递归进行，字段值保持原样。
func TestMergeChatExtraContent(t *testing.T) {
	tests := []struct {
		name         string
		earlier, now string
		want         string
	}{
		{"disjoint namespaces", `{"google":{"a":"1"}}`, `{"vendor":{"b":"2"}}`, `{"google":{"a":"1"},"vendor":{"b":"2"}}`},
		{"same namespace different fields", `{"google":{"a":"1"}}`, `{"google":{"b":"2"}}`, `{"google":{"a":"1","b":"2"}}`},
		{"nested objects merge", `{"google":{"x":{"a":"1"}}}`, `{"google":{"x":{"b":"2"}}}`, `{"google":{"x":{"a":"1","b":"2"}}}`},
		{"later non-empty wins", `{"google":{"a":"1"}}`, `{"google":{"a":"2"}}`, `{"google":{"a":"2"}}`},
		{"later empty string keeps earlier", `{"google":{"a":"1"}}`, `{"google":{"a":""}}`, `{"google":{"a":"1"}}`},
		{"later null keeps earlier", `{"google":{"a":"1"}}`, `{"google":{"a":null}}`, `{"google":{"a":"1"}}`},
		{"later empty object keeps earlier", `{"google":{"a":"1"}}`, `{"google":{}}`, `{"google":{"a":"1"}}`},
		{"non-object value is replaced", `{"google":"old"}`, `{"google":{"a":"1"}}`, `{"google":{"a":"1"}}`},
		{"large integer is preserved", `{"vendor":{"n":9007199254740993}}`, `{"vendor":{"m":1}}`, `{"vendor":{"n":9007199254740993,"m":1}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var earlier, now map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(tt.earlier), &earlier))
			require.NoError(t, json.Unmarshal([]byte(tt.now), &now))

			got, err := json.Marshal(mergeChatExtraContent(earlier, now))
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
			require.Equal(t, strings.Contains(tt.want, "9007199254740993"), strings.Contains(string(got), "9007199254740993"))
		})
	}
}

// Chat 工具调用上的 extra_content、signature 和所在消息的 signature 收进该调用的 ProviderData。
func TestChatDecodeToolCallProviderData(t *testing.T) {
	const googleExtra = `{"google":{"thought_signature":"g-sig"},"vendor":{"n":9007199254740993}}`
	providerData := func(fields map[string]string) *ir.ToolCallProviderData {
		raw := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			raw[key] = json.RawMessage(value)
		}
		return &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat, Fields: raw}
	}
	decodeRequest := func(body string) func(*testing.T) ([]ir.ToolCall, *ir.Request) {
		return func(t *testing.T) ([]ir.ToolCall, *ir.Request) {
			request, err := (&handler{}).decodeHTTPRequest(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
			require.NoError(t, err)
			var calls []ir.ToolCall
			for _, message := range request.Messages {
				calls = append(calls, message.ToolCalls...)
			}
			return calls, request
		}
	}
	decodeResponse := func(stream bool, body string) func(*testing.T) ([]ir.ToolCall, *ir.Request) {
		return func(t *testing.T) ([]ir.ToolCall, *ir.Request) {
			events, err := (&handler{}).decodeHTTPResponse(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, stream)
			require.NoError(t, err)
			var calls []ir.ToolCall
			startIDs := map[int]string{}
			for _, event := range collectEventChannel(events) {
				switch event.Type {
				case ir.EventToolCallStart:
					startIDs[event.ToolCall.Index] = event.ToolCall.CallID
				case ir.EventToolCallEnd:
					require.Equal(t, startIDs[event.ToolCall.Index], event.ToolCall.CallID)
					calls = append(calls, ir.ToolCall{ID: event.ToolCall.CallID, ProviderData: event.ToolCall.ProviderData})
				case ir.EventToolCallDelta:
					calls = append(calls, ir.ToolCall{ID: event.Delta.ToolCall.ID, ProviderData: event.Delta.ToolCall.ProviderData})
				}
			}
			return calls, nil
		}
	}

	tests := []struct {
		name   string
		decode func(*testing.T) ([]ir.ToolCall, *ir.Request)
		want   []ir.ToolCall // ID 为空表示上游没给 ID，应生成 call_ 开头的 ID
	}{
		{
			name: "request call and message signatures",
			decode: decodeRequest(`{"model":"m","messages":[{"role":"assistant","signature":"m-sig","tool_calls":[` +
				`{"id":"call_1","type":"function","function":{"name":"a","arguments":"{}"},"signature":"t-sig","extra_content":` + googleExtra + `},` +
				`{"id":"call_2","type":"function","function":{"name":"b","arguments":"{}"}}]},` +
				`{"role":"tool","tool_call_id":"call_1","content":"ok"}]}`),
			want: []ir.ToolCall{
				{ID: "call_1", ProviderData: providerData(map[string]string{"extra_content": googleExtra, "signature": `"t-sig"`, "message_signature": `"m-sig"`})},
				{ID: "call_2", ProviderData: providerData(map[string]string{"message_signature": `"m-sig"`})},
			},
		},
		{
			name:   "request without signatures",
			decode: decodeRequest(`{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"a","arguments":"{}"}}]}]}`),
			want:   []ir.ToolCall{{ID: "call_1"}},
		},
		{
			name:   "request empty id",
			decode: decodeRequest(`{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"","type":"function","function":{"name":"a","arguments":"{}"}}]},{"role":"tool","tool_call_id":"","content":"ok"}]}`),
			want:   []ir.ToolCall{{}},
		},
		{
			name: "nonstream response",
			decode: decodeResponse(false, `{"choices":[{"message":{"role":"assistant","signature":"m-sig","tool_calls":[`+
				`{"id":"call_1","type":"function","function":{"name":"a","arguments":"{}"},"extra_content":`+googleExtra+`},`+
				`{"id":"","type":"function","function":{"name":"b","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`),
			want: []ir.ToolCall{
				{ID: "call_1", ProviderData: providerData(map[string]string{"extra_content": googleExtra, "message_signature": `"m-sig"`})},
				{ProviderData: providerData(map[string]string{"message_signature": `"m-sig"`})},
			},
		},
		{
			name: "stream signature in a later chunk",
			decode: decodeResponse(true, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"a","arguments":"{"}}]}}]}`+"\n\n"+
				"data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"},"signature":"t-sig","extra_content":`+googleExtra+`}]}}]}`+"\n\n"+
				"data: "+`{"choices":[{"delta":{"signature":"m-sig"},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n"),
			want: []ir.ToolCall{
				{ID: "call_1", ProviderData: providerData(map[string]string{"extra_content": googleExtra, "signature": `"t-sig"`, "message_signature": `"m-sig"`})},
			},
		},
		{
			name:   "stream without signatures",
			decode: decodeResponse(true, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"a","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n"),
			want:   []ir.ToolCall{{ID: "call_1"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, request := tt.decode(t)
			require.Len(t, got, len(tt.want))
			for index, want := range tt.want {
				if want.ID == "" {
					require.True(t, strings.HasPrefix(got[index].ID, "call_") && len(got[index].ID) > len("call_"), got[index].ID)
				} else {
					require.Equal(t, want.ID, got[index].ID)
				}
				requireProviderDataEqual(t, want.ProviderData, got[index].ProviderData)
			}
			if request == nil {
				return
			}
			// 带工具调用的 assistant 消息不再保留消息签名；工具结果跟随调用的 ID。
			for _, message := range request.Messages {
				if len(message.ToolCalls) > 0 {
					require.Empty(t, message.Signature)
				}
				if message.Role == ir.RoleTool {
					require.Equal(t, got[0].ID, message.ToolCallID)
				}
			}
		})
	}
}

// 纯文本 assistant 消息照旧把签名写进 Message.Signature。
func TestChatDecodeTextMessageKeepsSignature(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
	}{
		{name: "with signature", body: `{"model":"m","messages":[{"role":"assistant","content":"hi","signature":"text-sig"}]}`, want: "text-sig"},
		{name: "without signature", body: `{"model":"m","messages":[{"role":"assistant","content":"hi"}]}`, want: ""},
		{name: "empty content", body: `{"model":"m","messages":[{"role":"assistant","content":"","signature":"text-sig"}]}`, want: "text-sig"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request, err := (&handler{}).decodeHTTPRequest(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body)))
			require.NoError(t, err)
			require.Equal(t, tt.want, request.Messages[0].Signature)
		})
	}
}

// requireProviderDataEqual 比较 ProviderData，Fields 按 JSON 等值比较且保留大整数精度。
func requireProviderDataEqual(t *testing.T, want, got *ir.ToolCallProviderData) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.Equal(t, want.Protocol, got.Protocol)
	require.Equal(t, want.Models, got.Models)
	require.Len(t, got.Fields, len(want.Fields))
	for key, value := range want.Fields {
		require.JSONEq(t, string(value), string(got.Fields[key]), key)
		require.Equal(t, strings.Contains(string(value), "9007199254740993"), strings.Contains(string(got.Fields[key]), "9007199254740993"), key)
	}
}

func TestChatDecodeStreamReasoningLifecycleCompletesBeforeContent(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Need \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"inspect.\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Done\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	got := reasoningEvents(collectStreamEvents(t, sse))

	require.Equal(t, []ir.EventType{
		ir.EventReasoningSummaryDelta, ir.EventReasoningContentDelta, ir.EventThinkingDelta,
		ir.EventReasoningSummaryDelta, ir.EventReasoningContentDelta, ir.EventThinkingDelta,
		ir.EventReasoningDone, ir.EventContentDelta,
	}, eventTypesOf(got))
	done := got[6]
	require.Equal(t, ir.ReasoningCompleted, done.ReasoningStatus)
	require.Equal(t, []string{"Need inspect."}, done.Reasoning.Summary)
	require.Equal(t, []string{"Need inspect."}, done.Reasoning.Content)
	require.JSONEq(t, `{"reasoning_content":"Need inspect."}`, string(done.Reasoning.RawJSON))
}

func TestChatDecodeStreamReasoningCompletesBeforeToolCall(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Use tool.\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	got := reasoningEvents(collectStreamEvents(t, sse))
	require.Less(t, indexEvent(got, ir.EventReasoningDone), indexEvent(got, ir.EventToolCallStart))
}

func TestChatDecodeStreamReasoningAbruptEOFFinalizesInterrupted(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"partial\"}}]}\n\n"
	got := reasoningEvents(collectStreamEvents(t, sse))
	done := findEvent(t, got, ir.EventReasoningDone)
	require.Equal(t, ir.ReasoningInterrupted, done.ReasoningStatus)
	require.Equal(t, []string{"partial"}, done.Reasoning.Content)
}

func TestChatDecodeStreamReasoningOnlyCompletesWithFinishReason(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Think.\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	events := collectStreamEvents(t, sse)
	reasoningDone := findEvent(t, events, ir.EventReasoningDone)
	require.Equal(t, ir.ReasoningCompleted, reasoningDone.ReasoningStatus)
	require.Equal(t, 1, countEvents(events, ir.EventReasoningDone))
	require.Less(t, indexEvent(events, ir.EventReasoningDone), indexEvent(events, ir.EventDone))
}

func TestChatDecodeStreamReasoningOnlyCompletesWithDoneMarker(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Think.\"}}]}\n\n" +
		"data: [DONE]\n\n"
	events := collectStreamEvents(t, sse)
	reasoningDone := findEvent(t, events, ir.EventReasoningDone)
	require.Equal(t, ir.ReasoningCompleted, reasoningDone.ReasoningStatus)
	require.Equal(t, 1, countEvents(events, ir.EventReasoningDone))
	require.Less(t, indexEvent(events, ir.EventReasoningDone), indexEvent(events, ir.EventDone))
}

func TestChatDecodeStreamWithoutReasoningDoesNotEmitReasoningLifecycle(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	events := collectStreamEvents(t, sse)
	for _, eventType := range []ir.EventType{
		ir.EventReasoningSummaryDelta,
		ir.EventReasoningContentDelta,
		ir.EventThinkingDelta,
		ir.EventReasoningDone,
	} {
		require.Equal(t, -1, indexEvent(events, eventType))
	}
	require.NotEqual(t, -1, indexEvent(events, ir.EventContentDelta))
	require.NotEqual(t, -1, indexEvent(events, ir.EventDone))
}

func TestChatDecodeNonStreamReasoningCompletesBeforeContent(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","reasoning_content":"Think.","content":"Answer."},"finish_reason":"stop"}]}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	events, err := (&handler{}).decodeHTTPResponse(resp, false)
	require.NoError(t, err)
	got := reasoningEvents(collectEventChannel(events))
	require.Equal(t, []ir.EventType{
		ir.EventReasoningSummaryDelta,
		ir.EventReasoningContentDelta,
		ir.EventThinkingDelta,
		ir.EventReasoningDone,
		ir.EventContentDelta,
	}, eventTypesOf(got))
}

func TestChatDecodeNonStreamEmitsSingleLifecycleWithFinishReasonOnDone(t *testing.T) {
	tests := []struct {
		name              string
		choices           string
		wantFinish        string
		wantEncodedFinish string
	}{
		{
			name:              "reasoning and answer",
			choices:           `[{"message":{"role":"assistant","reasoning_content":"Think.","content":"Answer."},"finish_reason":"stop"}]`,
			wantFinish:        "stop",
			wantEncodedFinish: "stop",
		},
		{
			name:              "token limit",
			choices:           `[{"message":{"content":"Partial"},"finish_reason":"length"}]`,
			wantFinish:        "length",
			wantEncodedFinish: "length",
		},
		{
			name:              "tool calls normalize stop",
			choices:           `[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"stop"}]`,
			wantFinish:        "tool_calls",
			wantEncodedFinish: "tool_calls",
		},
		{
			name:              "missing finish reason",
			choices:           `[{"message":{"content":"Answer."}}]`,
			wantEncodedFinish: "stop",
		},
		{
			name:              "empty choices",
			choices:           `[]`,
			wantEncodedFinish: "stop",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := `{"choices":` + test.choices + `,"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
			events, err := (&handler{}).decodeHTTPResponse(resp, false)
			require.NoError(t, err)
			got := collectEventChannel(events)
			require.Equal(t, 1, countEvents(got, ir.EventStreamStart))
			require.Equal(t, ir.EventStreamStart, got[0].Type)
			require.Equal(t, 1, countEvents(got, ir.EventDone))
			require.Equal(t, ir.EventDone, got[len(got)-1].Type)
			require.Equal(t, test.wantFinish, got[len(got)-1].FinishReason)
			require.Equal(t, 1, countEvents(got, ir.EventUsage))
			require.Equal(t, ir.EventUsage, got[len(got)-2].Type)
			require.Equal(t, 3, got[len(got)-2].Usage.TotalTokens)
			for _, event := range got[:len(got)-1] {
				require.Empty(t, event.FinishReason)
			}

			chunks := parseChatSSE(runChatEncodeStream(t, got))
			var finishes []string
			for _, chunk := range chunks {
				for _, choice := range chunk.Choices {
					if choice.FinishReason != "" {
						finishes = append(finishes, choice.FinishReason)
					}
				}
			}
			require.Equal(t, []string{test.wantEncodedFinish}, finishes)
		})
	}
}

// ---------------------------------------------------------------------------
// llama.cpp `timings` usage fallback (no standard `usage` object)
// ---------------------------------------------------------------------------

// streamUsage 返回流事件里最后一个 EventUsage 的 Usage（无则 nil）。
func streamUsage(events []ir.Event) *ir.Usage {
	var u *ir.Usage
	for _, ev := range events {
		if ev.Type == ir.EventUsage {
			u = ev.Usage
		}
	}
	return u
}

func TestChatDecodeStream_TimingsUsageFallback(t *testing.T) {
	sse := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}]," +
		"\"timings\":{\"cache_n\":11660,\"prompt_n\":1478,\"predicted_n\":32}}\n\n" +
		"data: [DONE]\n\n"
	u := streamUsage(collectStreamEvents(t, sse))
	if u == nil {
		t.Fatal("expected EventUsage derived from timings, got none")
	}
	// prompt_n/predicted_n/cache_n 直映（互斥相加，不做 prompt_n+cache_n）
	if u.PromptTokens != 1478 || u.CompletionTokens != 32 || u.CacheReadTokens != 11660 {
		t.Fatalf("timings mapping wrong: %+v", u)
	}
}

func TestChatDecodeStream_UsagePreferredOverTimings(t *testing.T) {
	sse := "data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}]," +
		"\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":10}," +
		"\"timings\":{\"prompt_n\":1478,\"predicted_n\":32,\"cache_n\":11660}}\n\n" +
		"data: [DONE]\n\n"
	u := streamUsage(collectStreamEvents(t, sse))
	if u == nil || u.PromptTokens != 20 || u.CompletionTokens != 10 {
		t.Fatalf("usage must win over timings, got %+v", u)
	}
	if u.CacheReadTokens != 0 {
		t.Fatalf("timings cache_n must be ignored when usage present, got %+v", u)
	}
}

func TestChatDecodeStream_TimingsGatedWhenEmpty(t *testing.T) {
	// 全零 timings(别的上游偶发字段/无意义)→ 不产出 usage,落估算。
	sse := "data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}]," +
		"\"timings\":{\"prompt_n\":0,\"predicted_n\":0}}\n\n" +
		"data: [DONE]\n\n"
	if u := streamUsage(collectStreamEvents(t, sse)); u != nil {
		t.Fatalf("all-zero timings must not produce usage, got %+v", u)
	}
}

func TestChatDecodeStream_TimingsPromptOnlyTrusted(t *testing.T) {
	// 空响应(0 completion)但有 prompt/cache → 仍计费,不能因 0 输出丢掉 prompt。
	sse := "data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}]," +
		"\"timings\":{\"prompt_n\":1478,\"predicted_n\":0,\"cache_n\":500}}\n\n" +
		"data: [DONE]\n\n"
	u := streamUsage(collectStreamEvents(t, sse))
	if u == nil || u.PromptTokens != 1478 || u.CacheReadTokens != 500 || u.CompletionTokens != 0 {
		t.Fatalf("prompt-only timings should still bill prompt/cache, got %+v", u)
	}
}

func TestChatDecodeNonStream_TimingsUsageFallback(t *testing.T) {
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` +
		`"timings":{"prompt_n":1478,"predicted_n":32,"cache_n":11660}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	ch, err := (&handler{}).decodeHTTPResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var u *ir.Usage
	for ev := range ch {
		if ev.Type == ir.EventUsage {
			u = ev.Usage
		}
	}
	if u == nil || u.PromptTokens != 1478 || u.CompletionTokens != 32 || u.CacheReadTokens != 11660 {
		t.Fatalf("non-stream timings mapping wrong: %+v", u)
	}
}

// ---------------------------------------------------------------------------
// DecodeRequest from fixture files
// ---------------------------------------------------------------------------

func TestChatDecodeRequest_Simple(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../../../testdata", "openai_chat", "request_simple.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")

	c := &handler{}
	req, err := c.decodeHTTPRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if req.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", req.Model)
	}
	if req.Stream {
		t.Error("stream = true, want false")
	}
	if req.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want 1024", req.MaxTokens)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != ir.RoleSystem {
		t.Errorf("msg[0].role = %q, want system", req.Messages[0].Role)
	}
	if req.Messages[1].Role != ir.RoleUser {
		t.Errorf("msg[1].role = %q, want user", req.Messages[1].Role)
	}
	if len(req.Messages[1].Content) != 1 || req.Messages[1].Content[0].Text != "Hello!" {
		t.Errorf("msg[1].content = %v, want text 'Hello!'", req.Messages[1].Content)
	}
}

func TestChatDecodeRequest_Tools(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../../../testdata", "openai_chat", "request_tools.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")

	c := &handler{}
	req, err := c.decodeHTTPRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", req.Tools[0].Name)
	}
	if req.Tools[0].Description != "Get current weather" {
		t.Errorf("tool description = %q, want 'Get current weather'", req.Tools[0].Description)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" {
		t.Errorf("tool_choice = %v, want auto", req.ToolChoice)
	}
}

// ---------------------------------------------------------------------------
// O6: DecodeRequest must parse data: URIs into MediaB64 + MimeType
// ---------------------------------------------------------------------------

func TestChatDecodeRequest_DataURI(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "What is this?"},
					{"type": "image_url", "image_url": {"url": "data:image/png;base64,abc123"}}
				]
			}
		]
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &handler{}
	req, err := c.decodeHTTPRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(req.Messages))
	}
	msg := req.Messages[0]
	if len(msg.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(msg.Content))
	}

	img := msg.Content[1]
	if img.Type != ir.ContentTypeImage {
		t.Errorf("type = %q, want image", img.Type)
	}
	if img.MediaB64 != "abc123" {
		t.Errorf("MediaB64 = %q, want abc123", img.MediaB64)
	}
	if img.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want image/png", img.MimeType)
	}
	if img.MediaURL != "" {
		t.Errorf("MediaURL should be empty for data: URI, got %q", img.MediaURL)
	}
}

// ---------------------------------------------------------------------------
// O1: Stream decode must emit FinishReason from choice
// ---------------------------------------------------------------------------

func TestChatDecodeStream_FinishReason(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-fr","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-fr","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}

data: {"id":"chatcmpl-fr","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &handler{}
	ch, err := c.decodeHTTPResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []ir.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// There must be an event with FinishReason == "stop"
	found := false
	for _, ev := range events {
		if ev.FinishReason == "stop" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no event with FinishReason='stop' found; events: %+v", events)
	}
}

func TestChatDecodeStream_FinishReason_ToolCalls(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-tc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-tc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"fn","arguments":"{}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-tc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &handler{}
	ch, err := c.decodeHTTPResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []ir.Event
	for ev := range ch {
		events = append(events, ev)
	}

	found := false
	for _, ev := range events {
		if ev.FinishReason == "tool_calls" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no event with FinishReason='tool_calls' found; events: %+v", events)
	}
}

// ---------------------------------------------------------------------------
// DecodeStream tool calls
// ---------------------------------------------------------------------------

func TestChatDecodeStream_ToolCalls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../../../testdata", "openai_chat", "stream_tool_calls.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}

	c := &handler{}
	ch, errDec := c.decodeHTTPResponse(resp, true)
	if errDec != nil {
		t.Fatalf("DecodeResponse: %v", errDec)
	}

	var startEvents []ir.Event
	var argDeltas []ir.Event
	var endEvents []ir.Event
	for ev := range ch {
		switch ev.Type {
		case ir.EventToolCallStart:
			startEvents = append(startEvents, ev)
		case ir.EventToolCallArgumentsDelta:
			argDeltas = append(argDeltas, ev)
		case ir.EventToolCallEnd:
			endEvents = append(endEvents, ev)
		}
	}

	if len(startEvents) < 1 {
		t.Fatal("expected at least one EventToolCallStart")
	}
	first := startEvents[0]
	if first.ToolCall == nil {
		t.Fatal("first Start event missing ToolCall")
	}
	if first.ToolCall.CallID != "call_abc" {
		t.Errorf("tool call ID = %q, want call_abc", first.ToolCall.CallID)
	}
	if first.ToolCall.Name != "get_weather" {
		t.Errorf("tool call name = %q, want get_weather", first.ToolCall.Name)
	}

	var args strings.Builder
	for _, ev := range argDeltas {
		if ev.ToolCall != nil {
			args.WriteString(ev.ToolCall.Arguments)
		}
	}
	if args.String() != `{"city":"Tokyo"}` {
		t.Errorf("concatenated args = %q, want {\"city\":\"Tokyo\"}", args.String())
	}
	if len(endEvents) < 1 {
		t.Error("expected at least one EventToolCallEnd")
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse non-stream from fixtures
// ---------------------------------------------------------------------------

func TestChatDecodeResponse_Text(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../../../testdata", "openai_chat", "response_text.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}

	c := &handler{}
	ch, errDec := c.decodeHTTPResponse(resp, false)
	if errDec != nil {
		t.Fatalf("DecodeResponse: %v", errDec)
	}

	var events []ir.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Should have StreamStart, ContentDelta, Usage, Done (with FinishReason).
	if events[0].Type != ir.EventStreamStart {
		t.Errorf("first event type = %v, want StreamStart", events[0].Type)
	}
	if events[len(events)-1].Type != ir.EventDone {
		t.Errorf("last event type = %v, want Done", events[len(events)-1].Type)
	}

	// Find content
	foundContent := false
	for _, ev := range events {
		if ev.Type == ir.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "Hello! How can I help?" {
			foundContent = true
		}
	}
	if !foundContent {
		t.Error("missing content delta 'Hello! How can I help?'")
	}

	// Find usage with cached_tokens
	foundUsage := false
	for _, ev := range events {
		if ev.Type == ir.EventUsage && ev.Usage != nil {
			if ev.Usage.PromptTokens == 20 && ev.Usage.CompletionTokens == 10 && ev.Usage.CachedTokens == 5 {
				foundUsage = true
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event with cached_tokens")
	}
}

func TestChatDecodeResponse_ToolCalls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../../../testdata", "openai_chat", "response_tool_calls.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}

	c := &handler{}
	ch, errDec := c.decodeHTTPResponse(resp, false)
	if errDec != nil {
		t.Fatalf("DecodeResponse: %v", errDec)
	}

	var events []ir.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Find tool call delta
	foundTC := false
	for _, ev := range events {
		if ev.Type == ir.EventToolCallDelta && ev.Delta != nil && ev.Delta.ToolCall != nil {
			tc := ev.Delta.ToolCall
			if tc.ID == "call_abc" && tc.Name == "get_weather" && tc.Arguments == `{"city":"Tokyo"}` {
				foundTC = true
			}
		}
	}
	if !foundTC {
		t.Error("missing tool call delta from fixture")
	}

	// Find finish_reason == "tool_calls"
	foundFR := false
	for _, ev := range events {
		if ev.FinishReason == "tool_calls" {
			foundFR = true
		}
	}
	if !foundFR {
		t.Error("missing FinishReason='tool_calls'")
	}
}

// ---------------------------------------------------------------------------
// TesthandlerSetsInboundProtocol — Task 7: InboundProtocol field
// ---------------------------------------------------------------------------

func TestHandlerSetsInboundProtocol(t *testing.T) {
	body := `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	c := &handler{}
	req, err := c.decodeHTTPRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.InboundProtocol != ir.ProtocolOpenAIChat {
		t.Errorf("want %q, got %q", ir.ProtocolOpenAIChat, req.InboundProtocol)
	}
}

// ---------------------------------------------------------------------------
// Task 3: streaming tool_call events + empty content filter
// ---------------------------------------------------------------------------

func TestChatStreamDecode_ToolCallEventsCorrectShape(t *testing.T) {
	// Upstream chat-SSE: 1 chunk with id+name + 2 args fragment chunks + final
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"exec","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	events := collectStreamEvents(t, sse)

	var start, end *ir.Event
	var deltas []*ir.Event
	for i := range events {
		switch events[i].Type {
		case ir.EventToolCallStart:
			start = &events[i]
		case ir.EventToolCallArgumentsDelta:
			deltas = append(deltas, &events[i])
		case ir.EventToolCallEnd:
			end = &events[i]
		}
	}
	require.NotNil(t, start)
	require.Equal(t, "call_x", start.ToolCall.CallID)
	require.Equal(t, "exec", start.ToolCall.Name)
	require.Len(t, deltas, 2)
	require.Equal(t, `{"a"`, deltas[0].ToolCall.Arguments)
	require.Equal(t, `:1}`, deltas[1].ToolCall.Arguments)
	require.NotNil(t, end)
	require.Equal(t, `{"a":1}`, end.ToolCall.Arguments)

	require.NoError(t, convert.AssertStreamingToolCallInvariant(events))
}

// TestChatStreamDecode_TerminalChunkClosesOpenToolCalls covers the terminal
// shape emitted by vLLM for a forced named tool. OpenAI Chat represents the
// call itself in delta.tool_calls; the terminal finish_reason must not be used
// as the only signal for closing the already-open structured tool call.
//
// vLLM currently emits finish_reason="stop" for a named tool choice, while
// auto/required tool choices use finish_reason="tool_calls". All three cases
// below are complete structured tool calls and must satisfy the IR lifecycle:
// Start -> zero or more ArgumentsDelta -> End.
func TestChatStreamDecode_TerminalChunkClosesOpenToolCalls(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{
			name: "single tool with split arguments",
			chunks: []string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\""}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Tokyo\"}"}}]}}]}`,
			},
		},
		{
			name: "single tool with empty object arguments",
			chunks: []string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_list","type":"function","function":{"name":"list_files","arguments":"{}"}}]}}]}`,
			},
		},
		{
			name: "parallel tools",
			chunks: []string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}},{"index":1,"id":"call_time","type":"function","function":{"name":"get_time","arguments":"{\"timezone\":\"UTC\"}"}}]}}]}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := append([]string{}, tt.chunks...)
			lines = append(lines,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				``,
			)
			events := collectStreamEvents(t, strings.Join(lines, "\n\n"))

			require.NoError(t, convert.AssertStreamingToolCallInvariant(events))
		})
	}
}

func TestChatStreamDecode_NoEmptyContentDelta(t *testing.T) {
	// delta.content == null should not emit EventContentDelta
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`,
		`data: {"choices":[{"delta":{"content":""}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")
	events := collectStreamEvents(t, sse)
	for _, ev := range events {
		if ev.Type == ir.EventContentDelta {
			require.NotEmpty(t, ev.Delta.Text, "should not emit EventContentDelta with empty Text, got %+v", ev)
		}
	}
}

func TestChatDecode_RequestHistoryReasoningContentRoundtrip(t *testing.T) {
	body := `{
		"model": "deepseek-v4",
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "answer", "reasoning_content": "let me think..."}
		]
	}`

	httpReq := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(body)))
	httpReq.Header.Set("Content-Type", "application/json")

	c := &handler{}
	req, err := c.decodeHTTPRequest(httpReq)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.Messages) != 2 {
		t.Fatalf("messages count = %d, want 2", len(req.Messages))
	}
	asst := req.Messages[1]
	if asst.Role != ir.RoleAssistant {
		t.Fatalf("role = %q, want assistant", asst.Role)
	}
	// 第一个 block 应为 thinking（prepend 到首位）
	if len(asst.Content) < 1 || asst.Content[0].Type != ir.ContentTypeThinking {
		t.Fatalf("first content block type = %q, want thinking", asst.Content[0].Type)
	}
	if asst.Content[0].Text != "let me think..." {
		t.Fatalf("thinking text = %q, want %q", asst.Content[0].Text, "let me think...")
	}
	// 后续 block 应为 text
	if asst.Content[1].Type != ir.ContentTypeText || asst.Content[1].Text != "answer" {
		t.Fatalf("second block: type=%q text=%q", asst.Content[1].Type, asst.Content[1].Text)
	}
}
