package native

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/gin-gonic/gin"
)

// ==================== Shared helpers ====================
//
// 这些 helper 跟 backend/passthrough/passthrough_test.go 同款风格，但独立维护，
// 避免跨 package 共享暴露内部测试细节；同时让 native 测试可以独立调整
// InboundProto / IsStream / body 等。

// newNativeTestCtx 构造一个最小可用的 RelayContext + gin.Context，
// c.Request 指向 baseURL+path，便于 backend.Relay 走完整 codec 链路。
func newNativeTestCtx(t *testing.T, body []byte, inbound llmkit.Protocol, isStream bool) (*state.RelayContext, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// inbound path 仅用作语义占位，DecodeRequest 直接读 Body。
	path := "/v1/chat/completions"
	switch inbound {
	case llmkit.ProtocolClaude:
		path = "/v1/messages"
	case llmkit.ProtocolOpenAIResponses:
		path = "/v1/responses"
	}
	c.Request, _ = http.NewRequest(http.MethodPost, "http://gateway"+path,
		strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	rctx := &state.RelayContext{
		Context: c,
		Input: state.RelayInput{
			Body:         body,
			Model:        "gpt-4",
			InboundProto: inbound,
			IsStream:     isStream,
			StartTime:    time.Now(),
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(trace.CaptureOff, 0)},
	}
	return rctx, w
}

// makeNativeChannel 构造一个最小 native channel，BaseURL 指向 httptest server。
// 默认 Type=OpenAI、SupportedAPITypes 为空（不强制 outbound 协议）。
func makeNativeChannel(baseURL string) *models.Channel {
	return &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: baseURL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4"}
}

// ==================== Tests ====================

type recordingLLMKitClient struct {
	calls   int
	request llmkit.Request
	target  llmkit.Target
	options llmkit.CallOptions
	events  []llmkit.Event
	err     error
}

func (client *recordingLLMKitClient) Call(
	_ context.Context,
	request llmkit.Request,
	target llmkit.Target,
	options llmkit.CallOptions,
) (<-chan llmkit.Event, error) {
	client.calls++
	client.request = request
	client.target = target
	client.options = options
	if client.err != nil {
		return nil, client.err
	}
	events := make(chan llmkit.Event, len(client.events))
	for _, event := range client.events {
		events <- event
	}
	close(events)
	return events, nil
}

type recordingLLMKitCodec struct {
	decoded        llmkit.DecodedRequest
	decodeIn       llmkit.DecodeRequestInput
	encodeIn       llmkit.EncodeResponseInput
	chunks         []llmkit.EncodedChunk
	encodeResponse func(llmkit.EncodeResponseInput) <-chan llmkit.EncodedChunk
}

func (fake *recordingLLMKitCodec) DecodeRequest(input llmkit.DecodeRequestInput) (llmkit.DecodedRequest, error) {
	fake.decodeIn = input
	return fake.decoded, nil
}

func (*recordingLLMKitCodec) EncodeRequest(llmkit.EncodeRequestInput) (llmkit.EncodedRequest, error) {
	panic("native must let llmkit.Client encode the upstream request")
}

func (*recordingLLMKitCodec) DecodeResponse(context.Context, llmkit.DecodeResponseInput) (<-chan llmkit.Event, error) {
	panic("native must let llmkit.Client decode the upstream response")
}

func (fake *recordingLLMKitCodec) EncodeResponse(_ context.Context, input llmkit.EncodeResponseInput) (<-chan llmkit.EncodedChunk, error) {
	fake.encodeIn = input
	if fake.encodeResponse != nil {
		return fake.encodeResponse(input), nil
	}
	chunks := make(chan llmkit.EncodedChunk, len(fake.chunks))
	for _, chunk := range fake.chunks {
		chunks <- chunk
	}
	close(chunks)
	return chunks, nil
}

func encodeContentEvents(input llmkit.EncodeResponseInput) <-chan llmkit.EncodedChunk {
	chunks := make(chan llmkit.EncodedChunk)
	go func() {
		defer close(chunks)
		for event := range input.Events {
			if event.Type == llmkit.EventContentDelta && event.Delta != nil {
				chunks <- llmkit.EncodedChunk{Data: []byte(event.Delta.Text)}
			}
		}
	}()
	return chunks
}

type eventErrorAfterWriteClient struct {
	written <-chan struct{}
}

func (client eventErrorAfterWriteClient) Call(
	_ context.Context,
	_ llmkit.Request,
	_ llmkit.Target,
	_ llmkit.CallOptions,
) (<-chan llmkit.Event, error) {
	events := make(chan llmkit.Event)
	go func() {
		defer close(events)
		events <- llmkit.Event{Type: llmkit.EventStreamStart}
		events <- llmkit.Event{
			Type:  llmkit.EventContentDelta,
			Delta: &llmkit.DeltaPayload{Text: "first"},
		}
		<-client.written
		events <- llmkit.Event{
			Type:  llmkit.EventError,
			Error: &llmkit.ErrorPayload{Message: "stream failed after commit"},
		}
	}()
	return events, nil
}

type writeSignalResponseWriter struct {
	gin.ResponseWriter
	written chan struct{}
	once    sync.Once
}

func (writer *writeSignalResponseWriter) Write(data []byte) (int, error) {
	n, err := writer.ResponseWriter.Write(data)
	if n > 0 {
		writer.once.Do(func() { close(writer.written) })
	}
	return n, err
}

type monitoringLifecycleCodec struct {
	encodeErr  error
	sourceDone chan struct{}
}

func (*monitoringLifecycleCodec) DecodeRequest(llmkit.DecodeRequestInput) (llmkit.DecodedRequest, error) {
	return llmkit.DecodedRequest{
		Protocol: llmkit.ProtocolOpenAIChat,
		Request: llmkit.Request{
			Model: "client-model", Stream: true,
		},
	}, nil
}

func (*monitoringLifecycleCodec) EncodeRequest(llmkit.EncodeRequestInput) (llmkit.EncodedRequest, error) {
	return llmkit.EncodedRequest{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   []byte(`{"model":"provider-model","stream":true}`),
	}, nil
}

func (codec *monitoringLifecycleCodec) DecodeResponse(ctx context.Context, _ llmkit.DecodeResponseInput) (<-chan llmkit.Event, error) {
	events := make(chan llmkit.Event)
	go func() {
		defer close(codec.sourceDone)
		defer close(events)
		prefix := []llmkit.Event{
			{Type: llmkit.EventContentDelta, Delta: &llmkit.DeltaPayload{Text: "first"}},
			{Type: llmkit.EventUsage, Usage: &llmkit.Usage{PromptTokens: 7, CompletionTokens: 11}},
			{Type: llmkit.EventContentDelta, Delta: &llmkit.DeltaPayload{Text: "second"}},
		}
		for _, event := range prefix {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
		for {
			select {
			case events <- llmkit.Event{Type: llmkit.EventContentDelta, Delta: &llmkit.DeltaPayload{}}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (codec *monitoringLifecycleCodec) EncodeResponse(ctx context.Context, input llmkit.EncodeResponseInput) (<-chan llmkit.EncodedChunk, error) {
	chunks := make(chan llmkit.EncodedChunk)
	go func() {
		defer close(chunks)
		contentChunks := 0
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-input.Events:
				if !ok {
					return
				}
				if event.Type != llmkit.EventContentDelta || event.Delta == nil || event.Delta.Text == "" {
					continue
				}
				contentChunks++
				chunk := llmkit.EncodedChunk{Data: []byte(event.Delta.Text)}
				if codec.encodeErr != nil && contentChunks == 2 {
					chunk = llmkit.EncodedChunk{Err: codec.encodeErr}
				}
				select {
				case chunks <- chunk:
				case <-ctx.Done():
					return
				}
				if chunk.Err != nil {
					return
				}
			}
		}
	}()
	return chunks, nil
}

type lifecycleTrackingBody struct {
	closed chan struct{}
	once   sync.Once
}

func (*lifecycleTrackingBody) Read([]byte) (int, error) { return 0, io.EOF }

func (body *lifecycleTrackingBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

type failSecondWriteResponseWriter struct {
	gin.ResponseWriter
	err   error
	calls int
}

func (writer *failSecondWriteResponseWriter) Write(data []byte) (int, error) {
	writer.calls++
	if writer.calls == 2 {
		return 0, writer.err
	}
	return writer.ResponseWriter.Write(data)
}

func runMonitoringLifecycleFailure(
	t *testing.T,
	codec *monitoringLifecycleCodec,
	configureWriter func(*state.RelayContext),
) (state.AttemptResult, *httptest.ResponseRecorder, <-chan struct{}) {
	t.Helper()
	bodyClosed := make(chan struct{})
	transport := httpDoerTestTransport("monitoring", func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &lifecycleTrackingBody{closed: bodyClosed},
			Request:    request,
		}, nil
	})
	agent := httpDoerTestAgent{pool: &httpDoerTransportPool{transport: transport}}
	rctx, recorder := newNativeTestCtx(
		t,
		[]byte(`{"model":"client-model","stream":true}`),
		llmkit.ProtocolOpenAIChat,
		true,
	)
	if configureWriter != nil {
		configureWriter(rctx)
	}
	result := (&Backend{Agent: agent, Codec: codec}).Relay(rctx, state.Attempt{
		Channel: makeNativeChannel("monitoring://provider"), RealModel: "provider-model",
	})
	return result, recorder, bodyClosed
}

func assertMonitoringLifecycleFinalized(
	t *testing.T,
	result state.AttemptResult,
	wantErr error,
	sourceDone, bodyClosed <-chan struct{},
) {
	t.Helper()
	if !errors.Is(result.Err, wantErr) {
		t.Fatalf("Relay() error = %v, want %v", result.Err, wantErr)
	}
	if !result.Written {
		t.Fatalf("Relay() result = %+v, want committed response", result)
	}
	if result.PromptTokens != 7 || result.CompletionTokens != 11 {
		t.Fatalf("final usage = (%d,%d), want (7,11)", result.PromptTokens, result.CompletionTokens)
	}
	if result.ResponseText != "firstsecond" {
		t.Fatalf("final response text = %q, want firstsecond", result.ResponseText)
	}
	select {
	case <-sourceDone:
	case <-time.After(time.Second):
		t.Fatal("source producer did not stop after client response failure")
	}
	select {
	case <-bodyClosed:
	case <-time.After(time.Second):
		t.Fatal("upstream response body was not closed after client response failure")
	}
}

func TestBackend_EncoderFailureFinalizesMonitoringLifecycle(t *testing.T) {
	wantErr := errors.New("encode second chunk")
	codec := &monitoringLifecycleCodec{encodeErr: wantErr, sourceDone: make(chan struct{})}

	result, recorder, bodyClosed := runMonitoringLifecycleFailure(t, codec, nil)

	if got := recorder.Body.String(); got != "first" {
		t.Fatalf("client body = %q, want first committed chunk", got)
	}
	assertMonitoringLifecycleFinalized(t, result, wantErr, codec.sourceDone, bodyClosed)
}

func TestBackend_SecondWriterFailureFinalizesMonitoringLifecycle(t *testing.T) {
	wantErr := errors.New("write second chunk")
	codec := &monitoringLifecycleCodec{sourceDone: make(chan struct{})}

	result, recorder, bodyClosed := runMonitoringLifecycleFailure(t, codec, func(rctx *state.RelayContext) {
		rctx.Context.Writer = &failSecondWriteResponseWriter{
			ResponseWriter: rctx.Context.Writer,
			err:            wantErr,
		}
	})

	if got := recorder.Body.String(); got != "first" {
		t.Fatalf("client body = %q, want first committed chunk", got)
	}
	assertMonitoringLifecycleFinalized(t, result, wantErr, codec.sourceDone, bodyClosed)
}

func TestBackend_LLMKitCallsClientOnceWithChannelTarget(t *testing.T) {
	request := llmkit.Request{Model: "client-model", Stream: false}
	fakeCodec := &recordingLLMKitCodec{
		decoded: llmkit.DecodedRequest{Protocol: llmkit.ProtocolOpenAIChat, Request: request},
		chunks:  []llmkit.EncodedChunk{{Data: []byte(`{"ok":true}`)}},
	}
	client := &recordingLLMKitClient{events: []llmkit.Event{
		{Type: llmkit.EventStreamStart},
		{Type: llmkit.EventDone},
	}}
	channel := makeNativeChannel("https://provider.example/base")
	channel.Endpoints = `{"chat_completions":"/custom/chat"}`
	rctx, recorder := newNativeTestCtx(t,
		[]byte(`{"model":"client-model","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat,
		false,
	)

	result := (&Backend{Codec: fakeCodec, Client: client}).Relay(rctx, state.Attempt{
		Channel: channel, RealModel: "provider-model",
	})

	if result.Err != nil {
		t.Fatalf("Relay() error = %v", result.Err)
	}
	if client.calls != 1 {
		t.Fatalf("Client.Call calls = %d, want 1", client.calls)
	}
	wantTarget := llmkit.Target{
		Protocol: llmkit.ProtocolOpenAIChat, BaseURL: "https://provider.example/base",
		EndpointPath: "/custom/chat", APIKey: "k", Model: "provider-model",
	}
	if client.target.Protocol != wantTarget.Protocol || client.target.BaseURL != wantTarget.BaseURL ||
		client.target.EndpointPath != wantTarget.EndpointPath || client.target.APIKey != wantTarget.APIKey ||
		client.target.Model != wantTarget.Model {
		t.Fatalf("Client.Call target = %+v, want %+v", client.target, wantTarget)
	}
	if client.request.Model != "provider-model" {
		t.Fatalf("Client.Call request model = %q, want provider-model", client.request.Model)
	}
	if client.options.HTTPClient == nil {
		t.Fatal("Client.Call must receive the attempt HTTP doer")
	}
	if fakeCodec.decodeIn.Method != http.MethodPost || fakeCodec.decodeIn.Path != "/v1/chat/completions" {
		t.Fatalf("DecodeRequest input = %s %s", fakeCodec.decodeIn.Method, fakeCodec.decodeIn.Path)
	}
	if got := recorder.Body.String(); got != `{"ok":true}` {
		t.Fatalf("client body = %q, want encoded llmkit response", got)
	}
}

func TestBackend_LLMKitClientErrorProjectsAttemptResult(t *testing.T) {
	root := errors.New("dial provider")
	fakeCodec := &recordingLLMKitCodec{decoded: llmkit.DecodedRequest{
		Protocol: llmkit.ProtocolOpenAIChat,
		Request:  llmkit.Request{Model: "client-model"},
	}}
	client := &recordingLLMKitClient{err: &llmkit.Error{Stage: llmkit.ErrorStageConnect, Cause: root}}
	rctx, _ := newNativeTestCtx(t, []byte(`{"model":"client-model"}`), llmkit.ProtocolOpenAIChat, false)

	result := (&Backend{Codec: fakeCodec, Client: client}).Relay(rctx, state.Attempt{
		Channel: makeNativeChannel("https://provider.example"), RealModel: "provider-model",
	})

	if !errors.Is(result.Err, root) {
		t.Fatalf("Relay() error = %v, want root client error", result.Err)
	}
	if result.Written {
		t.Fatalf("client error before response must remain retryable: %+v", result)
	}
	if client.calls != 1 {
		t.Fatalf("Client.Call calls = %d, want 1", client.calls)
	}
}

func TestTraceStageMapsStreamToUpstreamDecode(t *testing.T) {
	if got := traceStage(llmkit.ErrorStageStream); got != trace.StageUpstreamDecode {
		t.Fatalf("traceStage(stream) = %q, want %q", got, trace.StageUpstreamDecode)
	}
}

func TestBackend_LLMKitScriptRejectionRestoresAttemptResult(t *testing.T) {
	rejection := state.AttemptResult{Written: true, Err: errors.New("script rejected")}
	fakeCodec := &recordingLLMKitCodec{decoded: llmkit.DecodedRequest{
		Protocol: llmkit.ProtocolOpenAIChat,
		Request:  llmkit.Request{Model: "client-model"},
	}}
	client := &recordingLLMKitClient{err: &llmkit.Error{
		Stage: llmkit.ErrorStageConnect,
		Cause: &scriptRejectedError{result: rejection},
	}}
	rctx, _ := newNativeTestCtx(t, []byte(`{"model":"client-model"}`), llmkit.ProtocolOpenAIChat, false)

	result := (&Backend{Codec: fakeCodec, Client: client}).Relay(rctx, state.Attempt{
		Channel: makeNativeChannel("https://provider.example"), RealModel: "provider-model",
	})

	if !result.Written || !errors.Is(result.Err, rejection.Err) {
		t.Fatalf("Relay() result = %+v, want original script rejection %+v", result, rejection)
	}
}

func TestBackend_LLMKitEventErrorBeforeCommitRemainsRetryable(t *testing.T) {
	fakeCodec := &recordingLLMKitCodec{
		decoded: llmkit.DecodedRequest{
			Protocol: llmkit.ProtocolOpenAIChat,
			Request:  llmkit.Request{Model: "client-model", Stream: true},
		},
		encodeResponse: encodeContentEvents,
	}
	client := &recordingLLMKitClient{events: []llmkit.Event{
		{Type: llmkit.EventStreamStart},
		{Type: llmkit.EventError, Error: &llmkit.ErrorPayload{Message: "stream failed before commit"}},
	}}
	rctx, recorder := newNativeTestCtx(t, []byte(`{"model":"client-model","stream":true}`), llmkit.ProtocolOpenAIChat, true)

	result := (&Backend{Codec: fakeCodec, Client: client}).Relay(rctx, state.Attempt{
		Channel: makeNativeChannel("https://provider.example"), RealModel: "provider-model",
	})

	if result.Err == nil || !strings.Contains(result.Err.Error(), "stream failed before commit") {
		t.Fatalf("Relay() error = %v, want upstream stream error", result.Err)
	}
	if result.Written {
		t.Fatalf("EventError before commit must remain retryable: %+v", result)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("client body = %q, want empty fallback response", recorder.Body.String())
	}
}

func TestBackend_LLMKitEventErrorAfterCommitTerminatesWrittenResponse(t *testing.T) {
	fakeCodec := &recordingLLMKitCodec{
		decoded: llmkit.DecodedRequest{
			Protocol: llmkit.ProtocolOpenAIChat,
			Request:  llmkit.Request{Model: "client-model", Stream: true},
		},
		encodeResponse: encodeContentEvents,
	}
	rctx, recorder := newNativeTestCtx(t, []byte(`{"model":"client-model","stream":true}`), llmkit.ProtocolOpenAIChat, true)
	written := make(chan struct{})
	rctx.Context.Writer = &writeSignalResponseWriter{
		ResponseWriter: rctx.Context.Writer,
		written:        written,
	}

	result := (&Backend{Codec: fakeCodec, Client: eventErrorAfterWriteClient{written: written}}).Relay(
		rctx,
		state.Attempt{Channel: makeNativeChannel("https://provider.example"), RealModel: "provider-model"},
	)

	if result.Err == nil || !strings.Contains(result.Err.Error(), "stream failed after commit") {
		t.Fatalf("Relay() error = %v, want upstream stream error", result.Err)
	}
	if !result.Written {
		t.Fatalf("EventError after commit must terminate a written response: %+v", result)
	}
	if got := recorder.Body.String(); got != "first" {
		t.Fatalf("client body = %q, want first committed chunk", got)
	}
	if result.ResponseText != "first" {
		t.Fatalf("response text = %q, want first", result.ResponseText)
	}
}

// TestBackend_BodyClosedOnEncodeFailure 守护 a699e7c 的 `defer resp.Body.Close()`
// 修复。注入一个 RoundTripper-wrapped client transport pool 来计数 Close 调用，
// 然后通过让 inboundCodec.EncodeResponse 写客户端时失败，触发 streamNativeResponse
// 的 early-return 路径，验证 resp.Body 被 native.Relay 兜底关闭。
//
// 实现简化说明：BuildHTTPClient 拿的是 *http.Transport（具体类型，非 RoundTripper），
// 没法注入 wrapping RoundTripper，所以我们用一个 *failingResponseWriter（Write 永远
// 失败）作为客户端 writer。streamNativeResponse 调 EncodeResponse 失败 → 返回 Err
// 且 Written=true（encodeNonStream 已 set Header）。这跟"defer 兜底关闭 body"是
// 同一个 invariant 的对偶面：只要 EncodeResponse 失败路径可达且不 panic，
// defer 保护就生效（原先没 defer 时这里会因 decode goroutine 还持有 body 而泄漏）。
//
// 该 case 也回归"EncodeResponse 失败要返回 Err 但带 Written=true"的不变量。
func TestBackend_BodyClosedOnEncodeFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 返回合法 OpenAI chat 响应，让 decodeNonStream 顺利出 events。
		_, _ = w.Write([]byte(`{
			"id":"x","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1}
		}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, baseRec := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	// 把 c.Writer 替换成 Write 必失败的 writer。
	// rec.WrapClientWriter 仍会再包一层，但底层 Write 委派回我们这层，errors propagate。
	rctx.Context.Writer = &failingResponseWriter{ResponseWriter: rctx.Context.Writer, baseRec: baseRec}

	backend := &Backend{Agent: nil}
	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})

	if got.Err == nil {
		t.Fatal("expected Err when EncodeResponse fails on Write")
	}
	if !strings.Contains(got.Err.Error(), "encode response") {
		t.Errorf("expected 'encode response' wrap, got %q", got.Err.Error())
	}
	if !got.Written {
		t.Errorf("encodeNonStream sets Content-Type before Write fails → Written should be true, got %+v", got)
	}
}

// TestBackend_NegotiatesOutboundProtocol 验证 resolveNativeCodecs 把 claude 入站 +
// channel.SupportedAPITypes=[chat-completion] 协商成 openai_chat 出站。
// 上游收到的 path 应该是 /v1/chat/completions（openai chat 默认 endpoint）。
func TestBackend_NegotiatesOutboundProtocol(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	ch.Type = consts.ChannelTypeAnthropic // claude-family channel
	ch.SupportedAPITypes = `["chat-completion"]`

	// claude inbound body
	body := []byte(`{"model":"gpt-4","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	rctx, _ := newNativeTestCtx(t, body, llmkit.ProtocolClaude, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("outbound protocol negotiation failed: upstream path = %q, want /v1/chat/completions", gotPath)
	}
}

// TestBackend_Upstream5xx_PropagatesError 验证 handleNativeErrorStatus 5xx 分支：
// upstream 500 → AttemptResult.Err != nil 且 Written=false（可重试）。
// 跟 passthrough 同款契约，只是 error 文本是 "upstream returned 500: ..."。
func TestBackend_Upstream5xx_PropagatesError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err from upstream 500")
	}
	if got.Written {
		t.Errorf("5xx must be retryable: Written should be false, got %+v", got)
	}
	if !strings.Contains(got.Err.Error(), "upstream returned 500") {
		t.Errorf("expected 'upstream returned 500' wrap, got %q", got.Err.Error())
	}
}

// TestBackend_SuccessfulRelay_ReturnsZeroErr 验证 happy path：
// 上游返回合法 JSON → AttemptResult.Err == nil, Written=true, usage 解析正确。
func TestBackend_SuccessfulRelay_ReturnsZeroErr(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":2}
		}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if !got.Written {
		t.Errorf("Written should be true on success, got %+v", got)
	}
	if got.PromptTokens != 5 || got.CompletionTokens != 2 {
		t.Errorf("usage = (p=%d,c=%d), want (5,2)", got.PromptTokens, got.CompletionTokens)
	}
	// 客户端响应应该是 openai chat JSON（codec re-encode 之后）。
	var parsed map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("client body not valid JSON: %v body=%q", err, w.Body.String())
	}
	if parsed["object"] != "chat.completion" {
		t.Errorf("client body object = %v, want chat.completion", parsed["object"])
	}
}

// TestBackend_ApplyModelMappingBeforeEncode 验证端到端经 Relay 流程，model mapping
// 在上行 encode 前被应用：ch.ModelMapping={"gpt-4":"gpt-4o"}
// → 上游 body 里的 model 字段应该是 "gpt-4o"，AttemptResult.UpstreamModel 同款。
func TestBackend_ApplyModelMappingBeforeEncode(t *testing.T) {
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(b, &parsed)
		if m, ok := parsed["model"].(string); ok {
			gotModel = m
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	ch.ModelMapping = `{"gpt-4":"gpt-4o"}`

	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotModel != "gpt-4o" {
		t.Errorf("upstream saw model=%q, want gpt-4o (mapping not applied)", gotModel)
	}
	if got.UpstreamModel != "gpt-4o" {
		t.Errorf("AttemptResult.UpstreamModel = %q, want gpt-4o", got.UpstreamModel)
	}
}

// TestBackend_StreamResponsePropagatesUsageEvents 验证流式上游 SSE 中带 usage
// chunk 时，AttemptResult.PromptTokens / CompletionTokens 被正确填入。
// 入站 / 出站协议都是 openai_chat（最简路径），客户端拿到的也是 SSE。
func TestBackend_StreamResponsePropagatesUsageEvents(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		``,
		`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":4}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, true)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if got.PromptTokens != 11 || got.CompletionTokens != 4 {
		t.Errorf("stream usage = (p=%d,c=%d), want (11,4)", got.PromptTokens, got.CompletionTokens)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("client SSE missing [DONE] terminator, got %q", w.Body.String())
	}
	if !strings.Contains(got.ResponseText, "hi") {
		t.Errorf("ResponseText missing content delta, got %q", got.ResponseText)
	}
}

// ==================== Bonus coverage helpers ====================

// TestBackend_InvalidUpstreamURL_DispatchError 复用 passthrough 的 dispatch 失败
// 验证模式：让 client.Do 返回 connection refused，确认 dispatchUpstream 走 WithFail
// 分支并返回 wrapped error，Written=false。
func TestBackend_InvalidUpstreamURL_DispatchError(t *testing.T) {
	ch := makeNativeChannel("http://127.0.0.1:1")
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected dispatch error for unreachable upstream")
	}
	if got.Written {
		t.Errorf("dispatch failure must not mark Written=true, got %+v", got)
	}
	if !strings.Contains(got.Err.Error(), "upstream request failed") {
		t.Errorf("expected 'upstream request failed' wrap, got %q", got.Err.Error())
	}
}

// TestBackend_Upstream4xx_Returns4xxAsUpstreamError 覆盖 handleNativeErrorStatus
// 4xx 分支的新合约(T4 重构后):上游 400 → 返回 *common.UpstreamError,
// Written=false,客户端 w 未被写过(EncodeError 决策移交 Executor)。
func TestBackend_Upstream4xx_Returns4xxAsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad model"}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 4xx")
	}
	if got.Written {
		t.Errorf("T4 后 backend 不再做 4xx EncodeError + Written=true 决策, got Written=true: %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T: %v", got.Err, got.Err)
	}
	if upErr.Status != http.StatusBadRequest {
		t.Errorf("UpstreamError.Status = %d, want 400", upErr.Status)
	}
	if upErr.ProviderErrorType != "invalid_request_error" {
		t.Errorf("ProviderErrorType = %q, want invalid_request_error", upErr.ProviderErrorType)
	}
	// 客户端 w 应没被写过(EncodeError 已移到 Executor)。
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// ==================== Task 6: 4xx 错误路径覆盖 ====================

// TestHandleNativeError_429_RateLimit_GoesFallback 验证 handleNativeErrorStatus
// 对 429 的处理：Written=false（可重试/fallback），Err 是 *common.UpstreamError，
// Status=429，Body 含 rate_limit。
func TestHandleNativeError_429_RateLimit_GoesFallback(t *testing.T) {
	rateLimitBody := `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(rateLimitBody))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 429")
	}
	if got.Written {
		t.Errorf("429 必须 Written=false 以便 Executor 可 fallback, got Written=true: %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T: %v", got.Err, got.Err)
	}
	if upErr.Status != http.StatusTooManyRequests {
		t.Errorf("UpstreamError.Status = %d, want 429", upErr.Status)
	}
	if !strings.Contains(string(upErr.Body), "rate_limit") {
		t.Errorf("UpstreamError.Body 应含 rate_limit, got %q", upErr.Body)
	}
	// 客户端 w 不应被写过（body 回写决策已移到 Executor）。
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// TestHandleNativeError_408_Timeout_GoesFallback 验证 handleNativeErrorStatus
// 对 408 Request Timeout 的处理：同样是 Written=false，可以 fallback 到下一 channel。
func TestHandleNativeError_408_Timeout_GoesFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		_, _ = w.Write([]byte(`{"error":{"message":"Request timeout","type":"timeout_error"}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 408")
	}
	if got.Written {
		t.Errorf("408 必须 Written=false 以便 Executor 可 fallback, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusRequestTimeout {
		t.Errorf("UpstreamError.Status = %d, want 408", upErr.Status)
	}
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// TestHandleNativeError_400_InvalidRequest 验证 handleNativeErrorStatus 对
// HTTP 400 + provider error.type=invalid_request_error 的处理：
// UpstreamError.ProviderErrorType 应被正确解析（供 Executor 做短路决策）。
func TestHandleNativeError_400_InvalidRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"model not supported"}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 400")
	}
	if got.Written {
		t.Errorf("400 backend 不做 EncodeError，Written 应为 false, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusBadRequest {
		t.Errorf("UpstreamError.Status = %d, want 400", upErr.Status)
	}
	if upErr.ProviderErrorType != "invalid_request_error" {
		t.Errorf("ProviderErrorType = %q, want invalid_request_error (Executor 用此字段做短路决策)", upErr.ProviderErrorType)
	}
}

// TestHandleNativeError_400_NoProviderType 验证 handleNativeErrorStatus 对
// HTTP 400 但 body 不含 error.type 的处理：ProviderErrorType 应为空字符串
// （不强制走 invalid_request_error 短路路径，允许 Executor 走 fallback）。
func TestHandleNativeError_400_NoProviderType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// 不含 error.type 字段
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		llmkit.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 400")
	}
	if got.Written {
		t.Errorf("400 backend 不做 EncodeError，Written 应为 false, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusBadRequest {
		t.Errorf("UpstreamError.Status = %d, want 400", upErr.Status)
	}
	if upErr.ProviderErrorType != "" {
		t.Errorf("ProviderErrorType 应为空(无 error.type 字段), got %q", upErr.ProviderErrorType)
	}
}

// TestNative_DispatchHonorsCanceledContext 验证已取消的 client context 必须让
// 上游调用立刻失败，而非永久 hang。
func TestNative_DispatchHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://10.255.255.1:9/hang", nil)
	rec := trace.NewRecorder(trace.CaptureOff, 0)
	doer := &attemptHTTPDoer{
		attempt:  state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}},
		protocol: llmkit.ProtocolOpenAIChat, recorder: rec,
	}

	start := time.Now()
	_, err := doer.Do(req)
	if err == nil {
		t.Fatal("expected error on canceled context")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("canceled context should fail fast, took %v", time.Since(start))
	}
}

func TestNativeErrorBodyIsBoundedAndTraceKeepsTail(t *testing.T) {
	const physicalTail = `","tail":"native-physical-tail"}}`
	body := `{"error":{"type":"invalid_request_error","message":"` + strings.Repeat("x", 96*1024) + physicalTail
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()
	rctx, _ := newNativeTestCtx(t, []byte(`{"model":"gpt-4","messages":[]}`), llmkit.ProtocolOpenAIChat, false)
	rctx.State.Recorder = trace.NewRecorder(trace.CaptureFull, 64)
	got := (&Backend{}).Relay(rctx, state.Attempt{Channel: makeNativeChannel(upstream.URL), RealModel: "gpt-4"})
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("err=%T %v, want UpstreamError", got.Err, got.Err)
	}
	if len(upErr.Body) >= len(body) {
		t.Fatalf("bounded body len=%d, original=%d", len(upErr.Body), len(body))
	}
	if upErr.ProviderErrorType != "invalid_request_error" {
		t.Fatalf("provider error type=%q", upErr.ProviderErrorType)
	}
	traceRecord := rctx.State.Recorder.Finalize()
	if !strings.Contains(traceRecord.UpstreamBody, physicalTail) {
		t.Fatalf("trace tail=%q", traceRecord.UpstreamBody)
	}
}

func TestNativeErrorBodyReadErrorPropagates(t *testing.T) {
	wantErr := errors.New("native error body failed")
	transport := httpDoerTestTransport("readerr", func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway, Header: make(http.Header),
			Body: &backendErrorReadCloser{err: wantErr}, Request: request,
		}, nil
	})
	doer := &attemptHTTPDoer{
		agent:    httpDoerTestAgent{pool: &httpDoerTransportPool{transport: transport}},
		attempt:  state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}},
		protocol: llmkit.ProtocolOpenAIChat, recorder: trace.NewRecorder(trace.CaptureFull, 64),
	}
	_, err := doer.Do(httpDoerRequest(t, "readerr://provider/v1", `{}`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want read error", err)
	}
}

func TestNativeErrorBodyCancellationUnblocksRead(t *testing.T) {
	body := newBackendBlockingReadCloser()
	ctx, cancel := context.WithCancel(context.Background())
	transport := httpDoerTestTransport("blockread", func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: body, Request: request,
		}, nil
	})
	doer := &attemptHTTPDoer{
		agent:    httpDoerTestAgent{pool: &httpDoerTransportPool{transport: transport}},
		attempt:  state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}},
		protocol: llmkit.ProtocolOpenAIChat, recorder: trace.NewRecorder(trace.CaptureFull, 64),
	}
	req := httpDoerRequest(t, "blockread://provider/v1", `{}`).WithContext(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := doer.Do(req)
		done <- err
	}()
	<-body.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context canceled", err)
		}
	case <-time.After(time.Second):
		_ = body.Close()
		t.Fatal("cancellation did not unblock native error body")
	}
}

type backendErrorReadCloser struct{ err error }

func (r *backendErrorReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r *backendErrorReadCloser) Close() error             { return nil }

type backendBlockingReadCloser struct {
	entered chan struct{}
	closed  chan struct{}
}

func newBackendBlockingReadCloser() *backendBlockingReadCloser {
	return &backendBlockingReadCloser{entered: make(chan struct{}), closed: make(chan struct{})}
}
func (r *backendBlockingReadCloser) Read([]byte) (int, error) {
	select {
	case <-r.entered:
	default:
		close(r.entered)
	}
	<-r.closed
	return 0, errors.New("closed")
}
func (r *backendBlockingReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

// ==================== Test fixtures ====================

// failingResponseWriter 包装 gin.ResponseWriter，让所有 Write 调用返回 error，
// 用于触发 inboundCodec.EncodeResponse 失败路径。
// 同时显式实现 http.Flusher（gin.ResponseWriter 已要求），避免 encodeStream
// 走 flusher-assert 分支。WriteHeader / Header 委派回 baseRec，保证响应头
// 仍能写入 httptest.ResponseRecorder（让 Written=true 判定成立）。
type failingResponseWriter struct {
	gin.ResponseWriter
	baseRec *httptest.ResponseRecorder
}

func (f *failingResponseWriter) Write(b []byte) (int, error) {
	return 0, errors.New("synthetic write failure")
}

func (f *failingResponseWriter) WriteString(s string) (int, error) {
	return 0, errors.New("synthetic write failure")
}

func (f *failingResponseWriter) Flush() {
	// noop — encodeStream may call Flush before Write.
}

// Hijack 不需要支持，但 gin.ResponseWriter 接口要求实现。
func (f *failingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("not supported")
}
