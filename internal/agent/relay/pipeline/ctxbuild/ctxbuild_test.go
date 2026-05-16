package ctxbuild

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
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

func newTestRelayCtx(c *gin.Context) *state.RelayContext {
	return &state.RelayContext{
		Context: c,
		State:   &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
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
	rctx := newTestRelayCtx(c)
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
	rctx := newTestRelayCtx(c)
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
	rctx := newTestRelayCtx(c)
	err := Build(rctx)
	if !errors.Is(err, state.ErrReadBody) {
		t.Errorf("got %v want errors.Is ErrReadBody", err)
	}
	if err == nil || !strings.Contains(err.Error(), "read failure") {
		t.Errorf("error should embed original read cause, got %v", err)
	}
}

func TestBuildInvalidJSON(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader("not json"))
	})
	rctx := newTestRelayCtx(c)
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
	rctx := newTestRelayCtx(c)
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
	rctx := newTestRelayCtx(c)
	if err := Build(rctx); err != state.ErrModelRequired {
		t.Errorf("got %v want state.ErrModelRequired", err)
	}
}

func TestBuildInvalidForcedChannelID(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader(`{"model":"x"}`))
		c.Request.Header.Set(consts.HeaderXChannelID, "not-a-number")
	})
	rctx := newTestRelayCtx(c)
	if err := Build(rctx); err != state.ErrInvalidForcedChannelID {
		t.Errorf("got %v want state.ErrInvalidForcedChannelID", err)
	}
}

func TestBuildForcedChannelIDParsed(t *testing.T) {
	c := newGinCtxForTest(func(c *gin.Context) {
		c.Request = httptest.NewRequest("POST", "/v1/x", strings.NewReader(`{"model":"x"}`))
		c.Request.Header.Set(consts.HeaderXChannelID, "42")
	})
	rctx := newTestRelayCtx(c)
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
	rctx := newTestRelayCtx(c)
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
