// Package ctxbuild 是 relay pipeline 的 Phase 1：把 *gin.Context 装配成
// state.RelayContext.Input 的请求级 immutable 输入。
//
// 单一职责：只读 c → 写 rctx.Input，并把 4 个早期失败映射到 sentinel error。
// HTTP 响应、UsageLog 由主流程兜底，这里不写。
package ctxbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// computeRequestID 取请求 ID：优先用 X-Request-Id 头，缺省回退到时间戳。
func computeRequestID(c *gin.Context) string {
	id := agentproxy.CanonicalRequestID(c.GetHeader(consts.HeaderXRequestID))
	c.Request.Header.Set(consts.HeaderXRequestID, id)
	return id
}

// Build 把 *gin.Context 装配成 RelayContext.Input 的 8 个字段。
// 中间任何失败 return error；不写 UsageLog、不写 HTTP 响应——延后到 Handler 主流程兜底。
func Build(rctx *state.RelayContext) error {
	rctx.Input.StartTime = time.Now()
	rctx.Input.RequestID = computeRequestID(rctx.Context)
	if err := buildRoutingInput(rctx); err != nil {
		return err
	}

	rec := rctx.State.Recorder
	rec.WithStage(trace.StageInboundDecode)
	rec.WithInbound(rctx.Context.Request, nil) // 预捕 path/headers，body 后补

	if v, ok := rctx.Context.Get(consts.CtxKeyUserInfo); ok {
		if ui, ok := v.(*app.UserInfo); ok {
			rctx.Input.UserInfo = ui
		}
	}
	rctx.Input.InboundProto = codec.PathToProtocol(rctx.Context.Request.URL.Path)
	if rctx.Context.Request.Method == http.MethodGet {
		return nil
	}

	limits := requestBodyLimits(rctx)
	rctx.Input.BodyLimits = limits
	resources := &state.RequestResources{}
	rctx.Resources = resources
	original := rctx.Context.Request.Body
	if original == nil {
		original = http.NoBody
	}
	ownedOriginal := &onceReadCloser{ReadCloser: original}
	err := resources.Replace(rctx.Context.Request.Context(), rctx.Agent.GetBodyStore(), ownedOriginal, limits)
	_ = ownedOriginal.Close()
	rctx.Context.Request.Body = http.NoBody
	if err != nil {
		rec.WithFail(trace.StageInboundDecode, err)
		return err
	}
	replay := resources.Body()
	requestReader, err := replay.Open()
	if err != nil {
		rec.WithFail(trace.StageInboundDecode, err)
		return err
	}
	rctx.Context.Request.Body = requestReader
	rctx.Context.Request.GetBody = replay.Open
	rctx.Context.Request.ContentLength = replay.Size()

	if isAudioMultipartPath(rctx.Context.Request.URL.Path) {
		model, err := parseMultipartModel(
			rctx.Context.Request.Context(),
			replay,
			rctx.Context.Request.Header.Get("Content-Type"),
			limits.HardLimit,
		)
		if err != nil {
			rec.WithFail(trace.StageInboundDecode, err)
			return err
		}
		rctx.Input.Model = model
		rctx.Input.IsStream = false
		rec.WithInbound(rctx.Context.Request, nil)
	} else {
		body, err := replay.Bytes(limits.HardLimit)
		if err != nil {
			rec.WithFail(trace.StageInboundDecode, err)
			return err
		}
		rctx.Input.Body = body
		rec.WithInbound(rctx.Context.Request, body)

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
	}

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

func buildRoutingInput(rctx *state.RelayContext) error {
	agentID := strings.Trim(rctx.Context.GetHeader(consts.HeaderXAgentID), " \t\r\n\v\f")
	agentTag := strings.Trim(rctx.Context.GetHeader(consts.HeaderXAgentTag), " \t\r\n\v\f")
	if agentID != "" && agentTag != "" {
		return state.ErrInvalidAgentSelector
	}
	rctx.Input.HardSelector = app.AgentSelector{AgentID: agentID, AgentTag: agentTag}
	return nil
}

type onceReadCloser struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (r *onceReadCloser) Close() error {
	r.once.Do(func() { r.err = r.ReadCloser.Close() })
	return r.err
}

const (
	defaultBodyMemoryThreshold int64 = 1 << 20
	defaultBodyHardLimit       int64 = 64 << 20
	maxMultipartParts                = 128
	maxMultipartFieldBytes     int64 = 64 << 10
	maxMultipartModelBytes     int64 = 4 << 10
)

func requestBodyLimits(rctx *state.RelayContext) app.BodyLimits {
	limits := app.BodyLimits{
		MemoryThreshold: defaultBodyMemoryThreshold,
		HardLimit:       defaultBodyHardLimit,
	}
	if rctx != nil && rctx.Agent != nil {
		if cache := rctx.Agent.GetCache(); cache != nil {
			snapshot := cache.Settings()
			limits.MemoryThreshold = snapshot.BodyMemoryThresholdBytes
			limits.HardLimit = snapshot.BodyHardLimitBytes
		}
	}
	if limits.HardLimit <= 0 {
		limits.HardLimit = defaultBodyHardLimit
	}
	if limits.MemoryThreshold <= 0 {
		limits.MemoryThreshold = defaultBodyMemoryThreshold
	}
	if limits.MemoryThreshold > limits.HardLimit {
		limits.MemoryThreshold = limits.HardLimit
	}
	return limits
}

func isAudioMultipartPath(path string) bool {
	return path == "/v1/audio/transcriptions" || path == "/v1/audio/translations"
}

func parseMultipartModel(ctx context.Context, body app.ReplayBody, contentType string, maxFileBytes int64) (model string, err error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return "", fmt.Errorf("%w: invalid multipart boundary", state.ErrInvalidBody)
	}
	r, err := body.Open()
	if err != nil {
		return "", err
	}
	defer func() {
		closeErr := classifyMultipartReadError(r.Close(), "malformed multipart body")
		if closeErr == nil {
			return
		}
		if err == nil {
			err = closeErr
			return
		}
		err = errors.Join(err, closeErr)
	}()
	mr := multipart.NewReader(r, params["boundary"])
	for parts := 1; ; parts++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		part, err := mr.NextPart()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", classifyMultipartReadError(err, "malformed multipart body")
		}
		if parts > maxMultipartParts {
			return "", fmt.Errorf("%w: too many multipart parts", state.ErrInvalidBody)
		}
		if part.FileName() != "" {
			err = drainWithContext(ctx, part, maxFileBytes)
		} else {
			limit := maxMultipartFieldBytes
			if part.FormName() == "model" {
				limit = maxMultipartModelBytes
			}
			var field []byte
			field, err = readSmallPart(ctx, part, limit)
			if err == nil && part.FormName() == "model" && model == "" {
				model = strings.TrimSpace(string(field))
			}
		}
		if err != nil {
			return "", err
		}
	}
	if model == "" {
		return "", state.ErrModelRequired
	}
	return model, nil
}

func classifyMultipartReadError(err error, malformed string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var bodyErr interface{ BodyErrorCode() string }
	if errors.As(err, &bodyErr) {
		switch bodyErr.BodyErrorCode() {
		case "body_too_large", "body_store_failed":
			return err
		}
	}
	return fmt.Errorf("%w: %s", state.ErrInvalidBody, malformed)
}

func readSmallPart(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	buf := make([]byte, 0, min(int(limit), 4<<10))
	chunk := make([]byte, 4<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(chunk)
		// behavior change: an observed I/O failure wins over size, and either result stops all further reads.
		if err != nil && err != io.EOF {
			return nil, classifyMultipartReadError(err, "malformed multipart field")
		}
		if n > 0 {
			if int64(n) > limit-total {
				return nil, fmt.Errorf("%w: multipart field too large", state.ErrInvalidBody)
			}
			buf = append(buf, chunk[:n]...)
			total += int64(n)
		}
		if err == io.EOF {
			return buf, nil
		}
	}
}

func drainWithContext(ctx context.Context, r io.Reader, limit int64) error {
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf)
		// behavior change: an observed I/O failure wins over size, and either result stops all further reads.
		if err != nil && err != io.EOF {
			return classifyMultipartReadError(err, "malformed multipart file")
		}
		if n > 0 {
			if int64(n) > limit-total {
				return fmt.Errorf("%w: multipart file too large", state.ErrInvalidBody)
			}
			total += int64(n)
		}
		if err == io.EOF {
			return nil
		}
	}
}
