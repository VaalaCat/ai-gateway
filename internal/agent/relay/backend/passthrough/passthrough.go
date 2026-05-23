package passthrough

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Backend 走 passthrough 路径，仅替换 model / Authorization / base URL，
// 不做 codec 转换。只持有 app.AgentApplication 拿 logger / transport pool。
// 外部访问为 passthrough.Backend；Agent 字段导出方便 backend.Dispatcher 装配。
type Backend struct {
	Agent app.AgentApplication
}

// Relay 把原来 (*Handler).relayPassthrough 的整段流程内化到 backend 里。
// 步骤：替换 model + role mapping → 构造上行 req → 复制 header + 设置 auth →
// 应用 override → 上行 HTTP → 把整段响应原样回写 → 从 captured body 抽 usage。
//
// 不做 token 调和（FinalizeTokenCounts 在 Dispatcher 层统一处理）。
func (b *Backend) Relay(rctx *state.RelayContext, a state.Attempt) state.AttemptResult {
	c := rctx.Context
	ch := a.Channel
	bodyBytes := rctx.Input.Body
	modelName := a.RealModel
	isStream := rctx.Input.IsStream
	startTime := rctx.Input.StartTime
	inboundProto := rctx.Input.InboundProto
	rec := rctx.State.Recorder

	logger := b.logger()

	rec.WithStage(trace.StageUpstreamDispatch).WithPassthrough()

	upstreamModel := state.ApplyModelMapping(ch, modelName)

	newBody, err := buildPassthroughBody(bodyBytes, ch, modelName, upstreamModel)
	if err != nil {
		return state.AttemptResult{Err: err}
	}

	upstreamReq, err := buildPassthroughRequest(c.Request, ch, inboundProto, newBody)
	if err != nil {
		return state.AttemptResult{Err: err}
	}

	newBody = applyPassthroughOverrides(upstreamReq, newBody, ch, logger)

	rec.WithOutbound(upstreamReq, newBody, ch)

	resp, err := b.dispatchUpstream(upstreamReq, ch, rec)
	if err != nil {
		return state.AttemptResult{Err: err}
	}

	if result, handled := handlePassthroughErrorStatus(rec, resp, c.Writer, upstreamModel); handled {
		return result
	}

	firstResponseMs := streamPassthroughResponse(c, rec, resp, startTime)

	promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens, responseText := extractPassthroughUsage(rec.UpstreamBodyBytes(), isStream)

	return state.AttemptResult{
		Written:          true,
		UpstreamModel:    upstreamModel,
		FirstResponseMs:  firstResponseMs,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheWriteTokens: cacheWriteTokens,
		ResponseText:     responseText,
	}
}

// dispatchUpstream 跑 HTTP 请求；失败时通过 Recorder.WithFail 打 trace + 返回 wrapped error。
// 与原 inline 写法一致，error 文案保留 "passthrough upstream failed"。
func (b *Backend) dispatchUpstream(req *http.Request, ch *models.Channel, rec *trace.Recorder) (*http.Response, error) {
	client := upstream.BuildHTTPClient(b.transportPool(), ch)
	resp, err := client.Do(req)
	if err != nil {
		rec.WithFail(trace.StageUpstreamDispatch, err)
		return nil, fmt.Errorf("passthrough upstream failed: %w", err)
	}
	return resp, nil
}

// extractPassthroughUsage 从 Recorder 抓取的上行 body 抽 usage + responseText。
//
// 归一化规则：
//   - cacheReadTokens > 0 时，OpenAI 格式 prompt_tokens 已经包含 cached tokens，需要减去。
//
// 返回顺序对应 state.AttemptResult 的对应字段，保持原 Relay 写法。
func extractPassthroughUsage(body []byte, isStream bool) (promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int, responseText string) {
	promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens = upstream.ExtractUsageFromPassthroughBody(body, isStream)

	// Normalize: if cacheReadTokens were extracted from prompt_tokens_details
	// (OpenAI format), prompt_tokens includes cached tokens and we must subtract.
	if cacheReadTokens > 0 && promptTokens >= cacheReadTokens {
		promptTokens -= cacheReadTokens
	}

	// Extract response text from captured body for token estimation
	responseText = upstream.ExtractTextFromPassthroughBody(body, isStream)
	return promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens, responseText
}

// streamPassthroughResponse 把 2xx 上行响应原样写回客户端，
// 同时通过 Recorder.WrapUpstreamBody / WrapClientWriter 捕获 body 给 trace / usage 抽取。
// 返回首字节到达的耗时（毫秒）。调用方仍然负责把这个值放进 state.AttemptResult.FirstResponseMs。
//
// 副作用：
//   - 修改 resp.Body（wrap 成 TeeReader），并 defer 关闭它。
//   - 修改 c.Writer（wrap 成 Recorder-tracked writer）。
//   - 写出 response header + status code，触发 client 端连接的 commit。
func streamPassthroughResponse(c *gin.Context, rec *trace.Recorder, resp *http.Response, startTime time.Time) int {
	rec.WithUpstreamStatus(resp)
	resp.Body = rec.WrapUpstreamBody(resp)
	defer resp.Body.Close()

	// Copy response headers, excluding encoding-related headers since
	// Go's Transport decompresses the body transparently.
	copyRespHeaders(c.Writer.Header(), resp.Header)
	c.Writer.WriteHeader(resp.StatusCode)

	c.Writer = rec.WrapClientWriter(c.Writer)

	flusher, canFlush := c.Writer.(http.Flusher)
	buf := make([]byte, 32*1024)
	firstByte := true
	var firstResponseMs int
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if firstByte {
				firstResponseMs = int(time.Since(startTime).Milliseconds())
				firstByte = false
			}
			c.Writer.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
	return firstResponseMs
}

// handlePassthroughErrorStatus 处理非 2xx/3xx 上行响应。
//
// 本函数职责仅:读 body / 记 trace / 包装成 *common.UpstreamError 返回。
// 是否重试 / fallback / 立即返回的决策由 Executor 统一负责(参见
// pipeline/exec/exec.go Run() 主循环)。
//
// 注意:本函数不再把 body 原样写回客户端,因为是否写回取决于 Executor 走例外路径
// (plan 全部 attempt 耗尽 + 最后一次失败时才写),由 Executor 在终止 attempt 链时统一处理。
//
// 过渡期说明(T5 尚未落地):4xx 错误当前不写回客户端,会经历全部 attempt
// 后由 Executor 统一终止。T5 落地后 Executor 将对 invalid_request_error 做
// 立即短路返回。
func handlePassthroughErrorStatus(rec *trace.Recorder, resp *http.Response, _w gin.ResponseWriter, upstreamModel string) (state.AttemptResult, bool) {
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return state.AttemptResult{}, false
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	upErr := &common.UpstreamError{
		Status:            resp.StatusCode,
		Body:              body,
		ProviderErrorType: common.ParseProviderErrorType(body),
		Header:            resp.Header.Clone(),
	}
	rec.WithUpstreamStatus(resp)
	rec.SetUpstreamBody(body)
	rec.WithFail(trace.StageUpstreamStatus, upErr)
	return state.AttemptResult{
		UpstreamModel: upstreamModel,
		Err:           upErr,
		// Written 留默认 false;客户端写回由 Executor 在 plan 结束时统一处理。
	}, true
}

// applyPassthroughOverrides 把 channel 的 ParamOverride / HeaderOverride 应用到上行请求上。
// JSON unmarshal 失败时静默 fallback 并打 Debug 日志（与 main:native.go:552-558 的静默
// 吞错行为对齐，避免坏配置导致生产 Warn 噪音）；ApplyOverrides 本身返回 err 仍是 Warn 级，
// 因为它代表运行时合并失败而非配置层面的坏数据。
// 返回最终的 body（成功合并 / 解析失败回落到原 body 都可能）。
func applyPassthroughOverrides(upstreamReq *http.Request, newBody []byte, ch *models.Channel, logger *zap.Logger) []byte {
	var paramOverride, headerOverride map[string]any
	if ch.ParamOverride != "" {
		if err := json.Unmarshal([]byte(ch.ParamOverride), &paramOverride); err != nil {
			if logger != nil {
				logger.Debug("passthrough: unmarshal param override failed, skipping",
					zap.Uint("channel_id", ch.ID),
					zap.Error(err))
			}
			paramOverride = nil
		}
	}
	if ch.HeaderOverride != "" {
		if err := json.Unmarshal([]byte(ch.HeaderOverride), &headerOverride); err != nil {
			if logger != nil {
				logger.Debug("passthrough: unmarshal header override failed, skipping",
					zap.Uint("channel_id", ch.ID),
					zap.Error(err))
			}
			headerOverride = nil
		}
	}
	if updatedBody, err := upstream.ApplyOverrides(upstreamReq, newBody, paramOverride, headerOverride); err != nil {
		if logger != nil {
			logger.Warn("apply passthrough overrides failed", zap.Error(err))
		}
	} else {
		newBody = updatedBody
	}
	return newBody
}

// buildPassthroughRequest 根据原始 *http.Request + channel 配置 + 新 body 构造上行 HTTP 请求。
// 处理：endpoint 解析 / URL 拼接 / header 拷贝 + 过滤 / Authorization 覆盖 / Organization /
// 移除 hop-by-hop 与 Accept-Encoding。
func buildPassthroughRequest(origReq *http.Request, ch *models.Channel, inboundProto codec.Protocol, newBody []byte) (*http.Request, error) {
	// Build upstream URL: prefer Endpoints config path, fallback to original request path
	endpointPath := codec.ResolveEndpointPath(ch.Endpoints, inboundProto)
	if endpointPath == "" {
		endpointPath = origReq.URL.Path
	}
	upstreamURL := strings.TrimRight(ch.GetBaseURL(), "/") + endpointPath

	upstreamReq, err := http.NewRequest(origReq.Method, upstreamURL, bytes.NewReader(newBody))
	if err != nil {
		return nil, fmt.Errorf("create passthrough request: %w", err)
	}

	// Copy original headers, then override auth
	for k, vals := range origReq.Header {
		for _, v := range vals {
			upstreamReq.Header.Add(k, v)
		}
	}
	upstream.ApplyHeaderFilter(upstreamReq.Header)
	upstreamReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+ch.Key)
	upstreamReq.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	upstreamReq.ContentLength = int64(len(newBody))
	if ch.Organization != "" {
		upstreamReq.Header.Set(consts.HeaderOpenAIOrg, ch.Organization)
	}
	// Remove hop-by-hop headers
	upstreamReq.Header.Del(consts.HeaderConnection)
	upstreamReq.Header.Del(consts.HeaderHost)
	// Remove Accept-Encoding so Go's Transport handles decompression transparently.
	// Without this, the upstream may return gzip/br-compressed bodies that we cannot
	// parse for usage extraction.
	upstreamReq.Header.Del("Accept-Encoding")
	return upstreamReq, nil
}

// buildPassthroughBody 在原始 body 上替换 model + 应用 role mapping，
// 返回重新 marshal 后的 body。失败返回 wrapped error，由调用方包成 state.AttemptResult。
func buildPassthroughBody(bodyBytes []byte, ch *models.Channel, modelName, upstreamModel string) ([]byte, error) {
	var bodyMap map[string]any
	if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
		return nil, fmt.Errorf("unmarshal request for passthrough: %w", err)
	}
	bodyMap["model"] = upstreamModel

	// Apply role mapping if configured
	if ch.RoleMapping != "" {
		if rm := transform.ParseRoleMapping(ch.RoleMapping); rm != nil {
			if mapping := rm.ResolveRoleMapping(modelName); mapping != nil {
				if messagesRaw, ok := bodyMap["messages"]; ok {
					if messages, ok := messagesRaw.([]any); ok {
						for i, msgRaw := range messages {
							if msg, ok := msgRaw.(map[string]any); ok {
								if roleRaw, ok := msg["role"]; ok {
									if role, ok := roleRaw.(string); ok {
										if targetRole, ok := mapping[codec.Role(role)]; ok {
											msg["role"] = string(targetRole)
											messages[i] = msg
										}
									}
								}
							}
						}
						bodyMap["messages"] = messages
					}
				}
			}
		}
	}

	newBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("marshal request for passthrough: %w", err)
	}
	return newBody, nil
}

// copyRespHeaders 把 src 里除 Content-Encoding / Content-Length 之外的 header 全部 Add 到 dst。
// 这两个 header 在 Go Transport 透明解压后已不再代表实际响应体形态，转发会让客户端解码失败。
func copyRespHeaders(dst, src http.Header) {
	for k, vals := range src {
		if k == "Content-Encoding" || k == "Content-Length" {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

// logger 是 b.Agent.GetLogger() 的 nil-guarded 包装。
func (b *Backend) logger() *zap.Logger {
	if b.Agent == nil {
		return nil
	}
	return b.Agent.GetLogger()
}

// transportPool 是 b.Agent.GetTransportPool() 的 nil-guarded 包装。
func (b *Backend) transportPool() app.TransportPool {
	if b.Agent == nil {
		return nil
	}
	return b.Agent.GetTransportPool()
}
