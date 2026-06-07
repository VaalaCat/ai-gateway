package limiter

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func testStreamRctx() (*state.RelayContext, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/", nil)
	return &state.RelayContext{Context: c, State: &state.RelayState{}}, rec
}

func TestStreamGuard_OpenAndKeepalive(t *testing.T) {
	rctx, rec := testStreamRctx()
	g := newStreamGuard(rctx, 10) // 10ms 间隔
	g.ensureOpen()
	require.True(t, rctx.State.StreamOpened)
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), ": keepalive")

	g.startKeepalive()
	time.Sleep(45 * time.Millisecond)
	g.stopKeepalive() // 必须能干净 join、不卡死
	require.GreaterOrEqual(t, strings.Count(rec.Body.String(), ": keepalive"), 2)
}

func TestWriteSSEError_OpenAIAndClaude(t *testing.T) {
	rctx, rec := testStreamRctx()
	WriteSSEError(rctx.Writer, "openai", "rate limited")
	require.Contains(t, rec.Body.String(), "rate_limit_error")
	require.Contains(t, rec.Body.String(), "[DONE]")

	rctx2, rec2 := testStreamRctx()
	WriteSSEError(rctx2.Writer, "anthropic", "rate limited")
	require.Contains(t, rec2.Body.String(), "event: error")
}
