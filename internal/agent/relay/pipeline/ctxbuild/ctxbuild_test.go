package ctxbuild

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	bodypkg "github.com/VaalaCat/ai-gateway/internal/agent/body"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

func newGinCtxForTest(setup func(c *gin.Context)) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(""))
	if setup != nil {
		setup(c)
	}
	return c
}

func TestComputeRequestIDFromHeader(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request.Header.Set(consts.HeaderXRequestID, "abc-123")
	})
	if got := computeRequestID(c); got != "abc-123" {
		t.Errorf("got %q want abc-123", got)
	}
}

func TestComputeRequestIDDefaultGenerated(t *testing.T) {
	c := newGinCtxForTest(nil)
	got := computeRequestID(c)
	if !strings.HasPrefix(got, "req-") {
		t.Errorf("default RequestID should start with req-, got %q", got)
	}
}

func TestComputeRequestIDBoundaryEmptyHeader(t *testing.T) {
	// boundary: header set but empty string — should fall back to default
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request.Header.Set(consts.HeaderXRequestID, "")
	})
	got := computeRequestID(c)
	if !strings.HasPrefix(got, "req-") {
		t.Errorf("empty header should fall back to default, got %q", got)
	}
}

func TestComputeRequestIDCanonicalizesAndRewritesRequestHeader(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request.Header.Set(consts.HeaderXRequestID, " \trequest-a\r\n")
	})
	require.Equal(t, "request-a", computeRequestID(c))
	require.Equal(t, "request-a", c.Request.Header.Get(consts.HeaderXRequestID))

	long := strings.Repeat("x", 129)
	c.Request.Header.Set(consts.HeaderXRequestID, long)
	require.Equal(t, "req-0ec9eb33e74510bcdd1f2ea55206e82f", computeRequestID(c))
	require.Equal(t, "req-0ec9eb33e74510bcdd1f2ea55206e82f", c.Request.Header.Get(consts.HeaderXRequestID))
}

func TestBuildParsesHardAgentIDAndTagSelectors(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want app.AgentSelector
	}{
		{name: "agent id", key: consts.HeaderXAgentID, want: app.AgentSelector{AgentID: "agent-a"}},
		{name: "agent tag", key: consts.HeaderXAgentTag, want: app.AgentSelector{AgentTag: "gpu"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newGinCtxForTest(func(c *gin.Context) {
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
				c.Request.Header.Set(tt.key, map[bool]string{true: "agent-a", false: "gpu"}[tt.key == consts.HeaderXAgentID])
			})
			rctx := newTestRelayCtx(t, c)
			require.NoError(t, Build(rctx))
			require.Equal(t, tt.want, rctx.Input.HardSelector)
		})
	}
}

func TestBuildRejectsBothHardSelectorsAsBadRequest(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
		c.Request.Header.Set(consts.HeaderXAgentID, "agent-a")
		c.Request.Header.Set(consts.HeaderXAgentTag, "gpu")
	})
	rctx := newTestRelayCtx(t, c)
	err := Build(rctx)
	require.ErrorIs(t, err, state.ErrInvalidAgentSelector)
	rctx.State.Err = err
	status, _ := state.StatusFromState(rctx)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestTraceEnabledTrue(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{TraceEnabled: true})
	})
	if !trace.Enabled(c) {
		t.Error("should be true")
	}
}

func TestTraceEnabledFalseExplicit(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{TraceEnabled: false})
	})
	if trace.Enabled(c) {
		t.Error("should be false")
	}
}

func TestTraceEnabledMissingUserInfo(t *testing.T) {
	c := newGinCtxForTest(nil) // no UserInfo set
	if trace.Enabled(c) {
		t.Error("should be false when UserInfo missing")
	}
}

func TestTraceEnabledNilUserInfo(t *testing.T) {
	// boundary: key set but value is nil
	c := newGinCtxForTest(func(c *gin.Context) {
		var nilUI *app.UserInfo
		c.Set(consts.CtxKeyUserInfo, nilUI)
	})
	if trace.Enabled(c) {
		t.Error("should be false for nil UserInfo")
	}
}

func TestTraceEnabledWrongType(t *testing.T) {
	// boundary: key set but wrong type
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, "not-a-userinfo")
	})
	if trace.Enabled(c) {
		t.Error("should be false for wrong type")
	}
}

func newTestRelayCtx(t *testing.T, c *gin.Context) *state.RelayContext {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := bodypkg.NewStore(bodypkg.StoreOptions{Directory: dir, OwnerID: "ctxbuild-test"})
	if err != nil {
		t.Fatal(err)
	}
	spy := &captureSpy{delegate: store, directory: dir}
	cache := &settingsCache{settings: settings.AgentSettings{
		BodyMemoryThresholdBytes: bodypkg.DefaultMemoryThreshold,
		BodyHardLimitBytes:       bodypkg.DefaultHardLimit,
	}}
	rctx := &state.RelayContext{
		Context: c,
		State:   &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
		Agent:   &ctxbuildAgent{cache: cache, bodyStore: spy},
	}
	t.Cleanup(func() {
		if c.Request != nil && c.Request.Body != nil {
			_ = c.Request.Body.Close()
		}
		if rctx.Resources != nil {
			_ = rctx.Resources.Close()
		}
		_ = store.Close(context.Background())
	})
	return rctx
}

type ctxbuildAgent struct {
	app.AgentApplication
	cache     app.AgentCache
	bodyStore app.BodyStore
}

func (a *ctxbuildAgent) GetCache() app.AgentCache    { return a.cache }
func (a *ctxbuildAgent) GetBodyStore() app.BodyStore { return a.bodyStore }

type settingsCache struct {
	app.AgentCache
	settings settings.AgentSettings
	calls    atomic.Int32
}

func (c *settingsCache) Settings() settings.AgentSettings {
	c.calls.Add(1)
	return c.settings
}

type captureSpy struct {
	delegate  app.BodyStore
	directory string
	count     atomic.Int32
	mu        sync.Mutex
	limits    []app.BodyLimits
}

func (s *captureSpy) Capture(ctx context.Context, src io.Reader, limits app.BodyLimits) (app.ReplayBody, error) {
	s.count.Add(1)
	s.mu.Lock()
	s.limits = append(s.limits, limits)
	s.mu.Unlock()
	return s.delegate.Capture(ctx, src, limits)
}

type closeSpy struct {
	io.Reader
	closes atomic.Int32
}

func (r *closeSpy) Close() error {
	r.closes.Add(1)
	return nil
}

type ctxbuildBlockingBody struct {
	started    chan struct{}
	closed     chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
	closeCalls atomic.Int32
}

func (r *ctxbuildBlockingBody) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, errors.New("closed")
}

func (r *ctxbuildBlockingBody) Close() error {
	r.closeCalls.Add(1)
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

// errReader 是一个始终返回错误的 io.ReadCloser，用于模拟 body 读取失败。
// 与 internal/agent/relay/trace_integration_test.go 同名 helper 等价；
// 包级隔离后单独定义，避免跨包共享 test 辅助。
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, errors.New("read failure") }
func (errReader) Close() error               { return nil }

func TestBuildSuccess(t *testing.T) {
	body := []byte(`{"model":"gpt-4","stream":true}`)
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 2})
	})
	rctx := newTestRelayCtx(t, c)
	if err := Build(rctx); err != nil {
		t.Fatal(err)
	}
	if rctx.Input.Model != "gpt-4" {
		t.Errorf("Model = %q", rctx.Input.Model)
	}
	if !rctx.Input.IsStream {
		t.Error("IsStream")
	}
	if rctx.Input.UserInfo == nil || rctx.Input.UserInfo.UserID != 1 {
		t.Error("UserInfo")
	}
	if rctx.Input.RequestID == "" {
		t.Error("RequestID empty")
	}
	if rctx.Input.StartTime.IsZero() {
		t.Error("StartTime zero")
	}
	if len(rctx.Input.Body) == 0 {
		t.Error("Body empty")
	}
}

func TestBuildSuccessNoStream(t *testing.T) {
	body := []byte(`{"model":"gpt-4"}`)
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	})
	rctx := newTestRelayCtx(t, c)
	if err := Build(rctx); err != nil {
		t.Fatal(err)
	}
	if rctx.Input.IsStream {
		t.Error("IsStream should be false")
	}
}

func TestBuildReadBodyFail(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", nil)
		c.Request.Body = io.NopCloser(errReader{})
	})
	rctx := newTestRelayCtx(t, c)
	err := Build(rctx)
	if !errors.Is(err, bodypkg.ErrBodyStoreFailed) {
		t.Errorf("got %v want errors.Is ErrBodyStoreFailed", err)
	}
	if err == nil || err.Error() != "body_store_failed" || strings.Contains(err.Error(), "read failure") {
		t.Errorf("body store failure must be sanitized, got %v", err)
	}
}

func TestBuildCanceledCaptureClosesOriginalBodyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inbound := &closeSpy{Reader: strings.NewReader(`{"model":"gpt-4o"}`)}
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		c.Request.Body = inbound
	})
	err := Build(newTestRelayCtx(t, c))
	if !errors.Is(err, bodypkg.ErrBodyStoreFailed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Build error = %v, want canceled body store failure", err)
	}
	if inbound.closes.Load() != 1 {
		t.Fatalf("original body closes = %d, want 1", inbound.closes.Load())
	}
}

func TestBuildCancelUnblocksAndClosesOriginalBodyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	inbound := &ctxbuildBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		c.Request.Body = inbound
	})
	rctx := newTestRelayCtx(t, c)
	result := make(chan error, 1)
	go func() { result <- Build(rctx) }()
	<-inbound.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, bodypkg.ErrBodyStoreFailed) || !errors.Is(err, context.Canceled) {
			t.Fatalf("Build error = %v, want canceled body store failure", err)
		}
	case <-time.After(time.Second):
		_ = inbound.Close()
		<-result
		t.Fatal("Build did not unblock after request cancellation")
	}
	if inbound.closeCalls.Load() != 1 {
		t.Fatalf("original body Close calls = %d, want 1", inbound.closeCalls.Load())
	}
}

func TestBuildInvalidJSON(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader("not json"))
	})
	rctx := newTestRelayCtx(t, c)
	err := Build(rctx)
	if !errors.Is(err, state.ErrInvalidBody) {
		t.Errorf("got %v want errors.Is ErrInvalidBody", err)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("error should embed json cause containing 'invalid character', got %v", err)
	}
}

// TestBuildEmptyBody 复刻线上 req-1778766254284739757 现象：
// client 在 body 上传完成前断开，io.ReadAll 返回 ([]byte{}, nil) 不报错，
// 走到 json.Unmarshal 失败分支，cause 应为 "unexpected end of JSON input"。
func TestBuildEmptyBody(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader(""))
	})
	rctx := newTestRelayCtx(t, c)
	err := Build(rctx)
	if !errors.Is(err, state.ErrInvalidBody) {
		t.Errorf("got %v want errors.Is ErrInvalidBody", err)
	}
	if err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Errorf("empty body should yield cause 'unexpected end of JSON input', got %v", err)
	}
}

func TestBuildModelRequired(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader(`{}`))
	})
	rctx := newTestRelayCtx(t, c)
	if err := Build(rctx); err != state.ErrModelRequired {
		t.Errorf("got %v want state.ErrModelRequired", err)
	}
}

func TestBuildInvalidForcedChannelID(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader(`{"model":"x"}`))
		c.Request.Header.Set(consts.HeaderXChannelID, "not-a-number")
	})
	rctx := newTestRelayCtx(t, c)
	if err := Build(rctx); err != state.ErrInvalidForcedChannelID {
		t.Errorf("got %v want state.ErrInvalidForcedChannelID", err)
	}
}

func TestBuildForcedChannelIDParsed(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader(`{"model":"x"}`))
		c.Request.Header.Set(consts.HeaderXChannelID, "42")
	})
	rctx := newTestRelayCtx(t, c)
	if err := Build(rctx); err != nil {
		t.Fatal(err)
	}
	if rctx.Input.ForcedChannelID != 42 {
		t.Errorf("ForcedChannelID = %d want 42", rctx.Input.ForcedChannelID)
	}
}

func TestBuildBodyRestoredForDownstream(t *testing.T) {
	// boundary: after Build, c.Request.Body should still readable (was restored to NopCloser)
	body := []byte(`{"model":"x"}`)
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", bytes.NewReader(body))
	})
	rctx := newTestRelayCtx(t, c)
	if err := Build(rctx); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body not restored: got %q want %q", got, body)
	}
}

func TestBuildCapturesExactlyOnceAndInstallsReplayableRequest(t *testing.T) {
	want := []byte(`{"model":"gpt-4o","stream":true}`)
	inbound := &closeSpy{Reader: bytes.NewReader(want)}
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Request.Body = inbound
	})
	rctx := newTestRelayCtx(t, c)
	spy := rctx.Agent.GetBodyStore().(*captureSpy)
	cache := rctx.Agent.GetCache().(*settingsCache)
	if err := Build(rctx); err != nil {
		t.Fatal(err)
	}
	if spy.count.Load() != 1 {
		t.Fatalf("Capture count = %d, want 1", spy.count.Load())
	}
	if cache.calls.Load() != 1 {
		t.Fatalf("Settings snapshot reads = %d, want 1", cache.calls.Load())
	}
	if inbound.closes.Load() != 1 {
		t.Fatalf("original inbound body closes = %d, want 1", inbound.closes.Load())
	}
	if rctx.Resources == nil || rctx.Resources.Body() == nil {
		t.Fatal("Build did not attach request resources")
	}
	if c.Request.ContentLength != int64(len(want)) {
		t.Fatalf("ContentLength = %d, want %d", c.Request.ContentLength, len(want))
	}
	first, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatal(err)
	}
	secondReader, err := c.Request.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	second, err := io.ReadAll(secondReader)
	if err != nil {
		t.Fatal(err)
	}
	_ = secondReader.Close()
	if !bytes.Equal(first, want) || !bytes.Equal(second, want) {
		t.Fatalf("replays = %q / %q, want %q", first, second, want)
	}
}

func TestBuildModelsGETDoesNotCaptureBody(t *testing.T) {
	body := &readSpyCloser{err: errors.New("GET body must not be read")}
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		c.Request.Body = body
	})
	rctx := newTestRelayCtx(t, c)
	spy := rctx.Agent.GetBodyStore().(*captureSpy)

	// behavior change: model listing authentication/build metadata must not consume a request body.
	if err := Build(rctx); err != nil {
		t.Fatal(err)
	}
	if spy.count.Load() != 0 {
		t.Fatalf("Capture count = %d, want 0", spy.count.Load())
	}
	if body.reads.Load() != 0 {
		t.Fatalf("GET body reads = %d, want 0", body.reads.Load())
	}
}

func TestBuildMultipartFindsModelAfterLargeFileWithoutJSONCopy(t *testing.T) {
	var payload bytes.Buffer
	mw := multipart.NewWriter(&payload)
	file, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(bytes.Repeat([]byte("a"), 64*1024)); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("model", "whisper-1"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(payload.Bytes()))
		c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	})
	rctx := newTestRelayCtx(t, c)
	cache := rctx.Agent.GetCache().(*settingsCache)
	cache.settings.BodyMemoryThresholdBytes = 32
	cache.settings.BodyHardLimitBytes = 1 << 20
	if err := Build(rctx); err != nil {
		t.Fatal(err)
	}
	if rctx.Input.Model != "whisper-1" {
		t.Fatalf("Model = %q, want whisper-1", rctx.Input.Model)
	}
	if len(rctx.Input.Body) != 0 {
		t.Fatalf("multipart compatibility Body retained %d bytes", len(rctx.Input.Body))
	}
	spy := rctx.Agent.GetBodyStore().(*captureSpy)
	entries, err := os.ReadDir(spy.directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("spill files = %d, want 1", len(entries))
	}
}

func TestBuildNormalizesLimitsOnceAndReturnsTypedTooLarge(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"too-large"}`))
	})
	rctx := newTestRelayCtx(t, c)
	cache := rctx.Agent.GetCache().(*settingsCache)
	cache.settings.BodyMemoryThresholdBytes = 100
	cache.settings.BodyHardLimitBytes = 8
	spy := rctx.Agent.GetBodyStore().(*captureSpy)
	err := Build(rctx)
	if !errors.Is(err, bodypkg.ErrBodyTooLarge) {
		t.Fatalf("Build error = %v, want ErrBodyTooLarge", err)
	}
	if cache.calls.Load() != 1 {
		t.Fatalf("Settings snapshot reads = %d, want 1", cache.calls.Load())
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.limits) != 1 {
		t.Fatalf("Capture limits calls = %d, want 1", len(spy.limits))
	}
	if got := spy.limits[0]; got.MemoryThreshold != 8 || got.HardLimit != 8 {
		t.Fatalf("normalized limits = %+v, want threshold=hard=8", got)
	}
}

func TestBuildMultipartValidation(t *testing.T) {
	t.Run("malformed boundary", func(t *testing.T) {
		c := newGinCtxForTest(func(c *gin.Context) {
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader("not multipart"))
			c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
		})
		err := Build(newTestRelayCtx(t, c))
		if !errors.Is(err, state.ErrInvalidBody) {
			t.Fatalf("error = %v, want ErrInvalidBody", err)
		}
	})

	t.Run("missing model", func(t *testing.T) {
		var payload bytes.Buffer
		mw := multipart.NewWriter(&payload)
		if err := mw.WriteField("language", "en"); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		c := multipartRequestContext("/v1/audio/translations", payload.Bytes(), mw.FormDataContentType())
		err := Build(newTestRelayCtx(t, c))
		if !errors.Is(err, state.ErrModelRequired) {
			t.Fatalf("error = %v, want ErrModelRequired", err)
		}
	})

	t.Run("oversize model", func(t *testing.T) {
		var payload bytes.Buffer
		mw := multipart.NewWriter(&payload)
		if err := mw.WriteField("model", strings.Repeat("m", int(maxMultipartModelBytes)+1)); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		c := multipartRequestContext("/v1/audio/transcriptions", payload.Bytes(), mw.FormDataContentType())
		err := Build(newTestRelayCtx(t, c))
		if !errors.Is(err, state.ErrInvalidBody) {
			t.Fatalf("error = %v, want ErrInvalidBody", err)
		}
	})

	t.Run("oversize non-model field", func(t *testing.T) {
		var payload bytes.Buffer
		mw := multipart.NewWriter(&payload)
		if err := mw.WriteField("prompt", strings.Repeat("p", int(maxMultipartFieldBytes)+1)); err != nil {
			t.Fatal(err)
		}
		if err := mw.WriteField("model", "whisper-1"); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		c := multipartRequestContext("/v1/audio/transcriptions", payload.Bytes(), mw.FormDataContentType())
		err := Build(newTestRelayCtx(t, c))
		if !errors.Is(err, state.ErrInvalidBody) {
			t.Fatalf("error = %v, want ErrInvalidBody", err)
		}
	})

	t.Run("too many parts", func(t *testing.T) {
		var payload bytes.Buffer
		mw := multipart.NewWriter(&payload)
		for i := 0; i < maxMultipartParts; i++ {
			if err := mw.WriteField("ignored", "x"); err != nil {
				t.Fatal(err)
			}
		}
		if err := mw.WriteField("model", "whisper-1"); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		c := multipartRequestContext("/v1/audio/transcriptions", payload.Bytes(), mw.FormDataContentType())
		err := Build(newTestRelayCtx(t, c))
		if !errors.Is(err, state.ErrInvalidBody) {
			t.Fatalf("error = %v, want ErrInvalidBody", err)
		}
	})
}

func TestBuildMultipartPreservesReplayStorageFailures(t *testing.T) {
	const boundary = "storage-fault-boundary"
	contentType := "multipart/form-data; boundary=" + boundary
	modelPrefix := "--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"model\"\r\n\r\n"
	validPayload := []byte(modelPrefix + "whisper-1\r\n--" + boundary + "--\r\n")
	tests := []struct {
		name       string
		payload    []byte
		closeFault bool
	}{
		{
			name:    "next part",
			payload: []byte(modelPrefix + "whisper-1\r\n--" + boundary + "\r\n"),
		},
		{
			name:    "model field read",
			payload: []byte(modelPrefix + "whisper"),
		},
		{
			name: "file read",
			payload: []byte("--" + boundary + "\r\n" +
				"Content-Disposition: form-data; name=\"file\"; filename=\"audio.wav\"\r\n" +
				"Content-Type: audio/wav\r\n\r\nsecret-audio"),
		},
		{
			name:       "reader close after successful model parse",
			payload:    validPayload,
			closeFault: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretCause := &os.PathError{Op: "read", Path: "/secret/multipart-spill", Err: errors.New("failed")}
			storageErr := &ctxbuildMultipartBodyError{cause: errors.Join(bodypkg.ErrBodyStoreFailed, secretCause)}
			readerFactory := func(data []byte) io.ReadCloser {
				if tt.closeFault {
					return &ctxbuildFaultReader{Reader: bytes.NewReader(data), closeErr: storageErr}
				}
				return &ctxbuildFaultReader{Reader: bytes.NewReader(data), readErr: storageErr}
			}
			rctx := newMultipartFaultRelayContext(t, tt.payload, contentType, readerFactory)

			err := Build(rctx)
			assertCtxbuildMultipartBodyFailure(t, err, storageErr)
		})
	}
}

func TestBuildMultipartPreservesReaderContextError(t *testing.T) {
	const boundary = "context-fault-boundary"
	payload := []byte("--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"model\"\r\n\r\nwhisper")
	rctx := newMultipartFaultRelayContext(
		t,
		payload,
		"multipart/form-data; boundary="+boundary,
		func(data []byte) io.ReadCloser {
			return &ctxbuildFaultReader{Reader: bytes.NewReader(data), readErr: context.Canceled}
		},
	)

	err := Build(rctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Build error = %v, want context.Canceled", err)
	}
	if errors.Is(err, state.ErrInvalidBody) {
		t.Fatalf("context cancellation was rewritten as invalid multipart: %v", err)
	}
}

func TestBuildMultipartJoinsMalformedParseAndStorageCloseFailures(t *testing.T) {
	const boundary = "joined-fault-boundary"
	payload := []byte("--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"model\"\r\n\r\nwhisper")
	secretCause := &os.PathError{Op: "close", Path: "/secret/joined-spill", Err: errors.New("failed")}
	storageErr := &ctxbuildMultipartBodyError{cause: errors.Join(bodypkg.ErrBodyStoreFailed, secretCause)}
	rctx := newMultipartFaultRelayContext(
		t,
		payload,
		"multipart/form-data; boundary="+boundary,
		func(data []byte) io.ReadCloser {
			return &ctxbuildFaultReader{
				Reader:   bytes.NewReader(data),
				readErr:  errors.New("broken multipart framing"),
				closeErr: storageErr,
			}
		},
	)

	err := Build(rctx)
	if !errors.Is(err, state.ErrInvalidBody) || !errors.Is(err, storageErr) {
		t.Fatalf("Build error = %v, want malformed parse plus storage Close failure", err)
	}
	var coded interface{ BodyErrorCode() string }
	if !errors.As(err, &coded) || coded.BodyErrorCode() != "body_store_failed" {
		t.Fatalf("Build error = %v, want body_store_failed code", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "joined-spill") {
		t.Fatalf("Build error leaked Close path: %v", err)
	}
}

func TestBuildMultipartStopsReadingAfterContextCancellation(t *testing.T) {
	const boundary = "cancel-terminal-boundary"
	payload := []byte("--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"model\"\r\n\r\n" +
		strings.Repeat("m", 128))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	trap := newCtxbuildMultipartReadTrap(payload, cancel)
	rctx := newMultipartFaultRelayContext(
		t,
		payload,
		"multipart/form-data; boundary="+boundary,
		func([]byte) io.ReadCloser { return trap },
	)
	rctx.Context.Request = rctx.Context.Request.WithContext(ctx)

	assertBuildStopsBeforeMultipartTrap(t, rctx, trap, context.Canceled, "")
}

func TestBuildMultipartStopsReadingAfterFieldSizeErrors(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		limit     int64
		detail    string
	}{
		{
			name:      "model field",
			fieldName: "model",
			limit:     maxMultipartModelBytes,
			detail:    "multipart field too large",
		},
		{
			name:      "ordinary field",
			fieldName: "prompt",
			limit:     maxMultipartFieldBytes,
			detail:    "multipart field too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const boundary = "field-terminal-boundary"
			payload := []byte("--" + boundary + "\r\n" +
				"Content-Disposition: form-data; name=\"" + tt.fieldName + "\"\r\n\r\n" +
				strings.Repeat("x", int(tt.limit)+1))
			trap := newCtxbuildMultipartReadTrap(payload, nil)
			rctx := newMultipartFaultRelayContext(
				t,
				payload,
				"multipart/form-data; boundary="+boundary,
				func([]byte) io.ReadCloser { return trap },
			)

			assertBuildStopsBeforeMultipartTrap(t, rctx, trap, state.ErrInvalidBody, tt.detail)
		})
	}
}

func TestBuildMultipartStopsReadingAfterFileSizeError(t *testing.T) {
	const (
		boundary  = "file-terminal-boundary"
		hardLimit = int64(64)
	)
	payload := []byte("--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"file\"; filename=\"audio.wav\"\r\n" +
		"Content-Type: audio/wav\r\n\r\n" +
		strings.Repeat("f", int(hardLimit)+1))
	trap := newCtxbuildMultipartReadTrap(payload, nil)
	rctx := newMultipartFaultRelayContextWithHardLimit(
		t,
		payload,
		"multipart/form-data; boundary="+boundary,
		hardLimit,
		func([]byte) io.ReadCloser { return trap },
	)

	assertBuildStopsBeforeMultipartTrap(t, rctx, trap, state.ErrInvalidBody, "multipart file too large")
}

func TestBuildMultipartStopsReadingAfterPartCountError(t *testing.T) {
	const boundary = "count-terminal-boundary"
	var payload strings.Builder
	for range maxMultipartParts {
		payload.WriteString("--" + boundary + "\r\n" +
			"Content-Disposition: form-data; name=\"ignored\"\r\n\r\n" +
			"x\r\n")
	}
	payload.WriteString("--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"ignored\"\r\n\r\n")
	body := []byte(payload.String())
	trap := newCtxbuildMultipartReadTrap(body, nil)
	rctx := newMultipartFaultRelayContext(
		t,
		body,
		"multipart/form-data; boundary="+boundary,
		func([]byte) io.ReadCloser { return trap },
	)

	assertBuildStopsBeforeMultipartTrap(t, rctx, trap, state.ErrInvalidBody, "too many multipart parts")
}

func TestMultipartObservedReadErrorTakesPrecedenceOverSizeLimit(t *testing.T) {
	storageErr := &ctxbuildMultipartBodyError{cause: bodypkg.ErrBodyStoreFailed}
	tests := []struct {
		name     string
		readErr  error
		read     func(io.Reader) error
		want     error
		wantSize bool
	}{
		{
			name:    "field storage error",
			readErr: storageErr,
			read: func(r io.Reader) error {
				_, err := readSmallPart(context.Background(), r, 4)
				return err
			},
			want: storageErr,
		},
		{
			name:    "file storage error",
			readErr: storageErr,
			read: func(r io.Reader) error {
				return drainWithContext(context.Background(), r, 4)
			},
			want: storageErr,
		},
		{
			name:    "field context error",
			readErr: context.Canceled,
			read: func(r io.Reader) error {
				_, err := readSmallPart(context.Background(), r, 4)
				return err
			},
			want: context.Canceled,
		},
		{
			name:    "file context error",
			readErr: context.DeadlineExceeded,
			read: func(r io.Reader) error {
				return drainWithContext(context.Background(), r, 4)
			},
			want: context.DeadlineExceeded,
		},
		{
			name:    "field EOF keeps size error",
			readErr: io.EOF,
			read: func(r io.Reader) error {
				_, err := readSmallPart(context.Background(), r, 4)
				return err
			},
			want:     state.ErrInvalidBody,
			wantSize: true,
		},
		{
			name:    "file EOF keeps size error",
			readErr: io.EOF,
			read: func(r io.Reader) error {
				return drainWithContext(context.Background(), r, 4)
			},
			want:     state.ErrInvalidBody,
			wantSize: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.read(&ctxbuildDataErrorReader{data: []byte("12345"), err: tt.readErr})
			if !errors.Is(err, tt.want) {
				t.Fatalf("read error = %v, want %v", err, tt.want)
			}
			if tt.wantSize != errors.Is(err, state.ErrInvalidBody) {
				t.Fatalf("read error = %v, size classification = %t, want %t", err, errors.Is(err, state.ErrInvalidBody), tt.wantSize)
			}
		})
	}
}

func TestDrainMultipartPartBounds(t *testing.T) {
	t.Run("success at limit", func(t *testing.T) {
		if err := drainWithContext(context.Background(), strings.NewReader("1234"), 4); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects above limit", func(t *testing.T) {
		err := drainWithContext(context.Background(), strings.NewReader("12345"), 4)
		if !errors.Is(err, state.ErrInvalidBody) {
			t.Fatalf("error = %v, want ErrInvalidBody", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := drainWithContext(ctx, strings.NewReader("1234"), 4); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func multipartRequestContext(path string, payload []byte, contentType string) *gin.Context {
	return newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		c.Request.Header.Set("Content-Type", contentType)
	})
}

func newMultipartFaultRelayContext(
	t *testing.T,
	payload []byte,
	contentType string,
	readerFactory func([]byte) io.ReadCloser,
) *state.RelayContext {
	t.Helper()
	return newMultipartFaultRelayContextWithHardLimit(
		t,
		payload,
		contentType,
		bodypkg.DefaultHardLimit,
		readerFactory,
	)
}

func newMultipartFaultRelayContextWithHardLimit(
	t *testing.T,
	payload []byte,
	contentType string,
	hardLimit int64,
	readerFactory func([]byte) io.ReadCloser,
) *state.RelayContext {
	t.Helper()
	c := multipartRequestContext("/v1/audio/transcriptions", payload, contentType)
	bodyStore := &ctxbuildMultipartFaultStore{readerFactory: readerFactory}
	rctx := &state.RelayContext{
		Context: c,
		State:   &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
		Agent: &ctxbuildAgent{
			cache: &settingsCache{settings: settings.AgentSettings{
				BodyMemoryThresholdBytes: min(bodypkg.DefaultMemoryThreshold, hardLimit),
				BodyHardLimitBytes:       hardLimit,
			}},
			bodyStore: bodyStore,
		},
	}
	t.Cleanup(func() {
		if c.Request != nil && c.Request.Body != nil {
			_ = c.Request.Body.Close()
		}
		if rctx.Resources != nil {
			_ = rctx.Resources.Close()
		}
	})
	return rctx
}

type ctxbuildMultipartFaultStore struct {
	readerFactory func([]byte) io.ReadCloser
}

func (s *ctxbuildMultipartFaultStore) Capture(_ context.Context, src io.Reader, _ app.BodyLimits) (app.ReplayBody, error) {
	body, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	return &ctxbuildMultipartFaultBody{data: body, readerFactory: s.readerFactory}, nil
}

type ctxbuildMultipartFaultBody struct {
	data          []byte
	readerFactory func([]byte) io.ReadCloser
	opens         atomic.Int32
}

func (b *ctxbuildMultipartFaultBody) Size() int64 { return int64(len(b.data)) }
func (b *ctxbuildMultipartFaultBody) Open() (io.ReadCloser, error) {
	if b.opens.Add(1) == 2 {
		return b.readerFactory(b.data), nil
	}
	return io.NopCloser(bytes.NewReader(b.data)), nil
}
func (b *ctxbuildMultipartFaultBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, io.ErrShortBuffer
	}
	return append([]byte(nil), b.data...), nil
}
func (*ctxbuildMultipartFaultBody) Close() error { return nil }

type ctxbuildFaultReader struct {
	*bytes.Reader
	readErr  error
	closeErr error
	sentErr  bool
}

func (r *ctxbuildFaultReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if errors.Is(err, io.EOF) && r.readErr != nil && !r.sentErr {
		r.sentErr = true
		return 0, r.readErr
	}
	return n, err
}

func (r *ctxbuildFaultReader) Close() error { return r.closeErr }

type ctxbuildMultipartReadTrap struct {
	data        []byte
	offset      int
	onArmed     func()
	armed       sync.Once
	trapped     chan struct{}
	trapOnce    sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	afterReads  atomic.Int32
}

func newCtxbuildMultipartReadTrap(data []byte, onArmed func()) *ctxbuildMultipartReadTrap {
	return &ctxbuildMultipartReadTrap{
		data:    append([]byte(nil), data...),
		onArmed: onArmed,
		trapped: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *ctxbuildMultipartReadTrap) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.offset < len(r.data) {
		n := copy(p, r.data[r.offset:])
		r.offset += n
		if r.offset == len(r.data) {
			r.armed.Do(func() {
				if r.onArmed != nil {
					r.onArmed()
				}
			})
		}
		return n, nil
	}
	r.afterReads.Add(1)
	r.trapOnce.Do(func() { close(r.trapped) })
	<-r.release
	return 0, io.EOF
}

func (*ctxbuildMultipartReadTrap) Close() error { return nil }

func (r *ctxbuildMultipartReadTrap) releaseRead() {
	r.releaseOnce.Do(func() { close(r.release) })
}

type ctxbuildDataErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *ctxbuildDataErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), r.err
}

func assertBuildStopsBeforeMultipartTrap(
	t *testing.T,
	rctx *state.RelayContext,
	trap *ctxbuildMultipartReadTrap,
	want error,
	detail string,
) {
	t.Helper()
	t.Cleanup(trap.releaseRead)
	result := make(chan error, 1)
	go func() { result <- Build(rctx) }()

	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatalf("Build error = %v, want %v", err, want)
		}
		if detail != "" && !strings.Contains(err.Error(), detail) {
			t.Fatalf("Build error = %v, want detail %q", err, detail)
		}
		if got := trap.afterReads.Load(); got != 0 {
			t.Fatalf("multipart remainder reads = %d, want 0", got)
		}
	case <-trap.trapped:
		select {
		case err := <-result:
			t.Fatalf("Build returned %v after reading multipart remainder", err)
		default:
		}
		trap.releaseRead()
		err := <-result
		t.Fatalf("Build blocked draining multipart remainder after terminal error %v", err)
	case <-time.After(time.Second):
		trap.releaseRead()
		select {
		case err := <-result:
			t.Fatalf("Build returned late with %v", err)
		case <-time.After(time.Second):
			t.Fatal("Build did not return after multipart trap release")
		}
	}
}

type ctxbuildMultipartBodyError struct {
	cause error
}

func (*ctxbuildMultipartBodyError) Error() string         { return "body_store_failed" }
func (*ctxbuildMultipartBodyError) BodyErrorCode() string { return "body_store_failed" }
func (e *ctxbuildMultipartBodyError) Unwrap() error       { return e.cause }

func assertCtxbuildMultipartBodyFailure(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || !errors.Is(err, bodypkg.ErrBodyStoreFailed) {
		t.Fatalf("Build error = %v, want structured body_store_failed", err)
	}
	var coded interface{ BodyErrorCode() string }
	if !errors.As(err, &coded) || coded.BodyErrorCode() != "body_store_failed" {
		t.Fatalf("Build error = %v, want body_store_failed code", err)
	}
	if err.Error() != "body_store_failed" || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "multipart-spill") {
		t.Fatalf("Build error leaked multipart storage details: %v", err)
	}
	if errors.Is(err, state.ErrInvalidBody) {
		t.Fatalf("storage failure was rewritten as invalid multipart: %v", err)
	}
}

type readSpyCloser struct {
	reads atomic.Int32
	err   error
}

func (r *readSpyCloser) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, r.err
}

func (*readSpyCloser) Close() error { return nil }
