package trace

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	backendcommon "github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/legacy"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
)

type CaptureMode uint8

const (
	CaptureOff CaptureMode = iota
	CaptureHeaders
	CaptureFull
)

var (
	unknownTraceModeSuppressor = diagnostics.NewSuppressor(diagnostics.SuppressorOptions{})
	traceModeNow               = time.Now
)

// Recorder 是 request-scoped 的 trace 数据 + 请求体 buffer 唯一持有者。
// 字段全 unexport，状态变更只能经下面的 With* / Wrap* / ResetAttempt / Finalize 方法。
type Recorder struct {
	mode        CaptureMode
	maxBodySize int
	bodyMask    func(string, []string) string

	startedAt  time.Time
	stageBegin time.Time
	currStage  Stage
	timings    map[Stage]time.Duration

	inboundPath    string
	inboundHeaders http.Header
	inboundBody    []byte
	inboundSeen    int64

	outboundPath    string
	outboundHeaders http.Header
	outboundBody    []byte
	outboundSeen    int64

	upstreamStatus  int
	responseHeaders http.Header
	upstreamBody    *backendcommon.MaskingTail

	clientBody  *backendcommon.MaskingTail
	passthrough bool

	channelKey     string
	channelBaseURL string

	failStage Stage
	stageHook func(Stage) // 可选:每次 WithStage 时回调(供 in-flight 追踪)

	attempts []*TraceRecord // 每候选 SnapshotAttempt() 追加一条
}

// NewRecorder 创建一个 request-scoped Recorder。mode 控制成功 attempt 的 trace
// 捕获范围；业务所需的 response buffer 始终累积。
func NewRecorder(mode CaptureMode, maxBodySize int) *Recorder {
	return NewRecorderAt(mode, maxBodySize, time.Now())
}

// NewRecorderAt creates a recorder whose elapsed timings share the request's ingress start.
func NewRecorderAt(mode CaptureMode, maxBodySize int, startedAt time.Time) *Recorder {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	bufferLimit := traceBufferHardLimit(maxBodySize)
	return &Recorder{
		mode:         mode,
		maxBodySize:  maxBodySize,
		bodyMask:     maskText,
		startedAt:    startedAt,
		stageBegin:   startedAt,
		currStage:    StageNone,
		timings:      make(map[Stage]time.Duration),
		upstreamBody: backendcommon.NewMaskingTail(bufferLimit),
		clientBody:   backendcommon.NewMaskingTail(bufferLimit),
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
	r.inboundBody, r.inboundSeen = boundedTail(body, r.bufferHardLimit())
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
	r.outboundBody, r.outboundSeen = boundedTail(body, r.bufferHardLimit())
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
		r.outboundBody, r.outboundSeen = boundedTail(td.OutboundBody, r.bufferHardLimit())
		if td.OutboundBodySeen > r.outboundSeen {
			r.outboundSeen = td.OutboundBodySeen
		}
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
		_, _ = r.upstreamBody.Write(td.ResponseBody)
		r.upstreamBody.SetTotalSeenLowerBound(td.ResponseBodySeen)
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
	_, _ = r.upstreamBody.Write(body)
}

// SetUpstreamBodyCapture installs a bounded upstream tail while preserving the
// reader's lower-bound byte count for final truncation metadata.
func (r *Recorder) SetUpstreamBodyCapture(body []byte, totalSeen int64, truncated bool) {
	if r == nil {
		return
	}
	r.upstreamBody.Reset()
	_, _ = r.upstreamBody.Write(body)
	r.upstreamBody.SetTotalSeenLowerBound(totalSeen)
	if truncated {
		r.upstreamBody.SetTotalSeenLowerBound(int64(len(body)) + 1)
	}
}

// WrapUpstreamBody 永远把 resp.Body 包成 TeeReader 流到 Recorder 的 upstreamBody。
// 即使 disabled，buffer 仍然累积，供 usage extraction 等业务路径复用。
// 单 buffer 硬上限 = maxBodySize × TraceBufferHardLimitMultiple；超限淘汰最早
// 字节并继续保留真实流尾，不影响下游读取。
func (r *Recorder) WrapUpstreamBody(resp *http.Response) io.ReadCloser {
	if r == nil || resp == nil || resp.Body == nil {
		return nil
	}
	return io.NopCloser(io.TeeReader(resp.Body, r.upstreamBody))
}

// tailAppender is a fixed-capacity streaming window that always acknowledges
// the original write length so capture cannot interfere with relay I/O.
type tailAppender struct {
	buf       []byte
	limit     int
	totalSeen int64
}

func newTailAppender(limit int) *tailAppender {
	if limit <= 0 {
		limit = defaultTraceMaxBodySize
	}
	return &tailAppender{limit: limit}
}

func (a *tailAppender) Write(p []byte) (int, error) {
	originalLen := len(p)
	a.totalSeen += int64(originalLen)
	if originalLen >= a.limit {
		a.buf = append(a.buf[:0], p[originalLen-a.limit:]...)
		return originalLen, nil
	}
	overflow := len(a.buf) + originalLen - a.limit
	if overflow > 0 {
		copy(a.buf, a.buf[overflow:])
		a.buf = a.buf[:len(a.buf)-overflow]
	}
	a.buf = append(a.buf, p...)
	return originalLen, nil
}

func (a *tailAppender) WriteString(s string) (int, error) { return a.Write([]byte(s)) }
func (a *tailAppender) Bytes() []byte                     { return a.buf }
func (a *tailAppender) String() string                    { return string(a.buf) }
func (a *tailAppender) Len() int                          { return len(a.buf) }
func (a *tailAppender) Limit() int                        { return a.limit }
func (a *tailAppender) TotalSeen() int64                  { return a.totalSeen }
func (a *tailAppender) Truncated() bool                   { return a.totalSeen > int64(a.limit) }
func (a *tailAppender) Reset() {
	a.buf = a.buf[:0]
	a.totalSeen = 0
}

func boundedTail(body []byte, limit int) ([]byte, int64) {
	a := backendcommon.NewMaskingTail(limit)
	_, _ = a.Write(body)
	return append([]byte(nil), a.Bytes()...), a.TotalSeen()
}

func traceBufferHardLimit(size int) int {
	if size <= 0 {
		size = defaultTraceMaxBodySize
	}
	return size * consts.TraceBufferHardLimitMultiple
}

func (r *Recorder) bufferHardLimit() int { return traceBufferHardLimit(r.maxBodySize) }

// appendClientBody 给 recordingResponseWriter 调用。
func (r *Recorder) appendClientBody(p []byte) {
	if r == nil {
		return
	}
	_, _ = r.clientBody.Write(p)
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
	r.outboundSeen = 0
	r.upstreamStatus = 0
	r.responseHeaders = nil
	r.upstreamBody.Reset()
	r.clientBody.Reset()
}

// buildSummary 构建不含持久化 payload 的轻量 attempt 摘要。
func (r *Recorder) buildSummary() *TraceRecord {
	return &TraceRecord{
		Timings:        cloneTimings(r.timings),
		FailStage:      r.failStage,
		UpstreamStatus: r.upstreamStatus,
	}
}

func (r *Recorder) effectiveMode() CaptureMode {
	if r.failStage != StageNone {
		return CaptureFull
	}
	return r.mode
}

func (r *Recorder) fillHeaders(rec *TraceRecord, secrets []string) {
	rec.InboundPath = r.inboundPath
	rec.InboundHeaders = http.Header(maskHeaders(r.inboundHeaders, secrets))
	rec.OutboundPath = r.outboundPath
	rec.OutboundHeaders = http.Header(maskHeaders(r.outboundHeaders, secrets))
	rec.ResponseHeaders = http.Header(maskHeaders(r.responseHeaders, secrets))
}

func (r *Recorder) fillBodies(rec *TraceRecord, secrets []string) {
	limit := r.maxBodySize
	if limit <= 0 {
		limit = defaultTraceMaxBodySize
	}
	rec.InboundBody = r.finalizeBody(r.inboundBody, r.inboundSeen, secrets, limit)
	rec.OutboundBody = r.finalizeBody(r.outboundBody, r.outboundSeen, secrets, limit)
	rec.UpstreamBody = r.finalizeBody(r.upstreamBody.Bytes(), r.upstreamBody.TotalSeen(), secrets, limit)

	rec.ClientResponseBody = r.clientResponseBody(secrets, limit)
}

func (r *Recorder) finalizeBody(raw []byte, totalSeen int64, secrets []string, limit int) string {
	if totalSeen > int64(len(raw)) {
		raw = sanitizeTruncatedLeadingFragment(raw, secrets)
	}
	masked := []byte(r.bodyMask(string(raw), secrets))
	return string(truncateBodyTail(masked, limit, totalSeen > int64(len(raw)) || totalSeen > int64(limit)))
}

func sanitizeTruncatedLeadingFragment(raw []byte, secrets []string) []byte {
	matched := 0
	for _, secret := range secrets {
		if overlap := leadingSecretSuffixLength(raw, []byte(secret)); overlap > matched {
			matched = overlap
		}
	}
	raw = raw[matched:]
	return raw
}

func leadingSecretSuffixLength(raw, secret []byte) int {
	maxLen := len(secret)
	if maxLen > len(raw) {
		maxLen = len(raw)
	}
	if maxLen == 0 {
		return 0
	}
	pattern := raw[:maxLen]
	prefix := make([]int, len(pattern))
	for i, matched := 1, 0; i < len(pattern); i++ {
		for matched > 0 && pattern[i] != pattern[matched] {
			matched = prefix[matched-1]
		}
		if pattern[i] == pattern[matched] {
			matched++
		}
		prefix[i] = matched
	}
	matched := 0
	for index, b := range secret {
		for matched > 0 && b != pattern[matched] {
			matched = prefix[matched-1]
		}
		if b == pattern[matched] {
			matched++
			if matched == len(pattern) {
				if index == len(secret)-1 {
					return matched
				}
				matched = prefix[matched-1]
			}
		}
	}
	return matched
}

func (r *Recorder) clientResponseBody(secrets []string, limit int) string {
	clientRaw := r.clientBody.String()
	totalSeen := r.clientBody.TotalSeen()
	if clientRaw == "" && r.passthrough {
		clientRaw = r.upstreamBody.String()
		totalSeen = r.upstreamBody.TotalSeen()
	}
	return r.finalizeBody([]byte(clientRaw), totalSeen, secrets, limit)
}

func (r *Recorder) safeClientResponseBody() (body string, ok bool) {
	defer func() {
		if recover() != nil {
			body, ok = "", false
		}
	}()
	limit := r.maxBodySize
	if limit <= 0 {
		limit = defaultTraceMaxBodySize
	}
	return r.clientResponseBody(channelSecrets(r.channelKey, r.channelBaseURL), limit), true
}

// BuildRemoteFailureBodyFallback returns the bounded, masked target bodies
// needed only if a Header-only remote success is interrupted after its Result.
func (r *Recorder) BuildRemoteFailureBodyFallback() (rec *TraceRecord) {
	if r == nil || r.mode != CaptureHeaders || r.failStage != StageNone {
		return nil
	}
	defer func() {
		if recover() != nil {
			rec = nil
		}
	}()
	rec = &TraceRecord{}
	limit := r.maxBodySize
	if limit <= 0 {
		limit = defaultTraceMaxBodySize
	}
	secrets := channelSecrets(r.channelKey, r.channelBaseURL)
	rec.InboundBody = truncateBodyWithLimit(maskTextPreservingLength(string(r.inboundBody), secrets), limit)
	rec.OutboundBody = truncateBodyWithLimit(maskTextPreservingLength(string(r.outboundBody), secrets), limit)
	rec.UpstreamBody = truncateBodyWithLimit(maskTextPreservingLength(r.upstreamBody.String(), secrets), limit)
	clientRaw := r.clientBody.String()
	if clientRaw == "" && r.passthrough {
		clientRaw = r.upstreamBody.String()
	}
	rec.ClientResponseBody = truncateBodyWithLimit(maskTextPreservingLength(clientRaw, secrets), limit)
	return rec
}

// buildTraceRecord 用当前 attempt 状态构建一条按 effective mode 脱敏的记录。
func (r *Recorder) buildTraceRecord() *TraceRecord {
	rec := r.buildSummary()
	mode := r.effectiveMode()
	if mode == CaptureOff {
		return rec
	}
	secrets := channelSecrets(r.channelKey, r.channelBaseURL)
	r.fillHeaders(rec, secrets)
	if mode == CaptureFull {
		r.fillBodies(rec, secrets)
	}
	rec.Verbose = true
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

// RefreshLastAttemptClientResponse updates the final upstream-status attempt
// after the outer handler writes its error response. Other attempt fields stay
// frozen at execution time, preserving fallback ordering and remote ownership.
func (r *Recorder) RefreshLastAttemptClientResponse() {
	if r == nil || len(r.attempts) == 0 {
		return
	}
	last := r.attempts[len(r.attempts)-1]
	if last == nil || !last.Verbose || last.FailStage != StageUpstreamStatus {
		return
	}
	body, ok := r.safeClientResponseBody()
	if ok {
		last.ClientResponseBody = body
	}
}

// AppendAttempt appends a trace produced by a remote attempt. A nil trace is
// kept as an empty snapshot so Attempts indexes continue to match fallback Seq.
func (r *Recorder) AppendAttempt(record *TraceRecord) {
	if r == nil {
		return
	}
	r.attempts = append(r.attempts, cloneTraceRecord(record))
}

// AppendFailedRemoteAttempt upgrades a remote attempt to Full after the source
// detects a later transport or protocol failure. Target fields take priority;
// the source snapshot fills fields the target did not capture.
func (r *Recorder) AppendFailedRemoteAttempt(record *TraceRecord, err error) {
	if r == nil {
		return
	}
	if err == nil {
		r.AppendAttempt(record)
		return
	}
	r.WithFail(StageInternal, err)
	r.attempts = append(r.attempts, mergeRemoteTrace(r.buildTraceRecord(), record))
}

// Attempts 返回已快照的逐候选 TraceRecord（顺序即候选顺序）。
// nil 接收者安全（返回 nil）。
func (r *Recorder) Attempts() []*TraceRecord {
	if r == nil {
		return nil
	}
	attempts := make([]*TraceRecord, len(r.attempts))
	for i, attempt := range r.attempts {
		attempts[i] = cloneTraceRecord(attempt)
	}
	return attempts
}

func cloneTraceRecord(record *TraceRecord) *TraceRecord {
	if record == nil {
		return &TraceRecord{Timings: map[Stage]time.Duration{}}
	}
	return &TraceRecord{
		InboundPath:        strings.Clone(record.InboundPath),
		InboundHeaders:     record.InboundHeaders.Clone(),
		InboundBody:        strings.Clone(record.InboundBody),
		OutboundPath:       strings.Clone(record.OutboundPath),
		OutboundHeaders:    record.OutboundHeaders.Clone(),
		OutboundBody:       strings.Clone(record.OutboundBody),
		ResponseHeaders:    record.ResponseHeaders.Clone(),
		UpstreamBody:       strings.Clone(record.UpstreamBody),
		ClientResponseBody: strings.Clone(record.ClientResponseBody),
		UpstreamStatus:     record.UpstreamStatus,
		FailStage:          record.FailStage,
		Timings:            cloneTimings(record.Timings),
		Verbose:            record.Verbose,
	}
}

func mergeRemoteTrace(source, target *TraceRecord) *TraceRecord {
	merged := cloneTraceRecord(source)
	if target == nil {
		return merged
	}
	mergeRemoteTraceMetadata(merged, target)
	mergeRemoteTraceBodies(merged, target)
	merged.Verbose = true
	return merged
}

func mergeRemoteTraceMetadata(merged, target *TraceRecord) {
	if target.InboundPath != "" {
		merged.InboundPath = strings.Clone(target.InboundPath)
	}
	if len(target.InboundHeaders) > 0 {
		merged.InboundHeaders = target.InboundHeaders.Clone()
	}
	if target.OutboundPath != "" {
		merged.OutboundPath = strings.Clone(target.OutboundPath)
	}
	if len(target.OutboundHeaders) > 0 {
		merged.OutboundHeaders = target.OutboundHeaders.Clone()
	}
	if len(target.ResponseHeaders) > 0 {
		merged.ResponseHeaders = target.ResponseHeaders.Clone()
	}
	if target.UpstreamStatus != 0 {
		merged.UpstreamStatus = target.UpstreamStatus
	}
	if len(target.Timings) > 0 {
		merged.Timings = cloneTimings(target.Timings)
	}
	if target.FailStage != "" && target.FailStage != StageNone {
		merged.FailStage = target.FailStage
	}
}

func mergeRemoteTraceBodies(merged, target *TraceRecord) {
	if target.InboundBody != "" {
		merged.InboundBody = strings.Clone(target.InboundBody)
	}
	if target.OutboundBody != "" {
		merged.OutboundBody = strings.Clone(target.OutboundBody)
	}
	if target.UpstreamBody != "" {
		merged.UpstreamBody = strings.Clone(target.UpstreamBody)
	}
	if target.ClientResponseBody != "" {
		merged.ClientResponseBody = strings.Clone(target.ClientResponseBody)
	}
}

func cloneTimings(timings map[Stage]time.Duration) map[Stage]time.Duration {
	cloned := make(map[Stage]time.Duration, len(timings))
	for stage, duration := range timings {
		cloned[stage] = duration
	}
	return cloned
}

// LastSnapshotVerbose 返回最近一次 SnapshotAttempt 的 verbose 判定
// (= 该候选是否会写 trace 行)。nil 接收者 / 无快照返回 false。
func (r *Recorder) LastSnapshotVerbose() bool {
	if r == nil || len(r.attempts) == 0 {
		return false
	}
	return r.attempts[len(r.attempts)-1].Verbose
}

// CaptureModeFromContext 从鉴权上下文读取并规范化成功 attempt 的捕获模式。
func CaptureModeFromContext(c *gin.Context) CaptureMode {
	if c == nil {
		return CaptureOff
	}
	v, ok := c.Get(consts.CtxKeyUserInfo)
	if !ok {
		return CaptureOff
	}
	ui, ok := v.(*app.UserInfo)
	if !ok || ui == nil || !ui.TraceEnabled {
		return CaptureOff
	}
	mode, unknown := ui.TraceMode.ForRuntime()
	if unknown {
		logUnknownTraceMode(ui.TraceMode)
	}
	if mode == models.TokenTraceModeHeaders {
		return CaptureHeaders
	}
	return CaptureFull
}

func logUnknownTraceMode(mode models.TokenTraceMode) {
	key := diagnostics.SuppressionKey{Source: "token_auth", Stage: "trace_mode", ReasonCode: "unknown_token_trace_mode"}
	decision := unknownTraceModeSuppressor.Observe(key, traceModeNow())
	if decision.Allow {
		zap.L().Warn("unknown token trace mode; falling back to full", zap.String("trace_mode", string(mode)))
		return
	}
	if decision.Summary != nil {
		zap.L().Warn("unknown token trace mode warnings suppressed", zap.Uint64("suppressed_count", decision.Summary.SuppressedCount))
	}
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
