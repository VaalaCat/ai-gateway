package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

// scriptEngineHolder 是 AgentCache 暴露脚本引擎的最小契约（*cache.Store 实现）。
type scriptEngineHolder interface {
	ScriptEngine() *script.Engine
}

func (h *Handler) scriptEngine() *script.Engine {
	if h.Agent == nil {
		return nil
	}
	c := h.Agent.GetCache()
	if c == nil {
		return nil
	}
	if holder, ok := c.(scriptEngineHolder); ok {
		return holder.ScriptEngine()
	}
	return nil
}

// applyRequestScripts 在路由前执行 onRequest 钩子。
// 返回 true 表示请求已被脚本拒绝并写回响应，调用方应直接返回。
func (h *Handler) applyRequestScripts(rctx *state.RelayContext) (bool, error) {
	eng := h.scriptEngine()
	if eng == nil {
		return false, nil
	}
	res := eng.Run(script.HookInput{
		Hook:      script.HookRequest,
		ChannelID: 0, // 入站未路由：只命中全局作用域脚本
		Model:     rctx.Input.Model,
		User:      scriptUserMap(rctx.Input.UserInfo),
		Headers:   scriptHeaderMap(rctx.Context.Request.Header),
		Body:      rctx.Input.Body,
	})
	if res.Rejected {
		rctx.Context.AbortWithStatusJSON(res.Status, gin.H{
			"error": gin.H{"message": res.Message, "type": "script_rejected"},
		})
		return true, nil
	}
	if res.Changed {
		if rctx.Resources == nil || h.Agent == nil || h.Agent.GetBodyStore() == nil {
			return false, errors.New("request body resources unavailable")
		}
		body, reader, err := rctx.Resources.CaptureAndReplaceWithReader(
			rctx.Context.Request.Context(),
			h.Agent.GetBodyStore(),
			bytes.NewReader(res.Body),
			rctx.Input.BodyLimits,
		)
		if err != nil {
			return false, err
		}
		previous := rctx.Context.Request.Body
		rctx.Context.Request.Body = reader
		rctx.Context.Request.GetBody = body.Open
		rctx.Context.Request.ContentLength = body.Size()
		if previous != nil {
			_ = previous.Close()
		}
		rctx.Input.Body = res.Body
		reparseModelStream(rctx, res.Body)
	}
	return false, nil
}

// reparseModelStream 在 onRequest 改写后重新提取 model/stream（脚本可能改了它们）。
// 缺失的 key 不覆盖原值。
func reparseModelStream(rctx *state.RelayContext, body []byte) {
	var meta struct {
		Model  *string `json:"model"`
		Stream *bool   `json:"stream"`
	}
	if json.Unmarshal(body, &meta) != nil {
		return
	}
	if meta.Model != nil && *meta.Model != "" {
		rctx.Input.Model = *meta.Model
	}
	if meta.Stream != nil {
		rctx.Input.IsStream = *meta.Stream
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
