package trace

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/legacy"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestNewRecorder_FieldsInitialized(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	if r == nil {
		t.Fatal("NewRecorder returned nil")
	}
	if !r.enabled {
		t.Errorf("enabled = false, want true")
	}
	if r.maxBodySize != 64*1024 {
		t.Errorf("maxBodySize = %d, want 65536", r.maxBodySize)
	}
	if r.timings == nil {
		t.Errorf("timings map not initialized")
	}
	if r.upstreamBody == nil {
		t.Errorf("upstreamBody buffer not initialized")
	}
	if r.clientBody == nil {
		t.Errorf("clientBody buffer not initialized")
	}
	if r.failStage != StageNone {
		t.Errorf("failStage = %q, want StageNone", r.failStage)
	}
}

func TestNewRecorder_Disabled(t *testing.T) {
	r := NewRecorder(false, 0)
	if r.enabled {
		t.Errorf("enabled = true, want false")
	}
	// disabled 时 buffer 仍要初始化（always-on capture）
	if r.upstreamBody == nil || r.clientBody == nil {
		t.Errorf("buffers must be initialized even when disabled")
	}
}

// --- WithInbound ---

func TestRecorder_WithInbound_StoresPathHeadersBody(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer secret")
	body := []byte(`{"model":"x"}`)

	ret := r.WithInbound(req, body)

	if ret != r {
		t.Errorf("WithInbound 应返回 *Recorder 自身以支持链式")
	}
	if r.inboundPath != "/v1/chat/completions" {
		t.Errorf("inboundPath = %q", r.inboundPath)
	}
	if r.inboundHeaders.Get("Authorization") != "Bearer secret" {
		t.Errorf("inboundHeaders 缺失 Authorization")
	}
	if string(r.inboundBody) != `{"model":"x"}` {
		t.Errorf("inboundBody = %q", string(r.inboundBody))
	}
}

// --- WithOutbound ---

func TestRecorder_WithOutbound_StoresAndPicksUpChannelSecrets(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	req := httptest.NewRequest("POST", "https://up.example/v1/chat", strings.NewReader(""))
	req.Header.Set("X-Api-Key", "upstream-secret")
	body := []byte(`{"upstream":true}`)
	ch := &models.Channel{ChannelCore: models.ChannelCore{BaseURL: "https://up.example"}, Key: "upstream-secret"}

	r.WithOutbound(req, body, ch)

	if r.outboundPath != "/v1/chat" {
		t.Errorf("outboundPath = %q", r.outboundPath)
	}
	if string(r.outboundBody) != `{"upstream":true}` {
		t.Errorf("outboundBody mismatch")
	}
	if r.channelKey != "upstream-secret" {
		t.Errorf("channelKey not captured for masking")
	}
	if r.channelBaseURL != "https://up.example" {
		t.Errorf("channelBaseURL not captured")
	}
}

// --- WithUpstreamStatus ---

func TestRecorder_WithUpstreamStatus_StoresStatusAndHeaders(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	resp := &http.Response{
		StatusCode: 502,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
	r.WithUpstreamStatus(resp)

	if r.upstreamStatus != 502 {
		t.Errorf("upstreamStatus = %d", r.upstreamStatus)
	}
	if r.responseHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("responseHeaders not captured")
	}
}

// --- WithStage ---

func TestRecorder_WithStage_AccumulatesTimings(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	r.WithStage(StageInboundDecode)
	time.Sleep(5 * time.Millisecond)
	r.WithStage(StageOutboundEncode)

	if r.timings[StageInboundDecode] < 4*time.Millisecond {
		t.Errorf("inbound_decode timing too small: %v", r.timings[StageInboundDecode])
	}
	if r.currStage != StageOutboundEncode {
		t.Errorf("currStage = %q", r.currStage)
	}
}

func TestRecorder_WithStage_FirstCallNoCrash(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	r.WithStage(StageInboundDecode)
	if r.currStage != StageInboundDecode {
		t.Errorf("currStage not set")
	}
}

// --- WithFail ---

func TestRecorder_WithFail_OnlyFirstWins(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	err1 := errors.New("first")
	err2 := errors.New("second")

	r.WithFail(StageOutboundEncode, err1)
	r.WithFail(StageInternal, err2)

	if r.failStage != StageOutboundEncode {
		t.Errorf("failStage = %q, want outbound_encode (first should win)", r.failStage)
	}
}

func TestRecorder_WithFail_NilErrIgnored(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	r.WithFail(StageInternal, nil)
	if r.failStage != StageNone {
		t.Errorf("failStage should remain StageNone for nil err")
	}
}

// --- WithPassthrough ---

func TestRecorder_WithPassthrough_MarksFlag(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	r.WithPassthrough()
	if !r.passthrough {
		t.Errorf("passthrough flag not set")
	}
}

// --- WithLegacyTrace ---

func TestRecorder_WithLegacyTrace_PopulatesAndMarksPassthrough(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	td := &legacy.TraceData{
		OutboundURL:     "https://up.example/v1/chat",
		OutboundBody:    []byte(`{"out":1}`),
		OutboundHeaders: http.Header{"X-Up": []string{"v"}},
		ResponseStatus:  200,
		ResponseHeaders: http.Header{"Content-Type": []string{"text/event-stream"}},
		ResponseBody:    []byte(`event:done`),
	}
	ch := &models.Channel{ChannelCore: models.ChannelCore{BaseURL: "https://up.example"}, Key: "k"}

	r.WithLegacyTrace(td, ch)

	if r.outboundPath != "/v1/chat" {
		t.Errorf("outboundPath = %q", r.outboundPath)
	}
	if string(r.outboundBody) != `{"out":1}` {
		t.Errorf("outboundBody mismatch")
	}
	if r.upstreamStatus != 200 {
		t.Errorf("upstreamStatus mismatch")
	}
	if r.upstreamBody.String() != "event:done" {
		t.Errorf("upstreamBody mismatch: %q", r.upstreamBody.String())
	}
	if !r.passthrough {
		t.Errorf("legacy 路径应自动标记 passthrough，以便 Finalize 镜像 client_body")
	}
	if r.channelKey != "k" {
		t.Errorf("channelKey not captured for masking")
	}
}

// --- WrapUpstreamBody ---

func TestRecorder_WrapUpstreamBody_TeeAccumulates(t *testing.T) {
	payload := strings.Repeat("a", 1024)
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(payload)),
	}
	r := NewRecorder(true, 64*1024)
	resp.Body = r.WrapUpstreamBody(resp)

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != payload {
		t.Errorf("下游读取不完整")
	}
	if r.upstreamBody.String() != payload {
		t.Errorf("Recorder buffer 累积不完整: len=%d", r.upstreamBody.Len())
	}
}

func TestRecorder_WrapUpstreamBody_HardLimitDropsExcess(t *testing.T) {
	payload := strings.Repeat("x", 100) // 远大于 maxBodySize 2
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(payload))}
	r := NewRecorder(true, 2) // hard limit = 30 * 2 = 60 bytes
	resp.Body = r.WrapUpstreamBody(resp)

	got, _ := io.ReadAll(resp.Body)
	if len(got) != len(payload) {
		t.Errorf("下游读取被截断（绝对禁止）: got %d, want %d", len(got), len(payload))
	}
	if r.upstreamBody.Len() != 60 {
		t.Errorf("hard limit 没生效: buffer len = %d, want 60", r.upstreamBody.Len())
	}
}

// --- UpstreamBodyBytes ---

func TestRecorder_UpstreamBodyBytes_ReturnsBufferContent(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	r.upstreamBody.WriteString("provider usage payload")
	got := r.UpstreamBodyBytes()
	if string(got) != "provider usage payload" {
		t.Errorf("got %q", string(got))
	}
}

func TestRecorder_UpstreamBodyBytes_EvenWhenDisabled(t *testing.T) {
	r := NewRecorder(false, 64*1024)
	r.upstreamBody.WriteString("disabled but captured")
	got := r.UpstreamBodyBytes()
	if string(got) != "disabled but captured" {
		t.Errorf("disabled recorder 也必须能读 buffer（always-on capture）")
	}
}

// --- ResetAttempt ---

func TestRecorder_ResetAttempt_ClearsAttemptScopedState(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader("inbound"))
	r.WithInbound(req, []byte("inbound-bytes"))
	r.WithOutbound(httptest.NewRequest("POST", "https://up/v1", strings.NewReader("")),
		[]byte("outbound"), &models.Channel{})
	r.WithUpstreamStatus(&http.Response{StatusCode: 200, Header: http.Header{"X": []string{"y"}}})
	r.upstreamBody.WriteString("ub")
	r.clientBody.WriteString("cb")
	r.WithFail(StageUpstreamDispatch, errors.New("dispatch fail"))
	r.WithPassthrough()

	r.ResetAttempt()

	if r.failStage != StageNone {
		t.Errorf("failStage 应被清空")
	}
	if len(r.outboundBody) != 0 {
		t.Errorf("outboundBody 应被清空")
	}
	if r.upstreamStatus != 0 {
		t.Errorf("upstreamStatus 应被清空")
	}
	if r.upstreamBody.Len() != 0 || r.clientBody.Len() != 0 {
		t.Errorf("upstream/client buffer 应被清空")
	}
	if r.responseHeaders != nil {
		t.Errorf("responseHeaders 应被清空")
	}
	// 保留：inboundBody / inboundHeaders / passthrough
	if string(r.inboundBody) != "inbound-bytes" {
		t.Errorf("inboundBody 不应被清空（请求级状态）")
	}
	if !r.passthrough {
		t.Errorf("passthrough 标记不应被清空")
	}
}

// --- Finalize ---

func TestRecorder_Finalize_DisabledNoFail_LightOnly(t *testing.T) {
	r := NewRecorder(false, 64*1024)
	r.WithInbound(httptest.NewRequest("POST", "/", nil), []byte("body"))
	r.WithStage(StageInboundDecode)
	time.Sleep(2 * time.Millisecond)

	rec := r.Finalize()
	if rec == nil {
		t.Fatal("disabled+no-fail Finalize must return non-nil record")
	}
	if rec.HasBody() {
		t.Errorf("disabled+no-fail record should have HasBody()==false (InboundPath empty)")
	}
	if rec.InboundBody != "" || rec.OutboundBody != "" || rec.UpstreamBody != "" || rec.ClientResponseBody != "" {
		t.Errorf("disabled+no-fail record should have empty bodies, got in=%q out=%q up=%q cli=%q",
			rec.InboundBody, rec.OutboundBody, rec.UpstreamBody, rec.ClientResponseBody)
	}
	if rec.Timings == nil || rec.Timings[StageInboundDecode] < time.Millisecond {
		t.Errorf("timings must be populated even when disabled, got %v", rec.Timings)
	}
	if rec.FailStage != StageNone {
		t.Errorf("FailStage should be None, got %q", rec.FailStage)
	}
}

func TestRecorder_Finalize_HappyPath(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer client-secret")
	r.WithInbound(req, []byte(`{"in":1}`))
	outReq := httptest.NewRequest("POST", "https://up/v1/chat", strings.NewReader(""))
	outReq.Header.Set("X-Api-Key", "upstream-key-AAA")
	r.WithOutbound(outReq, []byte(`{"out":1}`),
		&models.Channel{ChannelCore: models.ChannelCore{BaseURL: "https://up"}, Key: "upstream-key-AAA"})
	r.WithUpstreamStatus(&http.Response{StatusCode: 200, Header: http.Header{"X-Up": []string{"v"}}})
	r.upstreamBody.WriteString(`{"resp":1}`)
	r.clientBody.WriteString(`{"cli":1}`)
	r.WithStage(StageInboundDecode)
	time.Sleep(2 * time.Millisecond)
	r.WithStage(StageOutboundEncode)

	rec := r.Finalize()
	if rec == nil {
		t.Fatal("Finalize returned nil")
	}
	if rec.InboundPath != "/v1/chat" || string(rec.InboundBody) != `{"in":1}` {
		t.Errorf("inbound mismatch: path=%q body=%q", rec.InboundPath, rec.InboundBody)
	}
	if rec.OutboundBody != `{"out":1}` || rec.OutboundPath != "/v1/chat" {
		t.Errorf("outbound mismatch: path=%q body=%q", rec.OutboundPath, rec.OutboundBody)
	}
	if rec.UpstreamStatus != 200 || rec.UpstreamBody != `{"resp":1}` {
		t.Errorf("upstream mismatch: status=%d body=%q", rec.UpstreamStatus, rec.UpstreamBody)
	}
	if rec.ClientResponseBody != `{"cli":1}` {
		t.Errorf("client mismatch: %q", rec.ClientResponseBody)
	}
	if rec.FailStage != StageNone {
		t.Errorf("FailStage should be none, got %q", rec.FailStage)
	}
	if rec.Timings[StageInboundDecode] < 1*time.Millisecond {
		t.Errorf("timings missing: %v", rec.Timings[StageInboundDecode])
	}
	// 脱敏：upstream key 应被 mask
	if strings.Contains(rec.OutboundBody, "upstream-key-AAA") {
		t.Errorf("upstream key 应该被 mask")
	}
}

func TestRecorder_Finalize_PassthroughMirrorsUpstreamToClient(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	r.WithPassthrough()
	r.upstreamBody.WriteString(`{"upstream":"data"}`)
	// clientBody 始终空（passthrough 走 io.Copy）
	rec := r.Finalize()

	if rec.ClientResponseBody != `{"upstream":"data"}` {
		t.Errorf("passthrough 时 client_response_body 应镜像 upstream，got %q", rec.ClientResponseBody)
	}
}

func TestRecorder_Finalize_TruncatesBodiesToMaxBodySize(t *testing.T) {
	r := NewRecorder(true, 10) // maxBodySize 故意很小
	r.WithInbound(httptest.NewRequest("POST", "/", nil), []byte(strings.Repeat("a", 50)))
	rec := r.Finalize()
	// truncateBodyWithLimit 会追加 "...(truncated)"，所以实际长度 = 10 + len("...(truncated)") = 24
	// 只验证截断了原始 50 byte 内容，不超过 10 + suffix
	if len(rec.InboundBody) >= 50 {
		t.Errorf("inbound body 未截断：%d", len(rec.InboundBody))
	}
}

func TestRecorder_Finalize_PanicSafe(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	// 注入坏状态：把 timings 设为 nil 让 collectTimings 内部某处可能 nil-deref
	r.timings = nil
	var rec *TraceRecord
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("Finalize panic 没被 recover: %v", p)
			}
		}()
		rec = r.Finalize()
	}()
	if rec == nil {
		t.Errorf("panic-safe 模式下也应返回最小 TraceRecord")
	}
	if rec.FailStage != StageInternal {
		t.Errorf("panic 时 FailStage 应被设为 StageInternal，got %q", rec.FailStage)
	}
}

// --- TraceRecord.MarshalJSON ---

func TestTraceRecord_MarshalJSON_AlignsWithUsageLogTrace(t *testing.T) {
	rec := &TraceRecord{
		InboundPath:        "/v1/chat",
		InboundHeaders:     http.Header{"X-In": []string{"v"}},
		InboundBody:        `{"in":1}`,
		OutboundPath:       "/v1/upstream",
		OutboundHeaders:    http.Header{"X-Out": []string{"v"}},
		OutboundBody:       `{"out":1}`,
		ResponseHeaders:    http.Header{"X-Up": []string{"v"}},
		UpstreamBody:       `{"resp":1}`,
		ClientResponseBody: `{"cli":1}`,
		UpstreamStatus:     200,
		FailStage:          StageOutboundEncode,
	}
	b, err := rec.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	// 与 models.UsageLogTrace JSON tag 完全对齐
	var asMap map[string]any
	if err := json.Unmarshal(b, &asMap); err != nil {
		t.Fatalf("unmarshal back: %v", err)
	}
	wantKeys := []string{
		"inbound_path", "outbound_path", "inbound_headers", "outbound_headers",
		"inbound_body", "outbound_body", "response_headers", "response_body",
		"client_response_body", "upstream_status", "error_stage",
	}
	for _, k := range wantKeys {
		if _, ok := asMap[k]; !ok {
			t.Errorf("缺 key: %s", k)
		}
	}
	if asMap["error_stage"] != "outbound_encode" {
		t.Errorf("error_stage 序列化错误: %v", asMap["error_stage"])
	}
	if asMap["upstream_status"].(float64) != 200 {
		t.Errorf("upstream_status 序列化错误")
	}
	// Headers 字段在 UsageLogTrace 模型是 string 类型，
	// 因此 MarshalJSON 输出的 headers 字段值必须是 JSON 字符串（非 object）。
	if _, ok := asMap["inbound_headers"].(string); !ok {
		t.Errorf("inbound_headers 应为 JSON 字符串，而非 object，实际: %T %v", asMap["inbound_headers"], asMap["inbound_headers"])
	}
	if _, ok := asMap["outbound_headers"].(string); !ok {
		t.Errorf("outbound_headers 应为 JSON 字符串，而非 object，实际: %T %v", asMap["outbound_headers"], asMap["outbound_headers"])
	}
	// 确认 UsageLogTrace 可以正确反序列化（settler 路径验证）
	var trace models.UsageLogTrace
	if err := json.Unmarshal(b, &trace); err != nil {
		t.Errorf("settler Unmarshal 失败: %v", err)
	}
	if trace.UpstreamStatus != 200 {
		t.Errorf("trace.UpstreamStatus 反序列化错误: %d", trace.UpstreamStatus)
	}
}

func TestTraceRecord_HasBody_EmptyInboundPath(t *testing.T) {
	rec := &TraceRecord{InboundPath: ""}
	if rec.HasBody() {
		t.Errorf("HasBody() should be false when InboundPath is empty")
	}
}

func TestTraceRecord_HasBody_NonEmptyInboundPath(t *testing.T) {
	rec := &TraceRecord{InboundPath: "/v1/chat/completions"}
	if !rec.HasBody() {
		t.Errorf("HasBody() should be true when InboundPath is set")
	}
}

func TestTraceRecord_HasBody_NilReceiver(t *testing.T) {
	var rec *TraceRecord
	if rec.HasBody() {
		t.Errorf("nil receiver HasBody() should be false")
	}
}

func TestRecorder_Finalize_DisabledWithFail_Verbose(t *testing.T) {
	r := NewRecorder(false, 64*1024)
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(""))
	r.WithInbound(req, []byte(`{"in":1}`))
	r.WithFail(StageInboundDecode, errors.New("bad body"))

	rec := r.Finalize()
	if rec == nil {
		t.Fatal("disabled+fail Finalize must return non-nil")
	}
	if !rec.HasBody() {
		t.Errorf("disabled+fail should be verbose (HasBody()==true)")
	}
	if rec.InboundPath != "/v1/chat" {
		t.Errorf("InboundPath mismatch: %q", rec.InboundPath)
	}
	if rec.InboundBody != `{"in":1}` {
		t.Errorf("InboundBody should be filled in verbose mode, got %q", rec.InboundBody)
	}
	if rec.FailStage != StageInboundDecode {
		t.Errorf("FailStage mismatch: %q", rec.FailStage)
	}
}

func TestRecorder_Finalize_EnabledWithFail_Verbose(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(""))
	r.WithInbound(req, []byte(`{"in":2}`))
	r.WithStage(StageUpstreamDispatch)
	r.WithFail(StageUpstreamDispatch, errors.New("net err"))

	rec := r.Finalize()
	if !rec.HasBody() {
		t.Errorf("enabled+fail should be verbose")
	}
	if rec.FailStage != StageUpstreamDispatch {
		t.Errorf("FailStage mismatch: %q", rec.FailStage)
	}
}

func TestRecorder_Finalize_NilReceiver(t *testing.T) {
	var r *Recorder
	rec := r.Finalize()
	if rec == nil {
		t.Fatal("nil receiver Finalize must return non-nil (defensive)")
	}
	if rec.HasBody() {
		t.Errorf("nil-receiver record should have HasBody()==false")
	}
	if rec.Timings == nil {
		t.Errorf("nil-receiver record should still have empty Timings map (not nil)")
	}
}

func TestRecorder_Finalize_DoubleCall_NoPanic(t *testing.T) {
	r := NewRecorder(true, 64*1024)
	r.WithInbound(httptest.NewRequest("POST", "/", nil), []byte("body"))
	r.WithStage(StageInboundDecode)

	rec1 := r.Finalize()
	if rec1 == nil {
		t.Fatal("first Finalize returned nil")
	}
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("second Finalize panicked: %v", p)
		}
	}()
	rec2 := r.Finalize()
	if rec2 == nil {
		t.Fatal("second Finalize returned nil")
	}
}

func TestRecorder_Finalize_TimingsAccumulatedAcrossStages(t *testing.T) {
	r := NewRecorder(false, 64*1024) // disabled，验证 timings 仍累积
	r.WithStage(StageInboundDecode)
	time.Sleep(2 * time.Millisecond)
	r.WithStage(StageUpstreamDispatch)
	time.Sleep(3 * time.Millisecond)
	r.WithStage(StageUpstreamDecode)
	time.Sleep(1 * time.Millisecond)

	rec := r.Finalize()
	if rec.Timings[StageInboundDecode] < time.Millisecond {
		t.Errorf("InboundDecode timing missing: %v", rec.Timings[StageInboundDecode])
	}
	if rec.Timings[StageUpstreamDispatch] < 2*time.Millisecond {
		t.Errorf("UpstreamDispatch timing missing: %v", rec.Timings[StageUpstreamDispatch])
	}
	if rec.Timings[StageUpstreamDecode] < time.Microsecond {
		t.Errorf("UpstreamDecode timing missing: %v", rec.Timings[StageUpstreamDecode])
	}
}

// TestTraceRecordHasNoFailErr: 防回归——TraceRecord 不再含 FailErr 字段。
// 历史上该字段从未在 MarshalJSON 输出，删除后任何外部读都是编译错。
func TestTraceRecordHasNoFailErr(t *testing.T) {
	rec := TraceRecord{}
	_ = rec.FailStage // 编译期 guard
	tp := reflect.TypeOf(rec)
	for i := 0; i < tp.NumField(); i++ {
		if tp.Field(i).Name == "FailErr" {
			t.Fatalf("FailErr was reintroduced; intentionally removed because MarshalJSON never exported it")
		}
	}
}
