package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/executionmode"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestReparseRequestFacts(t *testing.T) {
	tests := []struct {
		name  string
		start state.RelayInput
		body  string
		want  state.RelayInput
	}{
		{
			name:  "改了 model + stream",
			start: state.RelayInput{Model: "old", IsStream: false},
			body:  `{"model":"new","stream":true}`,
			want:  state.RelayInput{Model: "new", IsStream: true},
		},
		{
			name:  "不带 key：保持原值（未提供不等于清空）",
			start: state.RelayInput{Model: "keep", IsStream: true},
			body:  `{"foo":1}`,
			want:  state.RelayInput{Model: "keep", IsStream: true},
		},
		{
			name:  "非法 JSON：保持原值",
			start: state.RelayInput{Model: "keep", InboundProto: llmkit.ProtocolOpenAIChat, ToolCalls: llmkit.ToolCallRequestInfo{HasTools: true}},
			body:  `not json`,
			want:  state.RelayInput{Model: "keep", InboundProto: llmkit.ProtocolOpenAIChat, ToolCalls: llmkit.ToolCallRequestInfo{HasTools: true}},
		},
		{
			name:  "改写后带工具定义",
			start: state.RelayInput{Model: "m", InboundProto: llmkit.ProtocolOpenAIChat},
			body:  `{"model":"m","tools":[{"type":"function","function":{"name":"f"}}]}`,
			want:  state.RelayInput{Model: "m", InboundProto: llmkit.ProtocolOpenAIChat, ToolCalls: llmkit.ToolCallRequestInfo{HasTools: true}},
		},
		{
			name:  "改写后去掉工具定义",
			start: state.RelayInput{Model: "m", InboundProto: llmkit.ProtocolOpenAIChat, ToolCalls: llmkit.ToolCallRequestInfo{HasTools: true}},
			body:  `{"model":"m","messages":[]}`,
			want:  state.RelayInput{Model: "m", InboundProto: llmkit.ProtocolOpenAIChat},
		},
		{
			name:  "不支持工具的协议：特征保持为空",
			start: state.RelayInput{Model: "m", InboundProto: llmkit.ProtocolUnknown},
			body:  `{"model":"m","tools":[{"type":"function"}]}`,
			want:  state.RelayInput{Model: "m", InboundProto: llmkit.ProtocolUnknown},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx := &state.RelayContext{Input: tt.start}
			reparseRequestFacts(rctx, []byte(tt.body))
			assert.Equal(t, tt.want, rctx.Input)
		})
	}
}

// TestApplyRequestScriptsRefreshesToolCalls 脚本改写请求体后，本地选出的执行模式要和远端按改写后 body 重算的一致。
func TestApplyRequestScriptsRefreshesToolCalls(t *testing.T) {
	const (
		plainChat = `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
		toolChat  = `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f"}}]}`
	)
	tests := []struct {
		name      string
		code      string
		body      string
		wantTools llmkit.ToolCallRequestInfo
		wantMode  attemptproxy.ExecutionMode
	}{
		{
			name:      "脚本删掉 tools 后不再要求协议转换",
			code:      `function onRequest(ctx) { delete ctx.body.tools }`,
			body:      toolChat,
			wantTools: llmkit.ToolCallRequestInfo{},
			wantMode:  attemptproxy.ModePassthrough,
		},
		{
			name:      "脚本给纯文本 Chat 请求加 tools 后必须走协议转换",
			code:      `function onRequest(ctx) { ctx.body.tools = [{type: "function", function: {name: "f"}}] }`,
			body:      plainChat,
			wantTools: llmkit.ToolCallRequestInfo{HasTools: true},
			wantMode:  attemptproxy.ModeNative,
		},
		{
			name:      "脚本往历史里塞打包的工具调用 ID",
			code:      `function onRequest(ctx) { ctx.body.messages.push({role: "tool", tool_call_id: "call_llmkit_v1_abc", content: "x"}) }`,
			body:      plainChat,
			wantTools: llmkit.ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true},
			wantMode:  attemptproxy.ModeNative,
		},
		{
			name:      "脚本没动请求体时特征保持不变",
			code:      `function onRequest(ctx) { }`,
			body:      toolChat,
			wantTools: llmkit.ToolCallRequestInfo{HasTools: true},
			wantMode:  attemptproxy.ModeNative,
		},
	}
	channel := &models.Channel{ChannelCore: models.ChannelCore{
		Type:               consts.ChannelTypeOpenAI,
		PassthroughEnabled: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newResourceTestHandler(scriptCache(t, tt.code), &handlerTrackingBodyStore{})
			rctx, _ := scriptTestRelayContext(t, h, &handlerTrackingReplayBody{data: []byte(tt.body)})
			rctx.Input.InboundProto = llmkit.ProtocolOpenAIChat
			before, err := llmkit.InspectToolCallRequest(rctx.Input.InboundProto, rctx.Input.Body)
			require.NoError(t, err)
			rctx.Input.ToolCalls = before

			rejected, err := h.applyRequestScripts(rctx)
			require.NoError(t, err)
			require.False(t, rejected)

			// 远端 agent 收到的是改写后的请求体，ctxbuild 会按它重算特征。
			rewritten, err := io.ReadAll(rctx.Context.Request.Body)
			require.NoError(t, err)
			remote, err := llmkit.InspectToolCallRequest(rctx.Input.InboundProto, rewritten)
			require.NoError(t, err)

			assert.Equal(t, tt.wantTools, rctx.Input.ToolCalls)
			assert.Equal(t, remote, rctx.Input.ToolCalls)
			localMode := executionmode.ForChannel(channel, "gpt-4", rctx.Input.InboundProto, rctx.Input.ToolCalls)
			remoteMode := executionmode.ForChannel(channel, "gpt-4", rctx.Input.InboundProto, remote)
			assert.Equal(t, tt.wantMode, localMode)
			assert.Equal(t, remoteMode, localMode)
		})
	}
}

func TestApplyRequestScriptsAtomicallyReplacesReplayBody(t *testing.T) {
	store := scriptCache(t, mutateModelStreamScript)
	nextStore := &handlerTrackingBodyStore{}
	h := newResourceTestHandler(store, nextStore)
	old := &handlerTrackingReplayBody{data: []byte(`{"model":"old","stream":false}`)}
	rctx, oldReader := scriptTestRelayContext(t, h, old)

	rejected, err := h.applyRequestScripts(rctx)
	if err != nil {
		t.Fatal(err)
	}
	if rejected {
		t.Fatal("mutating script unexpectedly rejected request")
	}
	if old.bodyCloses.Load() != 1 {
		t.Fatalf("old ReplayBody.Close calls = %d, want 1", old.bodyCloses.Load())
	}
	if old.readerCloses.Load() != 1 {
		t.Fatalf("old request reader closes = %d, want 1", old.readerCloses.Load())
	}
	if rctx.Resources.Body() == old {
		t.Fatal("resources still expose old body after successful replacement")
	}
	want := []byte(`{"model":"new","stream":true}`)
	got, err := io.ReadAll(rctx.Context.Request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("request body = %s, want %s", got, want)
	}
	copyReader, err := rctx.Context.Request.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	copyBody, err := io.ReadAll(copyReader)
	if err != nil {
		t.Fatal(err)
	}
	_ = copyReader.Close()
	if !bytes.Equal(copyBody, want) {
		t.Fatalf("GetBody replay = %s, want %s", copyBody, want)
	}
	if rctx.Input.Model != "new" || !rctx.Input.IsStream {
		t.Fatalf("reparsed input = model %q stream %v", rctx.Input.Model, rctx.Input.IsStream)
	}
	if oldReader == rctx.Context.Request.Body {
		t.Fatal("request still owns old reader")
	}
}

func TestApplyRequestScriptsCaptureFailureKeepsOldBodyUsable(t *testing.T) {
	wantErr := errors.New("replacement capture failed")
	h := newResourceTestHandler(scriptCache(t, mutateModelStreamScript), &handlerTrackingBodyStore{captureErr: wantErr})
	oldBytes := []byte(`{"model":"old","stream":false}`)
	old := &handlerTrackingReplayBody{data: oldBytes}
	rctx, oldReader := scriptTestRelayContext(t, h, old)

	rejected, err := h.applyRequestScripts(rctx)
	if rejected {
		t.Fatal("capture failure must not be reported as script rejection")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want capture failure", err)
	}
	if rctx.Resources.Body() != old {
		t.Fatal("failed replacement changed the owned body")
	}
	if old.bodyCloses.Load() != 0 {
		t.Fatalf("failed replacement closed old body %d times", old.bodyCloses.Load())
	}
	if rctx.Context.Request.Body != oldReader {
		t.Fatal("failed replacement changed the current request reader")
	}
	got, readErr := io.ReadAll(rctx.Context.Request.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBytes) {
		t.Fatalf("old request body = %s, want %s", got, oldBytes)
	}
	if !bytes.Equal(rctx.Input.Body, oldBytes) {
		t.Fatalf("RelayInput.Body changed on capture failure: %s", rctx.Input.Body)
	}
}

func TestApplyRequestScriptsOpenFailureKeepsOldBodyAndReaderUsable(t *testing.T) {
	secretErr := errors.New("replacement open failed at /secret/spill")
	next := &handlerTrackingReplayBody{openErr: secretErr}
	h := newResourceTestHandler(
		scriptCache(t, mutateModelStreamScript),
		&handlerTrackingBodyStore{nextBody: next},
	)
	oldBytes := []byte(`{"model":"old","stream":false}`)
	old := &handlerTrackingReplayBody{data: oldBytes}
	rctx, oldReader := scriptTestRelayContext(t, h, old)

	rejected, err := h.applyRequestScripts(rctx)
	if rejected {
		t.Fatal("Open failure must not be reported as script rejection")
	}
	var coded interface{ BodyErrorCode() string }
	if !errors.Is(err, secretErr) || !errors.As(err, &coded) || coded.BodyErrorCode() != "body_store_failed" {
		t.Fatalf("error = %v, want typed body_store_failed wrapping Open failure", err)
	}
	if err.Error() != "body_store_failed" || bytes.Contains([]byte(err.Error()), []byte("secret")) {
		t.Fatalf("Open failure is not sanitized: %v", err)
	}
	if rctx.Resources.Body() != old || old.bodyCloses.Load() != 0 {
		t.Fatal("Open failure changed or closed old ReplayBody")
	}
	if rctx.Context.Request.Body != oldReader {
		t.Fatal("Open failure changed the current request reader")
	}
	got, readErr := io.ReadAll(rctx.Context.Request.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBytes) || !bytes.Equal(rctx.Input.Body, oldBytes) {
		t.Fatalf("old body changed: reader=%s input=%s", got, rctx.Input.Body)
	}
	if next.bodyCloses.Load() != 1 {
		t.Fatalf("failed replacement Close calls = %d, want 1", next.bodyCloses.Load())
	}
}

// mutateModelStreamScript 把请求的 model 改成 new、stream 改成 true。
const mutateModelStreamScript = `function onRequest(ctx) { ctx.body.model = "new"; ctx.body.stream = true }`

func scriptCache(t *testing.T, code string) *cache.Store {
	t.Helper()
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.LoadScripts([]models.AdminScript{{
		ID: 1, Name: "mutate", Enabled: true,
		Code:  code,
		Scope: datatypes.NewJSONType(models.ScriptScope{}),
	}})
	return store
}

func scriptTestRelayContext(t *testing.T, h *Handler, old *handlerTrackingReplayBody) (*state.RelayContext, io.ReadCloser) {
	t.Helper()
	resources := &state.RequestResources{}
	if err := resources.Replace(context.Background(), fixedReplayStore{body: old}, bytes.NewReader(nil), app.BodyLimits{}); err != nil {
		t.Fatal(err)
	}
	reader, err := old.Open()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Body = reader
	rctx := &state.RelayContext{
		Context:   c,
		Agent:     h.Agent,
		Resources: resources,
		Input: state.RelayInput{
			Body:       append([]byte(nil), old.data...),
			Model:      "old",
			BodyLimits: app.BodyLimits{MemoryThreshold: 8, HardLimit: 1024},
		},
	}
	t.Cleanup(func() {
		_ = c.Request.Body.Close()
		_ = resources.Close()
	})
	return rctx, reader
}

type fixedReplayStore struct {
	body app.ReplayBody
}

func (s fixedReplayStore) Capture(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
	return s.body, nil
}
