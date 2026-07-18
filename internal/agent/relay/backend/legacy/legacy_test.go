package legacy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
)

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
		State:   &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
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
		State:   &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
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
