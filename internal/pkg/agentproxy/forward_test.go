package agentproxy_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// TestRouteForwarderImplementsAppInterface 断言 *agentproxy.RouteForwarder 满足
// app.RouteForwarder。
// 用 package agentproxy_test 外部测试包：app 已经 import agentproxy，
// 因此内部测试包再 import app 会形成循环。外部测试包独立编译，不参与该循环。
func TestRouteForwarderImplementsAppInterface(t *testing.T) {
	var _ app.RouteForwarder = (*agentproxy.RouteForwarder)(nil)
}

// TestRouteForwarderViaAppInterface_AgentNotFound 通过接口调用 ForwardByRoute，
// 验证当 route 解析不到 agent 时返回 forwarded=false + error。
// 保证签名兼容性：方法可直接被接口持有方调用。
func TestRouteForwarderViaAppInterface_AgentNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var fwd app.RouteForwarder = &agentproxy.RouteForwarder{
		SelfID:         "self",
		GetAgent:       func(string) *models.Agent { return nil },
		GetAgentsByTag: func(string) []*models.Agent { return nil },
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/x", nil)

	forwarded, err := fwd.ForwardByRoute(c, &models.AgentRoute{AgentID: "missing"})
	if forwarded {
		t.Error("expected forwarded=false when agent not found")
	}
	if err == nil {
		t.Error("expected error when agent not found")
	}
}

// TestRouteForwarderViaAppInterface_SelfNotForwarded 验证目标即为 SelfID 时，
// 返回 forwarded=false 且 err=nil（本地处理路径）。
func TestRouteForwarderViaAppInterface_SelfNotForwarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	self := &models.Agent{AgentID: "self"}
	var fwd app.RouteForwarder = &agentproxy.RouteForwarder{
		SelfID:   "self",
		GetAgent: func(id string) *models.Agent { return self },
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/x", nil)

	forwarded, err := fwd.ForwardByRoute(c, &models.AgentRoute{AgentID: "self"})
	if forwarded {
		t.Error("expected forwarded=false when target is self")
	}
	if err != nil {
		t.Errorf("expected nil err for self route, got %v", err)
	}
}

// closeNotifierRecorder 给 httptest.ResponseRecorder 加上 http.CloseNotifier，
// 否则 gin 的 responseWriter 在 ReverseProxy.ServeHTTP 路径下会 panic。
type closeNotifierRecorder struct{ *httptest.ResponseRecorder }

func (closeNotifierRecorder) CloseNotify() <-chan bool { return make(chan bool, 1) }

// TestForward_SuccessEndToEnd 验证 Forward 端到端：请求被代理到目标 server，
// 响应 body / status 透传，且转发头被剥离 / hop 计数 +1。
func TestForward_SuccessEndToEnd(t *testing.T) {
	var gotAgentID, gotAgentTag, gotHop string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAgentID = r.Header.Get(consts.HeaderXAgentID)
		gotAgentTag = r.Header.Get(consts.HeaderXAgentTag)
		gotHop = r.Header.Get(consts.HeaderXAgentHop)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
	req.Header.Set(consts.HeaderXAgentID, "should-be-stripped")
	req.Header.Set(consts.HeaderXAgentTag, "should-be-stripped")
	req.Header.Set(consts.HeaderXAgentHop, "2")
	w := httptest.NewRecorder()

	if err := agentproxy.Forward(target.URL, "", w, req); err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("expected body to pass through, got %q", body)
	}
	if gotAgentID != "" || gotAgentTag != "" {
		t.Errorf("expected forwarding headers stripped, got id=%q tag=%q", gotAgentID, gotAgentTag)
	}
	if gotHop != "3" {
		t.Errorf("expected hop counter 2+1=3, got %q", gotHop)
	}
}

// TestForward_InvalidTargetURL 验证非法 targetURL 直接返错，未触发 ServeHTTP。
func TestForward_InvalidTargetURL(t *testing.T) {
	req := httptest.NewRequest("POST", "/x", nil)
	w := httptest.NewRecorder()
	err := agentproxy.Forward("://bad-url", "", w, req)
	if err == nil {
		t.Error("expected error for malformed targetURL")
	}
}

// TestForwardByRoute_SuccessByAgentID 验证 ByRoute(AgentID 路径) 端到端：
// GetAgent 返回包含 httptest server URL 的 agent，preferredTag 命中跳过 probe，
// 期望 forwarded=true 且响应透传。
func TestForwardByRoute_SuccessByAgentID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	defer target.Close()

	addresses := fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, target.URL)
	peer := &models.Agent{AgentID: "peer", HTTPAddresses: addresses}

	fwd := &agentproxy.RouteForwarder{
		SelfID:       "self",
		GetAgent:     func(id string) *models.Agent { return peer },
		PreferredTag: "local",
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(closeNotifierRecorder{w})
	c.Request = httptest.NewRequest("POST", "/v1/foo", strings.NewReader("body"))

	forwarded, err := fwd.ForwardByRoute(c, &models.AgentRoute{AgentID: "peer"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !forwarded {
		t.Error("expected forwarded=true")
	}
	if got := w.Body.String(); !strings.Contains(got, "upstream-ok") {
		t.Errorf("expected upstream body, got %q", got)
	}
}

// TestForwardByRoute_SuccessByAgentTag 验证 ByRoute(AgentTag 路径) 走 GetAgentsByTag
// 取第一个 agent 并转发成功。
func TestForwardByRoute_SuccessByAgentTag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	addresses := fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, target.URL)
	peer := &models.Agent{AgentID: "peer-by-tag", HTTPAddresses: addresses}

	fwd := &agentproxy.RouteForwarder{
		SelfID:         "self",
		GetAgent:       func(string) *models.Agent { return nil },
		GetAgentsByTag: func(tag string) []*models.Agent { return []*models.Agent{peer} },
		PreferredTag:   "local",
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(closeNotifierRecorder{w})
	c.Request = httptest.NewRequest("POST", "/v1/foo", nil)

	forwarded, err := fwd.ForwardByRoute(c, &models.AgentRoute{AgentTag: "group-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !forwarded {
		t.Error("expected forwarded=true")
	}
	if status := c.Writer.Status(); status != http.StatusAccepted {
		t.Errorf("expected upstream 202, got %d", status)
	}
}

// TestForwardByRoute_NoAddresses 验证 agent 找到但 HTTPAddresses 为空时，
// ResolveAddress 返错，forwarded=false 且 err 非 nil。
func TestForwardByRoute_NoAddresses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	peer := &models.Agent{AgentID: "peer", HTTPAddresses: ""}

	fwd := &agentproxy.RouteForwarder{
		SelfID:   "self",
		GetAgent: func(string) *models.Agent { return peer },
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/x", nil)

	forwarded, err := fwd.ForwardByRoute(c, &models.AgentRoute{AgentID: "peer"})
	if forwarded {
		t.Error("expected forwarded=false when no addresses configured")
	}
	if err == nil {
		t.Error("expected error when agent has no addresses")
	}
}
