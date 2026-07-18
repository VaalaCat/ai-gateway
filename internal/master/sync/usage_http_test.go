package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newUsageTestFixture 组装一个最小 master.sync 环境:
// 内存 sqlite + AutoMigrate + 一条已启用 agent + Hub(内存 event bus) +
// 仅注册 POST /api/agents/usage 的 gin router(裸 handler 风格,同 /ws/agent)。
// 复用本包 fetch_handlers_test.go 里的 dbApp(满足 dao.AppProvider)。
func newUsageTestFixture(t *testing.T) (*Hub, *gin.Engine, *models.Agent) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	agent := &models.Agent{AgentID: "agent-t", Secret: "sec-t", Name: "agent-t", Status: consts.StatusEnabled}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}

	app := &dbApp{db: db}
	bus := eventbus.NewMemoryBus()
	h := NewHub(app, zap.NewNop(), bus, func() int64 { return 1 }, nil, HubOptions{})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/agents/usage", func(c *gin.Context) {
		h.HandleUsageHTTP(c)
	})

	return h, r, agent
}

// postUsage 向测试 router 发一笔上报。
func postUsage(t *testing.T, r *gin.Engine, id, secret string, report protocol.UsageReport) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(report)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/usage", bytes.NewReader(body))
	req.Header.Set(consts.HeaderXAgentID, id)
	req.Header.Set(consts.HeaderXAgentSecret, secret)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// postUsageRaw 向测试 router 发一笔上报,body/header 由调用方原样控制——postUsage
// 内部做 json.Marshal,喂不了压缩体或自定义 header(如 Content-Encoding: gzip)。
func postUsageRaw(t *testing.T, r *gin.Engine, id, secret string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/agents/usage", body)
	req.Header.Set(consts.HeaderXAgentID, id)
	req.Header.Set(consts.HeaderXAgentSecret, secret)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// gzipBody 把 b 压成 gzip,给 gzip 摄取相关测试用。
func gzipBody(t *testing.T, b []byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buf
}

func TestUsageHTTPGzipIngest(t *testing.T) {
	h, r, agent := newUsageTestFixture(t)
	var settled int
	h.SettleUsage = func(_ context.Context, _ string, logs []protocol.UsageLogEntry) error {
		settled = len(logs)
		return nil
	}
	body := gzipBody(t, []byte(`{"logs":[{"request_id":"r1"},{"request_id":"r2"}]}`))
	w := postUsageRaw(t, r, agent.AgentID, agent.Secret, body, map[string]string{"Content-Encoding": "gzip"})
	if w.Code != http.StatusOK || settled != 2 {
		t.Fatalf("code=%d settled=%d, want 200/2; body=%s", w.Code, settled, w.Body.String())
	}
}

func TestUsageHTTPPlainStillWorks(t *testing.T) {
	h, r, agent := newUsageTestFixture(t)
	h.SettleUsage = func(context.Context, string, []protocol.UsageLogEntry) error { return nil }
	w := postUsageRaw(t, r, agent.AgentID, agent.Secret, bytes.NewBufferString(`{"logs":[]}`), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("plain ingest broke: %d; body=%s", w.Code, w.Body.String())
	}
}

// TestUsageHTTPGzipBombRejected 40MiB 的空格字节压缩后极小(远低于
// usageIngestMaxBodyBytes 的压缩体上限),但解压后超 usageIngestMaxDecodedBytes(32MiB),
// 必须 400 而不是把 40MiB 全读进内存。
func TestUsageHTTPGzipBombRejected(t *testing.T) {
	_, r, agent := newUsageTestFixture(t)
	body := gzipBody(t, bytes.Repeat([]byte{' '}, 40<<20))
	w := postUsageRaw(t, r, agent.AgentID, agent.Secret, body, map[string]string{"Content-Encoding": "gzip"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("zip bomb must be rejected with 400, got %d", w.Code)
	}
}

func TestUsageHTTPCorruptGzipRejected(t *testing.T) {
	_, r, agent := newUsageTestFixture(t)
	w := postUsageRaw(t, r, agent.AgentID, agent.Secret, bytes.NewBufferString("not-gzip-at-all"),
		map[string]string{"Content-Encoding": "gzip"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("corrupt gzip must be 400, got %d", w.Code)
	}
}

func TestUsageHTTP_AcceptsAndPublishes(t *testing.T) {
	h, r, agent := newUsageTestFixture(t) // helper: hub + router(注册了 POST /api/agents/usage) + 已建 agent(status=1)
	// SettleUsage 显式留空(nil):走老的异步 publish 语义(ack ≠ 已持久化)。
	if h.SettleUsage != nil {
		t.Fatalf("fixture must leave SettleUsage nil for this legacy-path test")
	}
	type requestContextKey struct{}
	ctxKey := requestContextKey{}
	got := make(chan struct {
		ctx    context.Context
		report protocol.UsageReport
	}, 1)
	events.SubscribeUsageReported(h.Bus, func(ctx context.Context, rep protocol.UsageReport) error {
		got <- struct {
			ctx    context.Context
			report protocol.UsageReport
		}{ctx: ctx, report: rep}
		return nil
	})

	report := protocol.UsageReport{
		AgentID: agent.AgentID,
		Logs:    []protocol.UsageLogEntry{{RequestID: "req-1"}},
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/usage", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), ctxKey, "request"))
	req.Header.Set(consts.HeaderXAgentID, agent.AgentID)
	req.Header.Set(consts.HeaderXAgentSecret, agent.Secret)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	published := <-got
	rep := published.report
	if len(rep.Logs) != 1 || rep.Logs[0].RequestID != "req-1" {
		t.Fatalf("published report = %+v", rep)
	}
	if got := published.ctx.Value(ctxKey); got != "request" {
		t.Fatalf("published context marker = %v, want request", got)
	}
}

// TestUsageHTTP_SyncSettleSuccess_Returns200AndCallsSettleWithAuthenticatedAgentID
// 验证接线了 SettleUsage 时(数据面同步结算),摄取端点:
//   - 不发 usage.reported(不会双结算);
//   - 直接调用 SettleUsage,agentID 参数用鉴权得到的值(而非 body 里可能被冒领的
//     agent_id),logs 原样传入;
//   - SettleUsage 返回 nil → 200 且 accepted 计数正确。
func TestUsageHTTP_SyncSettleSuccess_Returns200AndCallsSettleWithAuthenticatedAgentID(t *testing.T) {
	h, r, agent := newUsageTestFixture(t)

	published := make(chan protocol.UsageReport, 1)
	events.SubscribeUsageReported(h.Bus, func(_ context.Context, rep protocol.UsageReport) error {
		published <- rep
		return nil
	})

	type settleCall struct {
		agentID string
		logs    []protocol.UsageLogEntry
	}
	calls := make(chan settleCall, 1)
	h.SettleUsage = func(_ context.Context, agentID string, logs []protocol.UsageLogEntry) error {
		calls <- settleCall{agentID: agentID, logs: logs}
		return nil
	}

	w := postUsage(t, r, agent.AgentID, agent.Secret, protocol.UsageReport{
		AgentID: "some-other-agent", // 冒领:不应影响传给 SettleUsage 的 agentID
		Logs:    []protocol.UsageLogEntry{{RequestID: "req-sync-1"}, {RequestID: "req-sync-2"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var body map[string]int
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["accepted"] != 2 {
		t.Fatalf("accepted = %d, want 2", body["accepted"])
	}

	select {
	case call := <-calls:
		if call.agentID != agent.AgentID {
			t.Fatalf("SettleUsage agentID = %q, want authenticated agent %q", call.agentID, agent.AgentID)
		}
		if len(call.logs) != 2 || call.logs[0].RequestID != "req-sync-1" || call.logs[1].RequestID != "req-sync-2" {
			t.Fatalf("SettleUsage logs = %+v", call.logs)
		}
	default:
		t.Fatal("SettleUsage was not called")
	}

	select {
	case rep := <-published:
		t.Fatalf("usage.reported must NOT be published on the sync-settle path, got %+v", rep)
	default:
	}
}

// TestUsageHTTP_SyncSettleError_Returns500WithSettleFailedBody 验证 SettleUsage
// 返回错误(如结算期 SQLITE_BUSY)时,摄取端点不 ack:500 + body 说 settle failed,
// 让 agent 带着数据重试整批(request_id 去重保证幂等)。
func TestUsageHTTP_SyncSettleError_Returns500WithSettleFailedBody(t *testing.T) {
	h, r, agent := newUsageTestFixture(t)
	h.SettleUsage = func(_ context.Context, _ string, _ []protocol.UsageLogEntry) error {
		return errors.New("boom: sqlite busy")
	}

	w := postUsage(t, r, agent.AgentID, agent.Secret, protocol.UsageReport{
		AgentID: agent.AgentID,
		Logs:    []protocol.UsageLogEntry{{RequestID: "req-fail-1"}},
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("settle failed")) {
		t.Fatalf("body = %s, want it to mention settle failed", w.Body.String())
	}
}

func TestUsageHTTP_RejectsBadCredentials(t *testing.T) {
	_, r, agent := newUsageTestFixture(t)
	// 错 secret
	w := postUsage(t, r, agent.AgentID, "wrong", protocol.UsageReport{})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad secret status = %d, want 401", w.Code)
	}
	// 缺 header
	req := httptest.NewRequest(http.MethodPost, "/api/agents/usage", bytes.NewReader([]byte("{}")))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("missing header status = %d, want 401", w2.Code)
	}
}

func TestUsageHTTP_RejectsMalformedBody(t *testing.T) {
	_, r, agent := newUsageTestFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/usage", bytes.NewReader([]byte("{not json")))
	req.Header.Set(consts.HeaderXAgentID, agent.AgentID)
	req.Header.Set(consts.HeaderXAgentSecret, agent.Secret)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestUsageHTTP_EmptyLogsIsOK(t *testing.T) { // boundary:空批次幂等返回 200
	_, r, agent := newUsageTestFixture(t)
	w := postUsage(t, r, agent.AgentID, agent.Secret, protocol.UsageReport{AgentID: agent.AgentID})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestUsageHTTP_RejectsOversizedBody(t *testing.T) { // SHOULD-FIX:摄取端点必须限制请求体大小
	_, r, agent := newUsageTestFixture(t)
	huge := bytes.Repeat([]byte("a"), 11<<20) // 11 MiB > 10 MiB 上限
	report := protocol.UsageReport{
		AgentID: agent.AgentID,
		Logs:    []protocol.UsageLogEntry{{RequestID: "req-huge", ClientIP: string(huge)}},
	}
	w := postUsage(t, r, agent.AgentID, agent.Secret, report)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for oversized body", w.Code)
	}
}

func TestUsageHTTP_OverwritesAgentIDFromAuth(t *testing.T) { // cheap:防跨 agent 冒领,忽略 body 里的 agent_id
	h, r, agent := newUsageTestFixture(t)
	// SettleUsage 显式留空(nil):走老的异步 publish 语义,验证冒领防护在该兜底路径上依然生效。
	if h.SettleUsage != nil {
		t.Fatalf("fixture must leave SettleUsage nil for this legacy-path test")
	}
	got := make(chan protocol.UsageReport, 1)
	events.SubscribeUsageReported(h.Bus, func(_ context.Context, rep protocol.UsageReport) error {
		got <- rep
		return nil
	})
	w := postUsage(t, r, agent.AgentID, agent.Secret, protocol.UsageReport{
		AgentID: "some-other-agent", // body 里声称是别的 agent
		Logs:    []protocol.UsageLogEntry{{RequestID: "req-spoof"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	rep := <-got
	if rep.AgentID != agent.AgentID {
		t.Fatalf("published AgentID = %q, want authenticated agent %q (auth must win over body)", rep.AgentID, agent.AgentID)
	}
}
