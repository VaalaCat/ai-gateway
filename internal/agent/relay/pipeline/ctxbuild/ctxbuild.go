// Package ctxbuild 是 relay pipeline 的 Phase 1：把 *gin.Context 装配成
// state.RelayContext.Input 的请求级 immutable 输入。
//
// 单一职责：只读 c → 写 rctx.Input，并把 4 个早期失败映射到 sentinel error。
// HTTP 响应、UsageLog 由主流程兜底，这里不写。
package ctxbuild

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// computeRequestID 取请求 ID：优先用 X-Request-Id 头，缺省回退到时间戳。
func computeRequestID(c *gin.Context) string {
	if id := c.GetHeader(consts.HeaderXRequestID); id != "" {
		return id
	}
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

// Build 把 *gin.Context 装配成 RelayContext.Input 的 8 个字段。
// 中间任何失败 return error；不写 UsageLog、不写 HTTP 响应——延后到 Handler 主流程兜底。
func Build(rctx *state.RelayContext) error {
	rctx.Input.StartTime = time.Now()
	rctx.Input.RequestID = computeRequestID(rctx.Context)

	rec := rctx.State.Recorder
	rec.WithStage(trace.StageInboundDecode)
	rec.WithInbound(rctx.Context.Request, nil) // 预捕 path/headers，body 后补

	if v, ok := rctx.Context.Get(consts.CtxKeyUserInfo); ok {
		if ui, ok := v.(*app.UserInfo); ok {
			rctx.Input.UserInfo = ui
		}
	}
	rctx.Input.InboundProto = codec.PathToProtocol(rctx.Context.Request.URL.Path)

	body, err := io.ReadAll(rctx.Context.Request.Body)
	if err != nil {
		wrapped := fmt.Errorf("%w: %v", state.ErrReadBody, err)
		rec.WithFail(trace.StageInboundDecode, wrapped)
		return wrapped
	}
	rctx.Input.Body = body
	rec.WithInbound(rctx.Context.Request, body)
	rctx.Context.Request.Body = io.NopCloser(bytes.NewReader(body))

	var req struct {
		Model  string `json:"model"`
		Stream *bool  `json:"stream,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		wrapped := fmt.Errorf("%w: %v", state.ErrInvalidBody, err)
		rec.WithFail(trace.StageInboundDecode, wrapped)
		return wrapped
	}
	if req.Model == "" {
		rec.WithFail(trace.StageInboundDecode, state.ErrModelRequired)
		return state.ErrModelRequired
	}
	rctx.Input.Model = req.Model
	rctx.Input.IsStream = req.Stream != nil && *req.Stream

	if s := rctx.Context.GetHeader(consts.HeaderXChannelID); s != "" {
		id, perr := strconv.ParseUint(s, 10, 64)
		if perr != nil {
			rec.WithFail(trace.StageInternal, state.ErrInvalidForcedChannelID)
			return state.ErrInvalidForcedChannelID
		}
		rctx.Input.ForcedChannelID = uint(id)
	}
	return nil
}
