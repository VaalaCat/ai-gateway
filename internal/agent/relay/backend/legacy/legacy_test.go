package legacy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/firstresponse"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/gin-gonic/gin"
)

func newLegacyResponseContext(
	t *testing.T,
	body []byte,
	stream bool,
) (*state.RelayContext, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(body),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	startedAt := time.Now().Add(-100 * time.Millisecond)
	tracker := firstresponse.NewTracker(startedAt)
	context.Writer = firstresponse.Wrap(context.Writer, tracker)
	return &state.RelayContext{
		Context: context,
		Input: state.RelayInput{
			Body: body, Model: "gpt-4", IsStream: stream,
			InboundProto: llmkit.ProtocolOpenAIChat, StartTime: startedAt,
		},
		State: &state.RelayState{
			Recorder:      trace.NewRecorderAt(trace.CaptureOff, 0, startedAt),
			FirstResponse: tracker,
		},
	}, response
}

func newLegacyChannel(baseURL string) *models.Channel {
	return &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: baseURL,
		},
		Key: "provider-key",
	}
}

func TestLegacyStreamFirstResponseOwnedByClientWriter(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		_, _ = io.WriteString(writer, ": keepalive\n\n")
		flusher.Flush()
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(
			writer,
			"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"+
				"data: [DONE]\n\n",
		)
	}))
	defer provider.Close()
	body := []byte(`{"model":"gpt-4","stream":true,"messages":[]}`)
	rctx, response := newLegacyResponseContext(t, body, true)

	result := (&Backend{}).Relay(rctx, state.Attempt{
		Channel: newLegacyChannel(provider.URL), RealModel: "gpt-4",
	})

	if result.Err != nil {
		t.Fatalf("Relay() error = %v", result.Err)
	}
	if result.FirstResponseMs != 0 {
		t.Fatalf("backend first response = %dms, want writer-owned zero outcome", result.FirstResponseMs)
	}
	if rctx.State.FirstResponse.Milliseconds() <= 0 {
		t.Fatal("client writer did not observe SSE response")
	}
	if responseBody := response.Body.String(); !strings.Contains(responseBody, `"role":"assistant"`) {
		t.Fatalf("client body = %q, want assistant role frame", responseBody)
	}
}

func TestLegacyNonStreamFirstResponseFallsBackToClientBody(t *testing.T) {
	const providerBody = `{"id":"chatcmpl-2","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, providerBody)
	}))
	defer provider.Close()
	body := []byte(`{"model":"gpt-4","messages":[]}`)
	rctx, response := newLegacyResponseContext(t, body, false)

	result := (&Backend{}).Relay(rctx, state.Attempt{
		Channel: newLegacyChannel(provider.URL), RealModel: "gpt-4",
	})

	if result.Err != nil {
		t.Fatalf("Relay() error = %v", result.Err)
	}
	if result.FirstResponseMs != 0 {
		t.Fatalf("backend first response = %dms, want writer-owned zero outcome", result.FirstResponseMs)
	}
	if rctx.State.FirstResponse.Milliseconds() <= 0 {
		t.Fatal("client writer did not observe JSON response")
	}
	if responseBody := response.Body.String(); !strings.Contains(responseBody, `"content":"ok"`) {
		t.Fatalf("client body = %q, want assistant content", responseBody)
	}
	if result.PromptTokens != 3 || result.CompletionTokens != 1 {
		t.Fatalf("usage = (%d,%d), want (3,1)", result.PromptTokens, result.CompletionTokens)
	}
}

func TestLegacyProviderRequestHonorsCanceledContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	rctx := &state.RelayContext{
		Context: c,
		Input:   state.RelayInput{Body: body, Model: "gpt-4", StartTime: time.Now()},
		State:   &state.RelayState{Recorder: trace.NewRecorder(trace.CaptureOff, 0)},
	}
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://10.255.255.1:9"},
		Key:         "provider-key",
	}

	start := time.Now()
	result := (&Backend{}).Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if result.Err == nil {
		t.Fatal("legacy provider request ignored canceled context")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("legacy provider cancellation took %v", elapsed)
	}
}

func TestLegacyProviderRequestCancelsInFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer provider.Close()
	defer close(release)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	rctx := &state.RelayContext{
		Context: c,
		Input:   state.RelayInput{Body: body, Model: "gpt-4", StartTime: time.Now()},
		State:   &state.RelayState{Recorder: trace.NewRecorder(trace.CaptureOff, 0)},
	}
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: provider.URL},
		Key:         "provider-key",
	}
	done := make(chan state.AttemptResult, 1)
	go func() {
		done <- (&Backend{}).Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("legacy provider request did not start")
	}
	cancel()
	select {
	case result := <-done:
		if result.Err == nil {
			t.Fatal("in-flight legacy provider cancellation returned nil")
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight legacy provider request ignored cancellation")
	}
}
