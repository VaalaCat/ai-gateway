package scripthook

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

var errScriptRejected = errors.New("request rejected by script hook")

type scriptEngineHolder interface {
	ScriptEngine() *script.Engine
}

func engineOf(a app.AgentApplication) *script.Engine {
	if a == nil {
		return nil
	}
	c := a.GetCache()
	if c == nil {
		return nil
	}
	if holder, ok := c.(scriptEngineHolder); ok {
		return holder.ScriptEngine()
	}
	return nil
}

// RunUpstreamScripts executes onUpstreamRequest just before a backend dispatches
// its prepared upstream request.
func RunUpstreamScripts(
	agent app.AgentApplication,
	c *gin.Context,
	rctx *state.RelayContext,
	ch *models.Channel,
	proto codec.Protocol,
	model string,
	upstreamReq *http.Request,
	outboundBody []byte,
) ([]byte, bool, state.AttemptResult) {
	eng := engineOf(agent)
	if eng == nil {
		return outboundBody, false, state.AttemptResult{}
	}
	res := eng.Run(script.HookInput{
		Hook:      script.HookUpstream,
		ChannelID: ch.ID,
		Model:     model,
		User:      scriptUserMap(rctx.Input.UserInfo),
		Headers:   scriptHeaderMap(c.Request.Header),
		Channel:   map[string]any{"id": ch.ID, "name": ch.Name},
		Protocol:  string(proto),
		Body:      outboundBody,
	})
	if res.Rejected {
		c.AbortWithStatusJSON(res.Status, gin.H{
			"error": gin.H{"message": res.Message, "type": "script_rejected"},
		})
		return outboundBody, true, state.AttemptResult{Written: true, Err: errScriptRejected}
	}
	applyHeaderOps(upstreamReq, res.HeaderOps)
	if res.Changed {
		b := res.Body
		upstreamReq.Body = io.NopCloser(bytes.NewReader(b))
		upstreamReq.ContentLength = int64(len(b))
		upstreamReq.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
		return b, false, state.AttemptResult{}
	}
	return outboundBody, false, state.AttemptResult{}
}

func applyHeaderOps(req *http.Request, ops []script.HeaderOp) {
	for _, op := range ops {
		if op.Remove {
			req.Header.Del(op.Name)
		} else {
			req.Header.Set(op.Name, op.Value)
		}
	}
}

func scriptUserMap(info *app.UserInfo) map[string]any {
	if info == nil {
		return nil
	}
	return map[string]any{"id": info.UserID, "group": info.GroupID}
}

func scriptHeaderMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		out[k] = strings.Join(vs, ", ")
	}
	return out
}
