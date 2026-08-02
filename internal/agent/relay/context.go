package relay

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ginutil"
)

// NewContext constructs the request-scoped relay state shared by the ordinary
// relay pipeline and the bound-attempt proxy pipeline.
func NewContext(c *gin.Context, agent app.AgentApplication) *state.RelayContext {
	startedAt, ok := ginutil.FindRequestStart(c)
	if !ok {
		startedAt = time.Now()
	}
	maxBody := 0
	if agent != nil {
		if cache := agent.GetCache(); cache != nil {
			maxBody = cache.TraceMaxBodySize()
		}
	}
	return &state.RelayContext{
		Context: c,
		Agent:   agent,
		Input: state.RelayInput{
			StartTime: startedAt,
		},
		State: &state.RelayState{
			Recorder: trace.NewRecorderAt(trace.CaptureModeFromContext(c), maxBody, startedAt),
		},
	}
}

// CloseContext releases request readers and captured replay resources. Both
// operations are idempotent, so callers may safely defer it on every path.
func CloseContext(rctx *state.RelayContext) {
	if rctx == nil {
		return
	}
	if rctx.Context != nil && rctx.Context.Request != nil && rctx.Context.Request.Body != nil {
		_ = rctx.Context.Request.Body.Close()
		rctx.Context.Request.Body = http.NoBody
	}
	if rctx.Resources != nil {
		_ = rctx.Resources.Close()
	}
}
