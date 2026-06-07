package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

// TestRelay_TracksAndReleasesInflight 验证:
// 1. Relay 开始时把请求登记到 inflight registry
// 2. 即使 ctxBuild 阶段失败早退,Done() 仍会被调用,Snapshot 长度归零
func TestRelay_TracksAndReleasesInflight(t *testing.T) {
	reg := inflight.NewRegistry(nil, 0)

	// 复用 setupTestHandler 同款构造(nil channels,测试只需验证 inflight 生命周期)
	// 把 registry 作为第四参传入
	h, _, bus := setupTestHandler(nil)
	// 重建一个携带 registry 的 handler
	h = NewHandler(bus, h.Agent, TestDispatcherFactory(h.Agent), reg, nil)

	// 构造一个 POST /v1/chat/completions、body=`{bad json` 的 gin.Context
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})

	h.Relay(c)

	if got := len(reg.Snapshot()); got != 0 {
		t.Fatalf("inflight not released after Relay, got %d", got)
	}
}

// TestRelay_InflightNilRegistry 验证 registry 为 nil 时 Relay 正常工作(不 panic)
func TestRelay_InflightNilRegistry(t *testing.T) {
	// nil registry,复用现有 setupTestHandler
	h, _, _ := setupTestHandler(nil)
	// 确认 registry 为 nil 时走老路径不 panic

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})

	// 只断言不 panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil registry caused panic: %v", r)
		}
	}()
	h.Relay(c)
}

// TestRelay_InflightTrackedDuringExecution 验证正常请求在执行期间 Snapshot 有 1 条记录。
// 用 sync channel 卡住 upstream,在 Relay 阻塞时读取 Snapshot。
func TestRelay_InflightTrackedDuringExecution(t *testing.T) {
	reg := inflight.NewRegistry(nil, 0)

	// 构造一个会阻塞的 upstream
	started := make(chan struct{})
	unblock := make(chan struct{})
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-unblock
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstreamSrv.Close()

	// 通过 setupTestHandler 构造 agentApp + bus,然后用带 registry 的 NewHandler 重建
	h, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 99, Type: consts.ChannelTypeOpenAI, BaseURL: upstreamSrv.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})
	h = NewHandler(bus, h.Agent, TestDispatcherFactory(h.Agent), reg, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		r := setupRouterWithUserInfo(h, &app.UserInfo{UserID: 1, TokenID: 1})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
	}()

	// 等 upstream 收到请求后验证 inflight 有条目
	<-started
	if got := len(reg.Snapshot()); got != 1 {
		t.Errorf("expected 1 inflight entry during execution, got %d", got)
	}

	// 放行 upstream,等 Relay 完成
	close(unblock)
	<-done

	// Relay 完成后条目应被释放
	if got := len(reg.Snapshot()); got != 0 {
		t.Errorf("inflight not released after Relay completed, got %d", got)
	}
}

// TestRelay_InterruptAbortsInflight 验证:打断在途请求会取消上游调用,
// Relay 在不放行 upstream 的情况下也能返回(context.Canceled 终止)。
func TestRelay_InterruptAbortsInflight(t *testing.T) {
	reg := inflight.NewRegistry(nil, 0)

	started := make(chan struct{})
	quit := make(chan struct{})
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done(): // 客户端(relay)取消后连接关闭,server-side context 被取消
		case <-quit: // 测试结束兜底
		}
	}))
	defer func() { close(quit); upstreamSrv.Close() }()

	h, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 99, Type: consts.ChannelTypeOpenAI, BaseURL: upstreamSrv.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})
	h = NewHandler(bus, h.Agent, TestDispatcherFactory(h.Agent), reg, nil)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})

	done := make(chan struct{})
	go func() { h.Relay(c); close(done) }()

	<-started
	snaps := reg.Snapshot()
	if len(snaps) == 0 {
		t.Fatal("no inflight snapshot to interrupt")
	}
	if !reg.Interrupt(snaps[0].ID) {
		t.Fatalf("Interrupt(%d) returned false", snaps[0].ID)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Relay did not return after interrupt (cancel not propagated to upstream)")
	}
}
