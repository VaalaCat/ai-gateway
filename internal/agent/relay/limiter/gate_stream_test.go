package limiter

import (
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGate_Wait_Stream_OpensStream(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 5, QueueTimeMs: 120, Enabled: true},
	}}
	g := NewGate(res, NewMemStore())

	l1, _ := g.AcquireRequest(rctxWithUser(1, 0)) // 占满不放 → 触发等待→超时
	_ = l1

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/", nil)
	rctx := &state.RelayContext{
		Context: c,
		Input:   state.RelayInput{IsStream: true, UserInfo: &app.UserInfo{UserID: 1}},
		State:   &state.RelayState{},
	}

	_, err := g.AcquireRequest(rctx)
	require.ErrorIs(t, err, state.ErrRateLimited)
	require.True(t, rctx.State.StreamOpened, "stream 等待应开流")
	require.Contains(t, rec.Body.String(), ": keepalive")
}
