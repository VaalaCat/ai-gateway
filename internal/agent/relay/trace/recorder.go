package trace

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/legacy"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// Recorder 是 request-scoped 的 trace 数据 + 请求体 buffer 唯一持有者。
// 字段全 unexport，状态变更只能经下面的 With* / Wrap* / ResetAttempt / Finalize 方法。
type Recorder struct {
	enabled     bool
	maxBodySize int

	startedAt  time.Time
	stageBegin time.Time
	currStage  Stage
	timings    map[Stage]time.Duration

	inboundPath    string
	inboundHeaders http.Header
	inboundBody    []byte

	outboundPath    string
	outboundHeaders http.Header
	outboundBody    []byte

	upstreamStatus  int
	responseHeaders http.Header
	upstreamBody    *bytes.Buffer

	clientBody  *bytes.Buffer
	passthrough bool

	channelKey     string
	channelBaseURL string

	failStage Stage
	stageHook func(Stage) // 可选:每次 WithStage 时回调(供 in-flight 追踪)

	attempts []*TraceRecord // 每候选 SnapshotAttempt() 追加一条
}

// NewRecorder 创建一个 request-scoped Recorder。enabled 控制 Finalize 是否产出
// TraceRecord（即是否把 4 份 body 落到 UsageLogTrace 表）；buffer 始终累积，供
// usage extraction / SSE scan / reconcile 复用。
func NewRecorder(enabled bool, maxBodySize int) *Recorder {
	now := time.Now()
	return &Recorder{
		enabled:      enabled,
		maxBodySize:  maxBodySize,
		startedAt:    now,
		stageBegin:   now,
		currStage:    StageNone,
		timings:      make(map[Stage]time.Duration),
		upstreamBody: &bytes.Buffer{},
		clientBody:   &bytes.Buffer{},
		failStage:    StageNone,
	}
}

// WithInbound 记录 client → gateway 的请求 path / headers / body。
func (r *Recorder) WithInbound(req *http.Request, body []byte) *Recorder {
	if r == nil {
		return nil
	}
	if req != nil {
		r.inboundPath = req.URL.Path
		r.inboundHeaders = cloneHeader(req.Header)
	}
	r.inboundBody = append(r.inboundBody[:0], body...)
	return r
}

// WithOutbound 记录 gateway → upstream 请求信息，并捕获 channel 密钥以供
// Finalize 时统一脱敏。
func (r *Recorder) WithOutbound(req *http.Request, body []byte, ch *models.Channel) *Recorder {
	if r == nil {
		return nil
	}
	if req != nil {
		r.outboundPath = req.URL.Path
		r.outboundHeaders = cloneHeader(req.Header)
	}
	r.outboundBody = append(r.outboundBody[:0], body...)
	if ch != nil {
		r.channelKey = ch.Key
		r.channelBaseURL = ch.GetBaseURL()
	}
	return r
}

// WithUpstreamStatus 记录上游响应 status 与 headers。注意上游 body 由
// WrapUpstreamBody 通过 TeeReader 累积。
func (r *Recorder) WithUpstreamStatus(resp *http.Response) *Recorder {
	if r == nil || resp == nil {
		return r
	}
	r.upstreamStatus = resp.StatusCode
	r.responseHeaders = cloneHeader(resp.Header)
	return r
}

// WithStage 切换到新阶段，并把上一阶段的 duration 累加到 timings。
// 首次调用：把 startedAt 算作初始 stageBegin，currStage=StageNone 的耗时被丢弃
// （StageNone 不出现在 5 个 _ms 列里）。
func (r *Recorder) WithStage(s Stage) *Recorder {
	if r == nil {
		return nil
	}
	now := time.Now()
	if r.currStage != StageNone {
		r.timings[r.currStage] += now.Sub(r.stageBegin)
	}
	r.currStage = s
	r.stageBegin = now
	if r.stageHook != nil {
		r.stageHook(s)
	}
	return r
}

// SetStageHook 注册一个在每次 WithStage 时触发的回调(用于在途请求阶段追踪)。
// 传 nil 可清除。nil 接收者安全。
func (r *Recorder) SetStageHook(fn func(Stage)) {
	if r == nil {
		return
	}
	r.stageHook = fn
}

// StartedAt 返回 Recorder 创建时间(请求开始)。
func (r *Recorder) StartedAt() time.Time { return r.startedAt }

// WithFail 是 Recorder 唯一的失败上报入口。仅记录首次失败（保留根因），
// 后续调用静默忽略 — 避免 cleanup 阶段次生错误覆盖。
// nil err 视为 no-op（防止误用）。
func (r *Recorder) WithFail(s Stage, err error) *Recorder {
	if r == nil || err == nil {
		return r
	}
	if r.failStage != StageNone {
		return r
	}
	r.failStage = s
	return r
}

// WithPassthrough 标记本次 relay 走的是 HTTP 直通路径。
// Finalize 时如果 clientBody 为空，会镜像 upstreamBody 作为 client_response_body —
// 这是 passthrough 不写 ClientResponseBody 的结构性修复（spec §3.3）。
func (r *Recorder) WithPassthrough() *Recorder {
	if r == nil {
		return nil
	}
	r.passthrough = true
	return r
}

// WithLegacyTrace 把 legacy adaptor 产出的 TraceData 适配进 Recorder。
// legacy 路径不细分 stage，因此 Recorder 只能产出 upstream_dispatch / upstream_status /
// none 三档 error_stage（准确度上限，不是 bug）。
func (r *Recorder) WithLegacyTrace(td *legacy.TraceData, ch *models.Channel) *Recorder {
	if r == nil || td == nil {
		return r
	}
	if td.OutboundURL != "" {
		if u, err := url.Parse(td.OutboundURL); err == nil {
			r.outboundPath = u.Path
		}
	}
	if td.OutboundBody != nil {
		r.outboundBody = append(r.outboundBody[:0], td.OutboundBody...)
	}
	if td.OutboundHeaders != nil {
		r.outboundHeaders = cloneHeader(td.OutboundHeaders)
	}
	if td.ResponseStatus > 0 {
		r.upstreamStatus = td.ResponseStatus
	}
	if td.ResponseHeaders != nil {
		r.responseHeaders = cloneHeader(td.ResponseHeaders)
	}
	if td.ResponseBody != nil {
		r.upstreamBody.Reset()
		r.upstreamBody.Write(td.ResponseBody)
	}
	if ch != nil {
		r.channelKey = ch.Key
		r.channelBaseURL = ch.GetBaseURL()
	}
	r.passthrough = true // legacy 直通，clientBody 由 Finalize 镜像
	return r
}

// SetUpstreamBody 直接把已经读出的 body 写入 upstreamBody buffer，
// 用于 4xx/5xx 错误路径（body 被完整读出后立刻写入，无需 TeeReader）。
// 同 WrapUpstreamBody 一样受 hard-limit 控制，始终运行（即使 disabled）。
func (r *Recorder) SetUpstreamBody(body []byte) {
	if r == nil {
		return
	}
	la := &limitedAppender{r: r, target: r.upstreamBody}
	la.Write(body)
}

// WrapUpstreamBody 永远把 resp.Body 包成 TeeReader 流到 Recorder 的 upstreamBody。
// 即使 disabled，buffer 仍然累积，供 usage extraction 等业务路径复用。
// 单 buffer 硬上限 = maxBodySize × TraceBufferHardLimitMultiple，超出静默 drop（不切下游）。
func (r *Recorder) WrapUpstreamBody(resp *http.Response) io.ReadCloser {
	if r == nil || resp == nil || resp.Body == nil {
		return nil
	}
	return io.NopCloser(io.TeeReader(resp.Body, &limitedAppender{r: r, target: r.upstreamBody}))
}

// limitedAppender 让 io.TeeReader 写入 Recorder buffer 时受 hard-limit 控制。
type limitedAppender struct {
	r      *Recorder
	target *bytes.Buffer
}

func (la *limitedAppender) Write(p []byte) (int, error) {
	limit := la.r.bufferHardLimit()
	remaining := limit - la.target.Len()
	if remaining <= 0 {
		return len(p), nil // 静默 drop，对下游 TeeReader 报"全部写入"
	}
	if len(p) <= remaining {
		la.target.Write(p)
	} else {
		la.target.Write(p[:remaining])
	}
	return len(p), nil
}

func (r *Recorder) bufferHardLimit() int {
	size := r.maxBodySize
	if size <= 0 {
		size = defaultTraceMaxBodySize
	}
	return size * consts.TraceBufferHardLimitMultiple
}

// appendClientBody 给 recordingResponseWriter 调用。
func (r *Recorder) appendClientBody(p []byte) {
	if r == nil {
		return
	}
	la := &limitedAppender{r: r, target: r.clientBody}
	la.Write(p)
}

// WrapClientWriter 永远把 c.Writer 包成 recordingResponseWriter。
// 即使 Recorder disabled，buffer 仍累积（保持与 e7001e6 一致的 always-on 行为）。
func (r *Recorder) WrapClientWriter(w gin.ResponseWriter) gin.ResponseWriter {
	if r == nil || w == nil {
		return w
	}
	return newRecordingResponseWriter(w, r)
}

// UpstreamBodyBytes 暴露上游响应体 buffer 内容给业务路径（usage extraction /
// SSE scan / reconcile）。即使 enabled=false，buffer 也累积，所以此方法始终有效。
// 返回的 slice 是 buffer 内部底层数组的视图，调用方不应保存或修改。
func (r *Recorder) UpstreamBodyBytes() []byte {
	if r == nil || r.upstreamBody == nil {
		return nil
	}
	return r.upstreamBody.Bytes()
}

// ResetAttempt 在 retry loop 每次新 attempt 开始时调用，清掉 attempt 级状态：
// failStage / outbound* / upstreamStatus / responseHeaders /
// upstreamBody / clientBody。保留请求级状态：inbound* / passthrough / channelKey/baseURL。
func (r *Recorder) ResetAttempt() {
	if r == nil {
		return
	}
	r.failStage = StageNone
	r.outboundPath = ""
	r.outboundHeaders = nil
	r.outboundBody = r.outboundBody[:0]
	r.upstreamStatus = 0
	r.responseHeaders = nil
	r.upstreamBody.Reset()
	r.clientBody.Reset()
}

// buildTraceRecord 用当前 attempt 状态构建一条 mask 过的 TraceRecord。
// 调用方负责在 Finalize 场景下提前 flush 最后一个 stage 的耗时；
// SnapshotAttempt 场景不 flush（mid-attempt 快照）。
func (r *Recorder) buildTraceRecord() *TraceRecord {
	rec := &TraceRecord{
		Timings:        r.timings,
		FailStage:      r.failStage,
		UpstreamStatus: r.upstreamStatus, // 始终填入:消费方在 snapshot 上无视 verbose 直接读它(轻量字段,无安全风险)
	}

	// verbose 决策：enabled OR 任意失败。
	// failStage != StageNone 表示 WithFail 被调过；这是回归 169892f 之前
	// "失败请求强制落 body" 的老语义（见 design §背景.二次发现）。
	verbose := r.enabled || r.failStage != StageNone
	rec.Verbose = verbose
	if !verbose {
		return rec
	}

	secrets := channelSecrets(r.channelKey, r.channelBaseURL)
	limit := r.maxBodySize
	if limit <= 0 {
		limit = defaultTraceMaxBodySize
	}

	rec.InboundPath = r.inboundPath
	rec.InboundHeaders = http.Header(maskHeaders(r.inboundHeaders, secrets))
	rec.InboundBody = truncateBodyWithLimit(maskText(string(r.inboundBody), secrets), limit)
	rec.OutboundPath = r.outboundPath
	rec.OutboundHeaders = http.Header(maskHeaders(r.outboundHeaders, secrets))
	rec.OutboundBody = truncateBodyWithLimit(maskText(string(r.outboundBody), secrets), limit)
	rec.ResponseHeaders = http.Header(maskHeaders(r.responseHeaders, secrets))
	rec.UpstreamBody = truncateBodyWithLimit(maskText(r.upstreamBody.String(), secrets), limit)

	// 客户端响应体：passthrough 路径若未独立采集（clientBody 空）则镜像 upstream。
	clientRaw := r.clientBody.String()
	if clientRaw == "" && r.passthrough {
		clientRaw = r.upstreamBody.String()
	}
	rec.ClientResponseBody = truncateBodyWithLimit(maskText(clientRaw, secrets), limit)

	return rec
}

// Finalize 是 Recorder 的终结方法，由 attachTraceData 唯一调用。
// 永远返回非 nil TraceRecord：
//   - 轻量字段（Timings / FailStage / UpstreamStatus）始终填
//   - 重字段（4 body + 4 headers）按 verbose 条件填
//     verbose = enabled || failStage != StageNone
//
// 内部对 panic 用 recover 兜底，确保 trace 系统再烂也不能拖垮业务路径。
// 失败描述对外可见的位置是 UsageLog.ErrorMessage（HTTP body 同源），
// trace 层只保留 FailStage 用于 stage 定位。
func (r *Recorder) Finalize() (rec *TraceRecord) {
	if r == nil {
		return &TraceRecord{
			Timings: map[Stage]time.Duration{},
		}
	}
	defer func() {
		if p := recover(); p != nil {
			rec = &TraceRecord{
				FailStage: StageInternal,
				Timings:   map[Stage]time.Duration{},
			}
		}
	}()

	// 把当前 stage 的耗时累上去（避免最后一个 stage 丢失）。
	// timings 在正常构造路径下不应为 nil；若为 nil 表示内部状态损坏，
	// 写 nil map 会产生 panic，由上方 recover 捕获并返回 StageInternal。
	if r.currStage != StageNone && r.currStage != "" {
		r.timings[r.currStage] += time.Since(r.stageBegin)
		r.currStage = StageNone
	}
	if r.timings == nil {
		panic("recorder timings map is nil: corrupted internal state")
	}

	return r.buildTraceRecord()
}

// SnapshotAttempt 把当前 attempt 的 mask trace 收进累加切片，供 publish 逐候选落库。
// nil 接收者安全（no-op）。
func (r *Recorder) SnapshotAttempt() {
	if r == nil {
		return
	}
	r.attempts = append(r.attempts, r.buildTraceRecord())
}

// Attempts 返回已快照的逐候选 TraceRecord（顺序即候选顺序）。
// nil 接收者安全（返回 nil）。
func (r *Recorder) Attempts() []*TraceRecord {
	if r == nil {
		return nil
	}
	return r.attempts
}

// LastSnapshotVerbose 返回最近一次 SnapshotAttempt 的 verbose 判定
// (= 该候选是否会写 trace 行)。nil 接收者 / 无快照返回 false。
func (r *Recorder) LastSnapshotVerbose() bool {
	if r == nil || len(r.attempts) == 0 {
		return false
	}
	return r.attempts[len(r.attempts)-1].Verbose
}

// Enabled 从 gin.Context 里 UserInfo 取 TraceEnabled 标志。
// 提供给 relay 主包构造 Recorder 时判断 enabled 入参。
func Enabled(c *gin.Context) bool {
	v, ok := c.Get(consts.CtxKeyUserInfo)
	if !ok {
		return false
	}
	ui, ok := v.(*app.UserInfo)
	if !ok || ui == nil {
		return false
	}
	return ui.TraceEnabled
}

// cloneHeader 浅复制一份 http.Header，避免后续业务路径修改影响 trace 数据。
func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}
