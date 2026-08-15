package trace

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/legacy"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLLMRecorderBehaviorMatchesBeforeExtraction(t *testing.T) {
	r := NewRecorder(CaptureFull, 8)
	inbound := httptest.NewRequest(http.MethodPost, "/v1/chat", nil)
	inbound.Header.Set("Authorization", "Bearer inbound-secret")
	r.WithInbound(inbound, []byte("llm-request"))
	outbound := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/chat", nil)
	outbound.Header.Set("X-Api-Key", "channel-secret")
	r.WithOutbound(outbound, []byte("llm-request"), &models.Channel{Key: "channel-secret"})
	r.WithUpstreamStatus(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Set-Cookie": {"secret"}}})
	r.SetUpstreamBody([]byte("llm-response"))
	r.WithPassthrough()
	record := r.Finalize()
	got := map[string]any{
		"inbound_path": record.InboundPath, "inbound_authorization": record.InboundHeaders.Get("Authorization"),
		"inbound_body": record.InboundBody, "outbound_path": record.OutboundPath,
		"outbound_api_key": record.OutboundHeaders.Get("X-Api-Key"), "outbound_body": record.OutboundBody,
		"upstream_status": record.UpstreamStatus, "response_cookie": record.ResponseHeaders.Get("Set-Cookie"),
		"upstream_body": record.UpstreamBody, "client_body": record.ClientResponseBody, "verbose": record.Verbose,
	}
	gotRaw, err := json.Marshal(got)
	require.NoError(t, err)
	got = nil
	require.NoError(t, json.Unmarshal(gotRaw, &got))

	wantRaw, err := os.ReadFile("testdata/recorder_behavior.golden.json")
	require.NoError(t, err)
	var want map[string]any
	require.NoError(t, json.Unmarshal(wantRaw, &want))
	require.Equal(t, want, got)
}

func TestCaptureModeFromContext(t *testing.T) {
	tests := []struct {
		name string
		user *app.UserInfo
		want CaptureMode
	}{
		{name: "missing user", want: CaptureOff},
		{name: "disabled ignores mode", user: &app.UserInfo{TraceMode: models.TokenTraceModeHeaders}, want: CaptureOff},
		{name: "legacy empty is full", user: &app.UserInfo{TraceEnabled: true}, want: CaptureFull},
		{name: "headers", user: &app.UserInfo{TraceEnabled: true, TraceMode: models.TokenTraceModeHeaders}, want: CaptureHeaders},
		{name: "unknown is full", user: &app.UserInfo{TraceEnabled: true, TraceMode: "future"}, want: CaptureFull},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.user != nil {
				c.Set(consts.CtxKeyUserInfo, tc.user)
			}
			if got := CaptureModeFromContext(c); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestCaptureModeFromContextSuppressesUnknownModeWarnings(t *testing.T) {
	originalSuppressor, originalNow := unknownTraceModeSuppressor, traceModeNow
	originalLogger := zap.L()
	t.Cleanup(func() {
		unknownTraceModeSuppressor, traceModeNow = originalSuppressor, originalNow
		zap.ReplaceGlobals(originalLogger)
	})

	base := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	now := base
	traceModeNow = func() time.Time { return now }
	unknownTraceModeSuppressor = diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute, MaxKeys: 2})
	core, logs := observer.New(zap.WarnLevel)
	zap.ReplaceGlobals(zap.New(core))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{TraceEnabled: true})
	CaptureModeFromContext(c)
	if logs.Len() != 0 {
		t.Fatalf("legacy empty mode emitted warning: %v", logs.All())
	}

	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{TraceEnabled: true, TraceMode: "future"})
	CaptureModeFromContext(c)
	CaptureModeFromContext(c)
	if logs.Len() != 1 {
		t.Fatalf("warnings=%d want=1", logs.Len())
	}
	entry := logs.All()[0]
	if strings.Contains(entry.Message, "request") || strings.Contains(entry.Message, "token") && strings.Contains(entry.Message, "key") {
		t.Fatalf("warning leaks request or token key: %q", entry.Message)
	}
	if got := entry.ContextMap()["trace_mode"]; got != "future" {
		t.Fatalf("trace_mode field=%v", got)
	}

	now = base.Add(time.Minute)
	CaptureModeFromContext(c)
	if logs.Len() != 2 || logs.All()[1].ContextMap()["suppressed_count"] != uint64(1) {
		t.Fatalf("window summary=%v", logs.All())
	}
}

func populatedRecorder(t *testing.T, mode CaptureMode) *Recorder {
	t.Helper()
	r := NewRecorder(mode, 32)
	inbound := httptest.NewRequest(http.MethodPost, "/v1/chat", nil)
	inbound.Header.Set("Authorization", "Bearer inbound-secret")
	r.WithInbound(inbound, []byte("inbound-body-secret"))
	outbound := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/chat", nil)
	outbound.Header.Set("X-Api-Key", "channel-secret")
	r.WithOutbound(outbound, []byte("outbound-channel-secret"), &models.Channel{Key: "channel-secret"})
	r.WithUpstreamStatus(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Set-Cookie": {"secret-cookie"}, "X-Test": {"response"}}})
	r.upstreamBody.WriteString("upstream-channel-secret")
	r.clientBody.WriteString("client-channel-secret")
	return r
}

func TestRecorderEffectiveCaptureMode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        CaptureMode
		fail        bool
		wantVerbose bool
		wantBody    bool
	}{
		{name: "off success", mode: CaptureOff},
		{name: "off failure", mode: CaptureOff, fail: true, wantVerbose: true, wantBody: true},
		{name: "headers success", mode: CaptureHeaders, wantVerbose: true},
		{name: "headers failure", mode: CaptureHeaders, fail: true, wantVerbose: true, wantBody: true},
		{name: "full success", mode: CaptureFull, wantVerbose: true, wantBody: true},
		{name: "full failure", mode: CaptureFull, fail: true, wantVerbose: true, wantBody: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := populatedRecorder(t, tc.mode)
			if tc.fail {
				r.WithFail(StageUpstreamStatus, errors.New("failed"))
			}
			rec := r.Finalize()
			if rec.Verbose != tc.wantVerbose {
				t.Fatalf("Verbose=%v want=%v", rec.Verbose, tc.wantVerbose)
			}
			gotBody := rec.InboundBody != "" || rec.OutboundBody != "" || rec.UpstreamBody != "" || rec.ClientResponseBody != ""
			if gotBody != tc.wantBody {
				t.Fatalf("gotBody=%v want=%v", gotBody, tc.wantBody)
			}
			if tc.wantVerbose {
				if rec.InboundPath == "" || rec.OutboundPath == "" || rec.UpstreamStatus != http.StatusOK {
					t.Fatalf("verbose trace lost path/status: %+v", rec)
				}
				if rec.InboundHeaders.Get("Authorization") != "***" || rec.OutboundHeaders.Get("X-Api-Key") != "***" || rec.ResponseHeaders.Get("Set-Cookie") != "***" {
					t.Fatalf("headers were not masked: in=%v out=%v response=%v", rec.InboundHeaders, rec.OutboundHeaders, rec.ResponseHeaders)
				}
				if rec.ResponseHeaders.Get("X-Test") != "response" {
					t.Fatalf("non-sensitive response header was lost: %v", rec.ResponseHeaders)
				}
			}
		})
	}
}

func TestRecorderRetentionStatus(t *testing.T) {
	tests := []struct {
		name     string
		mode     CaptureMode
		fail     bool
		truncate bool
		want     models.TraceRetentionStatus
	}{
		{name: "disabled", mode: CaptureOff, want: models.TraceRetentionDisabled},
		{name: "headers only", mode: CaptureHeaders, want: models.TraceRetentionHeadersOnly},
		{name: "full", mode: CaptureFull, want: models.TraceRetentionFull},
		{name: "failure forces full", mode: CaptureOff, fail: true, want: models.TraceRetentionFull},
		{name: "body truncated", mode: CaptureFull, truncate: true, want: models.TraceRetentionBodyTruncated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := populatedRecorder(t, tc.mode)
			if tc.fail {
				r.WithFail(StageUpstreamStatus, errors.New("failed"))
			}
			if tc.truncate {
				r.maxBodySize = 4
			}
			record := r.Finalize()
			if got := r.RetentionStatus(record); got != tc.want {
				t.Fatalf("RetentionStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRecorderHeadersSuccessSkipsBodyMask(t *testing.T) {
	r := populatedRecorder(t, CaptureHeaders)
	r.bodyMask = func(string, []string) string { panic("body mask called") }
	rec := r.Finalize()
	if rec.FailStage == StageInternal {
		t.Fatal("headers success called body mask helper")
	}
	if rec.InboundBody != "" || rec.OutboundBody != "" || rec.UpstreamBody != "" || rec.ClientResponseBody != "" {
		t.Fatalf("headers success retained body fields: %+v", rec)
	}
}

func TestRecorderHeadersSuccessHandlesNilAndEmptyBodies(t *testing.T) {
	r := NewRecorder(CaptureHeaders, 0)
	r.WithInbound(httptest.NewRequest(http.MethodPost, "/v1/chat", nil), nil)
	r.WithOutbound(httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/chat", nil), []byte{}, nil)
	rec := r.Finalize()
	if !rec.HasTraceData() || rec.InboundBody != "" || rec.OutboundBody != "" || rec.UpstreamBody != "" || rec.ClientResponseBody != "" {
		t.Fatalf("unexpected headers-only record: %+v", rec)
	}
}

func TestNewRecorder_FieldsInitialized(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
	if r == nil {
		t.Fatal("NewRecorder returned nil")
	}
	if r.mode != CaptureFull {
		t.Errorf("mode = %v, want full", r.mode)
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
	r := NewRecorder(CaptureOff, 0)
	if r.mode != CaptureOff {
		t.Errorf("mode = %v, want off", r.mode)
	}
	// disabled 时 buffer 仍要初始化（always-on capture）
	if r.upstreamBody == nil || r.clientBody == nil {
		t.Errorf("buffers must be initialized even when disabled")
	}
}

// --- WithInbound ---

func TestRecorder_WithInbound_StoresPathHeadersBody(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureFull, 64*1024)
	r.WithStage(StageInboundDecode)
	if r.currStage != StageInboundDecode {
		t.Errorf("currStage not set")
	}
}

// --- WithFail ---

func TestRecorder_WithFail_OnlyFirstWins(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
	err1 := errors.New("first")
	err2 := errors.New("second")

	r.WithFail(StageOutboundEncode, err1)
	r.WithFail(StageInternal, err2)

	if r.failStage != StageOutboundEncode {
		t.Errorf("failStage = %q, want outbound_encode (first should win)", r.failStage)
	}
}

func TestRecorder_WithFail_NilErrIgnored(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
	r.WithFail(StageInternal, nil)
	if r.failStage != StageNone {
		t.Errorf("failStage should remain StageNone for nil err")
	}
}

// --- WithPassthrough ---

func TestRecorder_WithPassthrough_MarksFlag(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
	r.WithPassthrough()
	if !r.passthrough {
		t.Errorf("passthrough flag not set")
	}
}

// --- WithLegacyTrace ---

func TestRecorder_WithLegacyTrace_PopulatesAndMarksPassthrough(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
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

func TestRecorderLegacyCapturePreservesTruncationMarkerAndTail(t *testing.T) {
	tail := []byte(strings.Repeat("x", legacy.MaxTraceBodySize-24) + `","tail":"legacy-tail"}`)
	r := NewRecorder(CaptureFull, 64)
	r.WithInbound(httptest.NewRequest(http.MethodPost, "/in", nil), []byte(`{"model":"x"}`))
	r.WithLegacyTrace(&legacy.TraceData{
		ResponseBody:     tail,
		ResponseBodySeen: int64(len(tail) + 100),
	}, nil)

	got := r.Finalize().UpstreamBody
	require.True(t, strings.HasPrefix(got, truncatedPrefix))
	require.True(t, strings.HasSuffix(got, `","tail":"legacy-tail"}`))
	require.LessOrEqual(t, len(strings.TrimPrefix(got, truncatedPrefix)), 64)
}

// --- WrapUpstreamBody ---

func TestRecorder_WrapUpstreamBody_TeeAccumulates(t *testing.T) {
	payload := strings.Repeat("a", 1024)
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(payload)),
	}
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureFull, 2) // hard limit = 30 * 2 = 60 bytes
	resp.Body = r.WrapUpstreamBody(resp)

	got, _ := io.ReadAll(resp.Body)
	if len(got) != len(payload) {
		t.Errorf("下游读取被截断（绝对禁止）: got %d, want %d", len(got), len(payload))
	}
	if r.upstreamBody.Len() != 60 {
		t.Errorf("hard limit 没生效: buffer len = %d, want 60", r.upstreamBody.Len())
	}
	if got := r.upstreamBody.String(); got != payload[len(payload)-60:] {
		t.Fatalf("captured body = %q, want real tail", got)
	}
}

func TestTailAppenderKeepsLatestStreamingBytes(t *testing.T) {
	a := newTailAppender(5)
	for _, chunk := range []string{"ab", "cdef", "gh"} {
		n, err := a.Write([]byte(chunk))
		require.NoError(t, err)
		require.Equal(t, len(chunk), n)
	}
	require.Equal(t, "defgh", string(a.Bytes()))
	require.Equal(t, int64(8), a.TotalSeen())
	require.True(t, a.Truncated())
}

func TestTailAppenderSingleWriteAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		write string
		want  string
	}{
		{name: "zero writes", limit: 3},
		{name: "exact limit", limit: 3, write: "abc", want: "abc"},
		{name: "limit plus one", limit: 3, write: "abcd", want: "bcd"},
		{name: "fallback zero", limit: 0, write: strings.Repeat("a", defaultTraceMaxBodySize+1), want: strings.Repeat("a", defaultTraceMaxBodySize)},
		{name: "fallback negative", limit: -1, write: strings.Repeat("b", defaultTraceMaxBodySize+1), want: strings.Repeat("b", defaultTraceMaxBodySize)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTailAppender(tc.limit)
			if tc.write != "" {
				n, err := a.Write([]byte(tc.write))
				require.NoError(t, err)
				require.Equal(t, len(tc.write), n)
			}
			require.Equal(t, tc.want, string(a.Bytes()))
			require.Equal(t, int64(len(tc.write)), a.TotalSeen())
			require.Equal(t, len(tc.write) > a.Limit(), a.Truncated())
		})
	}
}

func TestRecorderFinalizeSanitizesHardBoundaryForEveryStage(t *testing.T) {
	const limit = 64
	const suffix = `","tail":"界-real-tail"}`
	hardLimit := limit * consts.TraceBufferHardLimitMultiple
	secret := strings.Repeat("S", hardLimit+100)
	stageBody := secret + suffix
	r := NewRecorder(CaptureFull, limit)
	in := httptest.NewRequest(http.MethodPost, "/in", nil)
	out := httptest.NewRequest(http.MethodPost, "https://up.example/out", nil)
	ch := &models.Channel{ChannelCore: models.ChannelCore{BaseURL: "https://up.example"}, Key: secret}
	r.WithInbound(in, []byte(stageBody)).WithOutbound(out, []byte(stageBody), ch)
	cut := len(stageBody) - 7
	r.SetUpstreamBody([]byte(stageBody[:cut]))
	r.SetUpstreamBody([]byte(stageBody[cut:]))
	r.appendClientBody([]byte(stageBody[:cut]))
	r.appendClientBody([]byte(stageBody[cut:]))

	rec := r.Finalize()
	for name, body := range map[string]string{
		"inbound": rec.InboundBody, "outbound": rec.OutboundBody,
		"upstream": rec.UpstreamBody, "client": rec.ClientResponseBody,
	} {
		require.True(t, strings.HasPrefix(body, truncatedPrefix), name)
		require.True(t, strings.HasSuffix(body, `","tail":"界-real-tail"}`), name)
		require.NotContains(t, body, secret, name)
		require.NotContains(t, body, "S", name)
		require.LessOrEqual(t, len(strings.TrimPrefix(body, truncatedPrefix)), limit, name)
		require.True(t, utf8.ValidString(body), name)
	}
	require.Greater(t, r.inboundSeen, int64(hardLimit))
	require.Greater(t, r.outboundSeen, int64(hardLimit))
	require.Greater(t, r.upstreamBody.TotalSeen(), int64(hardLimit))
	require.Greater(t, r.clientBody.TotalSeen(), int64(hardLimit))
}

func TestRecorderStreamMasksUnknownTokenAcrossHardBoundaryForEveryStage(t *testing.T) {
	const limit = 64
	hardLimit := limit * consts.TraceBufferHardLimitMultiple
	for _, tc := range []struct {
		name   string
		prefix string
		token  string
	}{
		{name: "bearer braces", prefix: `{"authorization":"Bearer `, token: strings.Repeat("AAA{secret-tail}[]{}", hardLimit)},
		{name: "key brackets", prefix: `{"authorization":"Key `, token: strings.Repeat("KEY[]{secret-tail}", hardLimit)},
		{name: "sk alphanumeric", prefix: `{"key":"sk-`, token: strings.Repeat("sksecrettail", hardLimit)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.token
			body := tc.prefix + token + `","tail":"real-tail"}`
			r := NewRecorder(CaptureFull, limit)
			r.WithInbound(httptest.NewRequest(http.MethodPost, "/in", nil), []byte(body))
			r.WithOutbound(httptest.NewRequest(http.MethodPost, "https://up/out", nil), []byte(body), nil)
			cut := len(body) - 11
			r.SetUpstreamBody([]byte(body[:cut]))
			r.SetUpstreamBody([]byte(body[cut:]))
			r.appendClientBody([]byte(body[:cut]))
			r.appendClientBody([]byte(body[cut:]))

			record := r.Finalize()
			for stage, got := range map[string]string{
				"inbound": record.InboundBody, "outbound": record.OutboundBody,
				"upstream": record.UpstreamBody, "client": record.ClientResponseBody,
			} {
				require.True(t, strings.HasPrefix(got, truncatedPrefix), stage)
				require.True(t, strings.HasSuffix(got, `","tail":"real-tail"}`), stage)
				require.NotContains(t, got, "secret-tail", stage)
				require.NotContains(t, got, "secrettail", stage)
			}
		})
	}
}

func TestRecorderKeepsUnstructuredPlainTextTailForEveryStage(t *testing.T) {
	const limit = 64
	hardLimit := limit * consts.TraceBufferHardLimitMultiple
	body := strings.Repeat("a", hardLimit+100) + "REAL-TAIL"
	r := NewRecorder(CaptureFull, limit)
	r.WithInbound(httptest.NewRequest(http.MethodPost, "/in", nil), []byte(body))
	r.WithOutbound(httptest.NewRequest(http.MethodPost, "https://up/out", nil), []byte(body), nil)
	cut := len(body) / 2
	r.SetUpstreamBody([]byte(body[:cut]))
	r.SetUpstreamBody([]byte(body[cut:]))
	r.appendClientBody([]byte(body[:cut]))
	r.appendClientBody([]byte(body[cut:]))

	record := r.Finalize()
	for stage, got := range map[string]string{
		"inbound": record.InboundBody, "outbound": record.OutboundBody,
		"upstream": record.UpstreamBody, "client": record.ClientResponseBody,
	} {
		require.True(t, strings.HasPrefix(got, truncatedPrefix), stage)
		require.True(t, strings.HasSuffix(got, "REAL-TAIL"), stage)
		require.NotEqual(t, truncatedPrefix, got, stage)
	}
}

func TestRecorderPreservesFalseSensitivePrefixesAcrossEveryChunkSplit(t *testing.T) {
	const body = `Bear BearerX Keynote sk-short ordinary`
	for split := 0; split <= len(body); split++ {
		r := NewRecorder(CaptureFull, 1024)
		r.SetUpstreamBody([]byte(body[:split]))
		r.SetUpstreamBody([]byte(body[split:]))
		require.Equal(t, body, r.Finalize().UpstreamBody, "split=%d", split)
	}
}

func TestRecorderBoundsRequestStageCopiesAtCaptureTime(t *testing.T) {
	const limit = 2
	body := []byte(strings.Repeat("h", 80) + "request-tail")
	r := NewRecorder(CaptureFull, limit)
	r.WithInbound(httptest.NewRequest(http.MethodPost, "/in", nil), body)
	r.WithOutbound(httptest.NewRequest(http.MethodPost, "https://up/out", nil), body, nil)

	require.Len(t, r.inboundBody, limit*consts.TraceBufferHardLimitMultiple)
	require.Len(t, r.outboundBody, limit*consts.TraceBufferHardLimitMultiple)
	require.Equal(t, int64(len(body)), r.inboundSeen)
	require.Equal(t, int64(len(body)), r.outboundSeen)
	require.True(t, strings.HasSuffix(string(r.inboundBody), "request-tail"))
	require.True(t, strings.HasSuffix(string(r.outboundBody), "request-tail"))
}

// --- UpstreamBodyBytes ---

func TestRecorder_UpstreamBodyBytes_ReturnsBufferContent(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
	r.upstreamBody.WriteString("provider usage payload")
	got := r.UpstreamBodyBytes()
	if string(got) != "provider usage payload" {
		t.Errorf("got %q", string(got))
	}
}

func TestRecorder_UpstreamBodyBytes_EvenWhenDisabled(t *testing.T) {
	r := NewRecorder(CaptureOff, 64*1024)
	r.upstreamBody.WriteString("disabled but captured")
	got := r.UpstreamBodyBytes()
	if string(got) != "disabled but captured" {
		t.Errorf("disabled recorder 也必须能读 buffer（always-on capture）")
	}
}

// --- ResetAttempt ---

func TestRecorder_ResetAttempt_ClearsAttemptScopedState(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureOff, 64*1024)
	r.WithInbound(httptest.NewRequest("POST", "/", nil), []byte("body"))
	r.WithStage(StageInboundDecode)
	time.Sleep(2 * time.Millisecond)

	rec := r.Finalize()
	if rec == nil {
		t.Fatal("disabled+no-fail Finalize must return non-nil record")
	}
	if rec.HasTraceData() {
		t.Errorf("disabled+no-fail record should have HasTraceData()==false (InboundPath empty)")
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
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureFull, 64*1024)
	r.WithPassthrough()
	r.upstreamBody.WriteString(`{"upstream":"data"}`)
	// clientBody 始终空（passthrough 走 io.Copy）
	rec := r.Finalize()

	if rec.ClientResponseBody != `{"upstream":"data"}` {
		t.Errorf("passthrough 时 client_response_body 应镜像 upstream，got %q", rec.ClientResponseBody)
	}
}

func TestRecorder_Finalize_TruncatesBodiesToMaxBodySize(t *testing.T) {
	r := NewRecorder(CaptureFull, 10) // maxBodySize 故意很小
	r.WithInbound(httptest.NewRequest("POST", "/", nil), []byte(strings.Repeat("a", 50)))
	rec := r.Finalize()
	// truncateBodyWithLimit 会追加 "...(truncated)"，所以实际长度 = 10 + len("...(truncated)") = 24
	// 只验证截断了原始 50 byte 内容，不超过 10 + suffix
	if len(rec.InboundBody) >= 50 {
		t.Errorf("inbound body 未截断：%d", len(rec.InboundBody))
	}
}

func TestRecorder_Finalize_PanicSafe(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
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

func TestTraceRecord_HasTraceData_EmptyInboundPath(t *testing.T) {
	rec := &TraceRecord{InboundPath: "", Verbose: true}
	if rec.HasTraceData() {
		t.Errorf("HasTraceData() should be false when InboundPath is empty")
	}
}

func TestTraceRecord_HasTraceData_NonVerbosePath(t *testing.T) {
	rec := &TraceRecord{InboundPath: "/v1/chat/completions"}
	if rec.HasTraceData() {
		t.Errorf("HasTraceData() should be false for a non-verbose record")
	}
}

func TestTraceRecord_HasTraceData_NonEmptyInboundPath(t *testing.T) {
	rec := &TraceRecord{InboundPath: "/v1/chat/completions", Verbose: true}
	if !rec.HasTraceData() {
		t.Errorf("HasTraceData() should be true when InboundPath is set")
	}
}

func TestTraceRecord_HasTraceData_NilReceiver(t *testing.T) {
	var rec *TraceRecord
	if rec.HasTraceData() {
		t.Errorf("nil receiver HasTraceData() should be false")
	}
}

func TestRecorder_Finalize_DisabledWithFail_Verbose(t *testing.T) {
	r := NewRecorder(CaptureOff, 64*1024)
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(""))
	r.WithInbound(req, []byte(`{"in":1}`))
	r.WithFail(StageInboundDecode, errors.New("bad body"))

	rec := r.Finalize()
	if rec == nil {
		t.Fatal("disabled+fail Finalize must return non-nil")
	}
	if !rec.HasTraceData() {
		t.Errorf("disabled+fail should be verbose (HasTraceData()==true)")
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
	r := NewRecorder(CaptureFull, 64*1024)
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(""))
	r.WithInbound(req, []byte(`{"in":2}`))
	r.WithStage(StageUpstreamDispatch)
	r.WithFail(StageUpstreamDispatch, errors.New("net err"))

	rec := r.Finalize()
	if !rec.HasTraceData() {
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
	if rec.HasTraceData() {
		t.Errorf("nil-receiver record should have HasTraceData()==false")
	}
	if rec.Timings == nil {
		t.Errorf("nil-receiver record should still have empty Timings map (not nil)")
	}
}

func TestRecorder_Finalize_DoubleCall_NoPanic(t *testing.T) {
	r := NewRecorder(CaptureFull, 64*1024)
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
	r := NewRecorder(CaptureOff, 64*1024) // disabled，验证 timings 仍累积
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

func TestRecorder_StageHookFires(t *testing.T) {
	rec := NewRecorder(CaptureOff, 0)
	var got []string
	rec.SetStageHook(func(s Stage) { got = append(got, string(s)) })

	rec.WithStage(StageUpstreamDispatch)
	rec.WithStage(StageUpstreamDecode)

	if len(got) != 2 || got[0] != "upstream_dispatch" || got[1] != "upstream_decode" {
		t.Fatalf("hook not fired correctly: %v", got)
	}
}

func TestRecorder_SnapshotAttemptAccumulates(t *testing.T) {
	r := NewRecorder(CaptureFull, 0) // enabled=true
	r.WithFail(StageUpstreamStatus, errors.New("status err"))
	r.SnapshotAttempt()
	r.ResetAttempt()
	r.WithFail(StageUpstreamDispatch, errors.New("dispatch err"))
	r.SnapshotAttempt()

	got := r.Attempts()
	if len(got) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(got))
	}
	if got[0].FailStage != StageUpstreamStatus {
		t.Fatalf("snapshot[0] FailStage = %q, want %q", got[0].FailStage, StageUpstreamStatus)
	}
	if got[1].FailStage != StageUpstreamDispatch {
		t.Fatalf("snapshot[1] FailStage = %q, want %q", got[1].FailStage, StageUpstreamDispatch)
	}
	// 两条快照必须独立：修改 got[0] 不影响 got[1]。
	if got[0] == got[1] {
		t.Fatalf("snapshots must be independent records, got same pointer")
	}
}

func TestRecorderRefreshLastAttemptClientResponse(t *testing.T) {
	t.Run("refreshes only the final upstream status attempt", func(t *testing.T) {
		r := populatedRecorder(t, CaptureHeaders)
		r.WithFail(StageUpstreamStatus, errors.New("first failed"))
		r.SnapshotAttempt()
		firstClientBody := r.Attempts()[0].ClientResponseBody

		r.ResetAttempt()
		r.WithFail(StageUpstreamStatus, errors.New("final failed"))
		r.SnapshotAttempt()
		r.clientBody.WriteString("final client error")
		r.RefreshLastAttemptClientResponse()

		attempts := r.Attempts()
		require.Len(t, attempts, 2)
		require.Equal(t, firstClientBody, attempts[0].ClientResponseBody, "historical fallback trace changed")
		require.Equal(t, "final client error", attempts[1].ClientResponseBody)
	})

	t.Run("non upstream status attempt stays frozen", func(t *testing.T) {
		r := NewRecorder(CaptureFull, 1024)
		r.WithFail(StageOutboundEncode, errors.New("encode failed"))
		r.SnapshotAttempt()
		r.clientBody.WriteString("outer error")

		r.RefreshLastAttemptClientResponse()

		require.Empty(t, r.Attempts()[0].ClientResponseBody)
	})

	t.Run("empty attempt list is a no-op", func(t *testing.T) {
		r := NewRecorder(CaptureFull, 1024)
		r.clientBody.WriteString("outer error")

		require.NotPanics(t, r.RefreshLastAttemptClientResponse)
		require.Empty(t, r.Attempts())
	})

	t.Run("body masking panic preserves the snapshot", func(t *testing.T) {
		r := populatedRecorder(t, CaptureHeaders)
		r.WithFail(StageUpstreamStatus, errors.New("provider failed"))
		r.SnapshotAttempt()
		before := r.Attempts()[0]
		r.clientBody.Reset()
		r.clientBody.WriteString("final client error")
		r.bodyMask = func(string, []string) string { panic("mask failed") }

		published := false
		require.NotPanics(t, func() {
			r.RefreshLastAttemptClientResponse()
			published = true
		})

		after := r.Attempts()[0]
		require.True(t, published, "refresh panic would prevent the caller from publishing")
		require.Equal(t, before.ClientResponseBody, after.ClientResponseBody)
		require.Equal(t, before.InboundBody, after.InboundBody)
	})
}

func TestRecorderAppendAttemptDefensivelyCopiesRecord(t *testing.T) {
	original := &TraceRecord{
		InboundPath:        "/v1/chat/completions",
		InboundHeaders:     http.Header{"X-Inbound": {"one"}},
		InboundBody:        string([]byte("inbound")),
		OutboundHeaders:    http.Header{"X-Outbound": {"two"}},
		OutboundBody:       string([]byte("outbound")),
		ResponseHeaders:    http.Header{"X-Response": {"three"}},
		UpstreamBody:       string([]byte("response")),
		ClientResponseBody: string([]byte("client")),
		FailStage:          StageUpstreamStatus,
		Timings:            map[Stage]time.Duration{StageInboundDecode: time.Millisecond},
		Verbose:            true,
	}
	r := NewRecorder(CaptureOff, 0)
	r.AppendAttempt(original)

	original.InboundHeaders["X-Inbound"][0] = "mutated"
	original.OutboundHeaders.Set("X-Outbound", "mutated")
	original.ResponseHeaders.Set("X-Response", "mutated")
	original.Timings[StageInboundDecode] = 99 * time.Millisecond
	original.InboundBody = "mutated"

	got := r.Attempts()
	require.Len(t, got, 1)
	require.Equal(t, "one", got[0].InboundHeaders.Get("X-Inbound"))
	require.Equal(t, "two", got[0].OutboundHeaders.Get("X-Outbound"))
	require.Equal(t, "three", got[0].ResponseHeaders.Get("X-Response"))
	require.Equal(t, time.Millisecond, got[0].Timings[StageInboundDecode])
	require.Equal(t, "inbound", got[0].InboundBody)

	got[0].InboundHeaders.Set("X-Inbound", "returned-mutation")
	got[0].Timings[StageInboundDecode] = 100 * time.Millisecond
	again := r.Attempts()
	require.Equal(t, "one", again[0].InboundHeaders.Get("X-Inbound"))
	require.Equal(t, time.Millisecond, again[0].Timings[StageInboundDecode])
}

func TestRecorderAppendAttemptNilPreservesAttemptIndex(t *testing.T) {
	r := NewRecorder(CaptureOff, 0)
	r.AppendAttempt(nil)

	got := r.Attempts()
	require.Len(t, got, 1)
	require.NotNil(t, got[0])
	require.False(t, got[0].Verbose)
	require.False(t, r.LastSnapshotVerbose())
}

func TestRecorderAppendFailedRemoteAttemptMergesTargetTraceIntoLocalFull(t *testing.T) {
	r := populatedRecorder(t, CaptureHeaders)
	remote := &TraceRecord{
		InboundPath:     "/target/inbound",
		InboundHeaders:  http.Header{"X-Target-In": {"kept"}},
		OutboundPath:    "/target/outbound",
		OutboundHeaders: http.Header{"X-Target-Out": {"kept"}},
		OutboundBody:    "target-outbound-body",
		ResponseHeaders: http.Header{"X-Target-Response": {"kept"}},
		UpstreamStatus:  http.StatusBadGateway,
		UpstreamBody:    "target-upstream-body",
		FailStage:       StageUpstreamStatus,
		Timings:         map[Stage]time.Duration{StageUpstreamDispatch: 3 * time.Millisecond},
		Verbose:         true,
	}

	r.AppendFailedRemoteAttempt(remote, errors.New("source transport failed"))

	attempts := r.Attempts()
	require.Len(t, attempts, 1)
	got := attempts[0]
	require.Equal(t, "/target/inbound", got.InboundPath)
	require.Equal(t, "kept", got.InboundHeaders.Get("X-Target-In"))
	require.Equal(t, "/target/outbound", got.OutboundPath)
	require.Equal(t, "kept", got.OutboundHeaders.Get("X-Target-Out"))
	require.Equal(t, "kept", got.ResponseHeaders.Get("X-Target-Response"))
	require.Equal(t, http.StatusBadGateway, got.UpstreamStatus)
	require.Equal(t, 3*time.Millisecond, got.Timings[StageUpstreamDispatch])
	require.Equal(t, StageUpstreamStatus, got.FailStage)
	require.NotEmpty(t, got.InboundBody, "missing target inbound body should be recovered from source")
	require.Equal(t, "target-outbound-body", got.OutboundBody)
	require.Equal(t, "target-upstream-body", got.UpstreamBody)
	require.NotEmpty(t, got.ClientResponseBody, "missing target client body should be recovered from source")
}

func TestRecorderBuildRemoteFailureBodyFallback(t *testing.T) {
	t.Run("headers success", func(t *testing.T) {
		r := populatedRecorder(t, CaptureHeaders)
		got := r.BuildRemoteFailureBodyFallback()
		require.NotNil(t, got)
		require.Equal(t, "inbound-body-secret", got.InboundBody)
		require.Equal(t, "outbound-"+strings.Repeat("*", len("channel-secret")), got.OutboundBody)
		require.Equal(t, "upstream-"+strings.Repeat("*", len("channel-secret")), got.UpstreamBody)
		require.Equal(t, "client-"+strings.Repeat("*", len("channel-secret")), got.ClientResponseBody)
	})

	t.Run("oversize remains masked and bounded", func(t *testing.T) {
		const limit = 12
		r := NewRecorder(CaptureHeaders, limit)
		outbound := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1", nil)
		r.WithOutbound(outbound, []byte("xxchannel-secret-suffix"), &models.Channel{Key: "channel-secret"})
		r.SetUpstreamBody([]byte("xxchannel-secret-suffix"))
		r.WithPassthrough()

		got := r.BuildRemoteFailureBodyFallback()
		require.NotNil(t, got)
		for _, body := range []string{got.OutboundBody, got.UpstreamBody, got.ClientResponseBody} {
			require.NotContains(t, body, "channel-secret")
			require.Len(t, body, limit+len("...(truncated)"))
			// behavior change: remote fallback uses the same real-tail contract.
			require.Equal(t, "...(truncated)"+strings.Repeat("*", 5)+"-suffix", body)
		}
	})

	t.Run("non-header modes", func(t *testing.T) {
		require.Nil(t, populatedRecorder(t, CaptureOff).BuildRemoteFailureBodyFallback())
		require.Nil(t, populatedRecorder(t, CaptureFull).BuildRemoteFailureBodyFallback())
	})

	t.Run("empty bodies retain explicit marker", func(t *testing.T) {
		r := NewRecorder(CaptureHeaders, 32)
		got := r.BuildRemoteFailureBodyFallback()
		require.NotNil(t, got)
		require.Empty(t, got.InboundBody)
		require.Empty(t, got.OutboundBody)
		require.Empty(t, got.UpstreamBody)
		require.Empty(t, got.ClientResponseBody)
	})

	t.Run("failed and nil", func(t *testing.T) {
		r := populatedRecorder(t, CaptureHeaders)
		r.WithFail(StageUpstreamStatus, errors.New("failed"))
		require.Nil(t, r.BuildRemoteFailureBodyFallback())
		var nilRecorder *Recorder
		require.Nil(t, nilRecorder.BuildRemoteFailureBodyFallback())
	})
}

func TestRecorderFullTraceKeepsCompactMaskFormat(t *testing.T) {
	got := populatedRecorder(t, CaptureFull).Finalize()
	require.Equal(t, "outbound-***", got.OutboundBody)
	require.Equal(t, "upstream-***", got.UpstreamBody)
	require.Equal(t, "client-***", got.ClientResponseBody)
}

func TestRecorderAppendFailedRemoteAttemptUsesSourceFailureForSuccessfulTargetTrace(t *testing.T) {
	r := populatedRecorder(t, CaptureHeaders)
	remote := &TraceRecord{InboundPath: "/target", FailStage: StageNone, Verbose: true}

	r.AppendFailedRemoteAttempt(remote, errors.New("source protocol failed"))

	attempts := r.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, StageInternal, attempts[0].FailStage)
	require.NotEmpty(t, attempts[0].InboundBody)
}

func TestRecorder_LastSnapshotVerbose(t *testing.T) {
	var nilRec *Recorder
	if nilRec.LastSnapshotVerbose() {
		t.Fatal("nil 接收者应返回 false")
	}

	r := NewRecorder(CaptureOff, 0) // 关 trace
	if r.LastSnapshotVerbose() {
		t.Fatal("无快照时应返回 false")
	}

	r.SnapshotAttempt() // 关 trace + 无失败 → 非 verbose
	if r.LastSnapshotVerbose() {
		t.Fatal("非 verbose 快照应返回 false")
	}

	r.WithFail(StageUpstreamStatus, errors.New("boom"))
	r.SnapshotAttempt() // 有失败 → verbose
	if !r.LastSnapshotVerbose() {
		t.Fatal("verbose 快照应返回 true")
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
