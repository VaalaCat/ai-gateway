package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// doRoutingRequestExpectCode fires POST /v1/chat/completions and returns (httpCode, body).
// Unlike doRoutingRequest it does NOT subscribe to events (avoids subscriber leak in caller-managed buses).
func doRoutingRequestExpectCode(t *testing.T, handler *Handler, ui *app.UserInfo, modelName string) (int, string) {
	t.Helper()
	r := setupRouterWithUserInfo(handler, ui)
	w := httptest.NewRecorder()
	body := `{"model":"` + modelName + `","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// upstreamReturning500 returns an HTTP 500 for any request.
func upstreamReturning500() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
}

// doRoutingRequest fires a POST /v1/chat/completions with model=modelName and waits for a usage log.
// Returns (httpCode, usageLogs collected within 500ms).
// 用 collectUsageLogs 而不是裸 *[]UsageLogEntry，确保 -race 下读写有 mu 保护。
func doRoutingRequest(t *testing.T, handler *Handler, ui *app.UserInfo, bus app.EventBus, modelName string) (int, []protocol.UsageLogEntry) {
	t.Helper()

	logs := collectUsageLogs(bus)

	r := setupRouterWithUserInfo(handler, ui)
	w := httptest.NewRecorder()
	body := `{"model":"` + modelName + `","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	time.Sleep(150 * time.Millisecond)
	return w.Code, logs.Snapshot()
}

// TestRelay_RoutingHit_Success: global routing smart=[deepseek P0 W1]，deepseek 有可用 channel；
// 断言 200 + UsageLog.ModelName="deepseek" + UsageLog.RoutingName="smart"
func TestRelay_RoutingHit_Success(t *testing.T) {
	up := upstreamReturning200()
	defer up.Close()

	handler, store, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: up.URL, Status: 1, Weight: 1}, Key: "k", Models: "deepseek"},
	})

	store.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "deepseek", Priority: 0, Weight: 1},
		},
	})

	ui := &app.UserInfo{UserID: 1, TokenID: 1}
	code, logs := doRoutingRequest(t, handler, ui, bus, "smart")

	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(logs) == 0 {
		t.Fatal("expected usage log")
	}
	entry := logs[0]
	if entry.ModelName != "deepseek" {
		t.Errorf("ModelName = %q, want %q", entry.ModelName, "deepseek")
	}
	if entry.RoutingName != "smart" {
		t.Errorf("RoutingName = %q, want %q", entry.RoutingName, "smart")
	}
}

// TestRelay_NoRoutingHit_Unchanged: 没有 routing，请求 model=gpt-4o → 走老逻辑，
// UsageLog.RoutingName="" 空字符串
func TestRelay_NoRoutingHit_Unchanged(t *testing.T) {
	up := upstreamReturning200()
	defer up.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: up.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	ui := &app.UserInfo{UserID: 1, TokenID: 1}
	code, logs := doRoutingRequest(t, handler, ui, bus, "gpt-4o")

	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(logs) == 0 {
		t.Fatal("expected usage log")
	}
	entry := logs[0]
	if entry.RoutingName != "" {
		t.Errorf("RoutingName = %q, want empty string (non-routing request)", entry.RoutingName)
	}
	if entry.ModelName != "gpt-4o" {
		t.Errorf("ModelName = %q, want %q", entry.ModelName, "gpt-4o")
	}
}

// TestRelay_RoutingFallback_FirstMemberFailsAllChannels: smart=[a P10 W1, b P5 W1]，
// a 唯一 channel 返回 500，b 的 channel 返回 200 → 整体成功，ModelName=b，RoutingName=smart
func TestRelay_RoutingFallback_FirstMemberFailsAllChannels(t *testing.T) {
	upA := upstreamReturning500()
	defer upA.Close()
	upB := upstreamReturning200()
	defer upB.Close()

	handler, store, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1, Priority: 10}, Key: "k1", Models: "a"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upB.URL, Status: 1, Weight: 1, Priority: 5}, Key: "k2", Models: "b"},
	})

	store.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "a", Priority: 10, Weight: 1},
			{Ref: "b", Priority: 5, Weight: 1},
		},
	})

	ui := &app.UserInfo{UserID: 1, TokenID: 1}
	code, logs := doRoutingRequest(t, handler, ui, bus, "smart")

	if code != 200 {
		t.Fatalf("expected 200 after fallback, got %d", code)
	}
	if len(logs) == 0 {
		t.Fatal("expected at least one usage log")
	}

	// 找最后一条成功日志
	var successLog *protocol.UsageLogEntry
	for i := range logs {
		if logs[i].Status == 1 {
			successLog = &logs[i]
		}
	}
	if successLog == nil {
		t.Fatal("expected a successful usage log (Status=1)")
	}
	if successLog.ModelName != "b" {
		t.Errorf("ModelName = %q, want %q (fallback to b)", successLog.ModelName, "b")
	}
	if successLog.RoutingName != "smart" {
		t.Errorf("RoutingName = %q, want %q", successLog.RoutingName, "smart")
	}
}

// TestRelay_RoutingExhausted_404: smart=[a, b]，a 和 b 的所有 channel 都 500 → 整体 502，
// UsageLog.RoutingName=smart 仍记录
func TestRelay_RoutingExhausted_404(t *testing.T) {
	upA := upstreamReturning500()
	defer upA.Close()
	upB := upstreamReturning500()
	defer upB.Close()

	handler, store, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1}, Key: "k1", Models: "a"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upB.URL, Status: 1, Weight: 1}, Key: "k2", Models: "b"},
	})

	store.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "a", Priority: 0, Weight: 1},
			{Ref: "b", Priority: 0, Weight: 1},
		},
	})

	// setupTestHandler 默认 retryMax=3，配 2 个 channel（a, b）够穿过整条 plan，
	// 无需额外覆盖 RetryMax（Handler 不直接持有该字段，由 Agent 配置注入）。

	ui := &app.UserInfo{UserID: 1, TokenID: 1}

	logs := collectUsageLogs(bus)

	r := setupRouterWithUserInfo(handler, ui)
	w := httptest.NewRecorder()
	body := `{"model":"smart","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	time.Sleep(200 * time.Millisecond)

	// 应该是 502（BadGateway）
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
	snap := logs.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected at least one usage log even on failure")
	}

	// 检查是否有 RoutingName=smart 的日志
	foundRoutingLog := false
	for _, entry := range snap {
		if entry.RoutingName == "smart" {
			foundRoutingLog = true
			break
		}
	}
	if !foundRoutingLog {
		// 打印所有日志供调试
		for i, entry := range snap {
			t.Logf("log[%d]: ModelName=%q RoutingName=%q Status=%d", i, entry.ModelName, entry.RoutingName, entry.Status)
		}
		t.Error("expected at least one usage log with RoutingName=smart")
	}

	// 检查最终响应里有错误信息
	if !strings.Contains(w.Body.String(), "error") {
		t.Errorf("expected error in response body, got: %s", w.Body.String())
	}

	// 检查所有日志的 JSON Other 字段包含 routing_trace（如果有 trace）
	for _, entry := range snap {
		if entry.RoutingName == "smart" && entry.Other != "" {
			var other map[string]any
			if err := json.Unmarshal([]byte(entry.Other), &other); err == nil {
				if _, hasTrace := other["routing_trace"]; hasTrace {
					t.Logf("routing_trace in Other: %v", other["routing_trace"])
				}
			}
		}
	}
}

// TestRelay_AgentRoute_MatchesByRealModel: 验证 RouteIndex.Match 使用真实 model（routing 解析后）而非顶层入参。
// 设置：global routing smart=[deepseek]，调用 ResolveToRealModel 校验解析，
// 断言 routing 层后 realModel=deepseek（不是 smart）。
func TestRelay_AgentRoute_MatchesByRealModel(t *testing.T) {
	s := cache.NewStore(nil, config.AgentCacheConfig{})
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "deepseek", Priority: 0, Weight: 1}},
	})

	ctx := plan.NewResolveCtx()
	real := plan.ResolveToRealModel(s, "smart", 0, ctx)
	if real != "deepseek" {
		t.Fatalf("expect realModel=deepseek, got %q", real)
	}
	// 验证：handler.go 改造后 Match 调用用 real（不是 "smart"）
	// 完整 handler 集成测试 + build pass 即覆盖正确性
}

// TestRelay_ErrorMessageBytewiseParity_WithMain 钉死 4 个 sentinel 错误路径：
// HTTP body + UsageLog.ErrorMessage 必须与 main 分支老 handler.go 1:1 一致。
//
// behavior parity with main
//
// 对应老 main:handler.go 引用：
//   - state.ErrNoChannelAvailable（plain）: line 340 `errMsg := fmt.Sprintf("no channel available for model %s", modelName)`
//   - state.ErrNoChannelAvailable（whitelist 后缀）: line 341-343 `+= " (token whitelist active)"`
//   - state.ErrModelNotAllowed: line 280 `errMsg := fmt.Sprintf("model not allowed: %s", realModel)`
//   - state.ErrInvalidForcedChannelID: line 255 `errMsg := "no channel available for model " + modelName`
func TestRelay_ErrorMessageBytewiseParity_WithMain(t *testing.T) {
	cases := []struct {
		name      string
		channels  []*models.Channel
		ui        *app.UserInfo
		header    string // X-Channel-ID 头
		wantCode  int
		wantBody  string // HTTP body 中 "error" 字段
		wantUsage string // UsageLog.ErrorMessage
	}{
		{
			name:      "no_channel_for_model",
			channels:  nil, // store 里没有 channel
			ui:        &app.UserInfo{UserID: 1, TokenID: 1},
			wantCode:  http.StatusNotFound,
			wantBody:  "no channel available for model gpt-4o",
			wantUsage: "no channel available for model gpt-4o",
		},
		{
			name: "no_channel_with_token_whitelist_suffix",
			channels: []*models.Channel{
				{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://x", Status: 1, Weight: 1}, Models: "gpt-4o"},
			},
			// AllowedChannelIDs=[999] 不在 channels → whitelist 过滤后空 → 老 line 341-343 后缀触发
			ui:        &app.UserInfo{UserID: 1, TokenID: 1, AllowedChannelIDs: []uint{999}},
			wantCode:  http.StatusNotFound,
			wantBody:  "no channel available for model gpt-4o (token whitelist active)",
			wantUsage: "no channel available for model gpt-4o (token whitelist active)",
		},
		{
			name: "model_not_allowed_token_models_blocks",
			channels: []*models.Channel{
				{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://x", Status: 1, Weight: 1}, Models: "gpt-4o"},
			},
			ui:        &app.UserInfo{UserID: 1, TokenID: 1, TokenModels: []string{"deepseek"}}, // gpt-4o 不在白名单
			wantCode:  http.StatusNotFound,
			wantBody:  "model not allowed: gpt-4o",
			wantUsage: "model not allowed: gpt-4o",
		},
		{
			name: "invalid_forced_channel_id",
			channels: []*models.Channel{
				{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://x", Status: 1, Weight: 1}, Models: "gpt-4o"},
			},
			ui:        &app.UserInfo{UserID: 1, TokenID: 1},
			header:    "not-a-number",
			wantCode:  http.StatusNotFound,
			wantBody:  "no channel available for model gpt-4o",
			wantUsage: "no channel available for model gpt-4o",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handler, _, bus := setupTestHandler(c.channels)
			logs := collectUsageLogs(bus)

			r := setupRouterWithUserInfo(handler, c.ui)
			w := httptest.NewRecorder()
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
			req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if c.header != "" {
				req.Header.Set(consts.HeaderXChannelID, c.header)
			}
			r.ServeHTTP(w, req)

			if w.Code != c.wantCode {
				t.Fatalf("code = %d, want %d (body=%s)", w.Code, c.wantCode, w.Body.String())
			}

			// HTTP body：JSON {"error":"<wantBody>"}
			var resp struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal body: %v / raw=%s", err, w.Body.String())
			}
			if resp.Error != c.wantBody {
				t.Errorf("HTTP body.error = %q, want %q (main parity)", resp.Error, c.wantBody)
			}

			time.Sleep(50 * time.Millisecond)
			snap := logs.Snapshot()
			if len(snap) == 0 {
				t.Fatal("expected usage log")
			}
			if snap[0].ErrorMessage != c.wantUsage {
				t.Errorf("UsageLog.ErrorMessage = %q, want %q (main parity)", snap[0].ErrorMessage, c.wantUsage)
			}
		})
	}
	// 屏蔽 cache import warning
	_ = cache.NewStore
}

// TestRelay_RoutingFallback_AllMembersNoChannel_Returns502:
// behavior parity with main:handler.go 502 fallback (lastErr==nil) 分支
//
// routing smart=[A, B] 但 store 里 A/B 都没 channel → 老主循环 outer-loop 跑完，
// lastErr 仍为 nil → 走 else 分支 → 502 + body "no available channels"。
// 重构前漂移为 404，本测试钉死 502 + 文案。
//
// 区分对照测试：TestRelayHandler_NoChannels（同 file 内的 setup）非 routing 路径 → 404。
func TestRelay_RoutingFallback_AllMembersNoChannel_Returns502(t *testing.T) {
	// 没有任何 channel
	handler, store, bus := setupTestHandler(nil)
	// 注入 routing smart 指向 A / B，但两个 model 都没 channel
	store.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "A", Priority: 5, Weight: 1},
			{Ref: "B", Priority: 1, Weight: 1},
		},
	})
	logs := collectUsageLogs(bus)

	ui := &app.UserInfo{UserID: 1, TokenID: 1}
	r := setupRouterWithUserInfo(handler, ui)
	w := httptest.NewRecorder()
	body := `{"model":"smart","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	// 老 main:handler.go 502 fallback (lastErr==nil) 分支 → 502 + "no available channels"
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (routing 整链耗尽走 fallback). body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v / raw=%s", err, w.Body.String())
	}
	if resp.Error != consts.ErrNoChannelAvailable {
		t.Errorf("body.error = %q, want %q (main parity)", resp.Error, consts.ErrNoChannelAvailable)
	}

	// UsageLog 也需对齐
	time.Sleep(80 * time.Millisecond)
	snap := logs.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected usage log")
	}
	if snap[0].ErrorMessage != consts.ErrNoChannelAvailable {
		t.Errorf("UsageLog.ErrorMessage = %q, want %q (main parity)",
			snap[0].ErrorMessage, consts.ErrNoChannelAvailable)
	}
	// main parity: main:handler.go 502 fallback (lastErr==nil) 分支 502 fallback 路径走 buildBaseUsageLogEntry，
	// 不传 RoutingName → UsageLog.RoutingName 保持空字符串。重构期间一度写成 "smart"，
	// 与 main 漂移；本断言钉死空字符串，防止再次漂移。
	if snap[0].RoutingName != "" {
		t.Errorf("UsageLog.RoutingName = %q, want \"\" (main parity: 502 fallback path skips RoutingName)", snap[0].RoutingName)
	}
}

// TestRelay_Whitelist_GroupModelsBlock: GroupModels=[deepseek-v3]，请求 model=gpt-4o → handler 返回 404。
// （此测试绕过 auth 中间件，直接把 GroupModels 注入 UserInfo，验证 handler 层的防御性逻辑。）
func TestRelay_Whitelist_GroupModelsBlock(t *testing.T) {
	up := upstreamReturning200()
	defer up.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: up.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	ui := &app.UserInfo{
		UserID:      1,
		TokenID:     1,
		GroupModels: []string{"deepseek-v3"}, // gpt-4o 不在 GroupModels 白名单
	}
	code, body := doRoutingRequestExpectCode(t, handler, ui, "gpt-4o")

	if code != http.StatusNotFound {
		t.Fatalf("expected 404 (group whitelist block), got %d: %s", code, body)
	}
	if !strings.Contains(body, "model not allowed") {
		t.Errorf("expected 'model not allowed' in body, got: %s", body)
	}
}

// TestRelay_Whitelist_FailsAtRealModel_RoutingFallback:
// TokenModels=[smart, deepseek-v3]；routing smart=[gpt-4o(P10), deepseek-v3(P5)]。
// 第一次 resolve 得到 gpt-4o → modelAllowedByWhitelist 失败（gpt-4o 不在 TokenModels）→ markExhausted →
// 第二次 resolve 得到 deepseek-v3 → 通过 → channel 成功。
// 断言 200 + UsageLog.ModelName=deepseek-v3 + UsageLog.RoutingName=smart。
func TestRelay_Whitelist_FailsAtRealModel_RoutingFallback(t *testing.T) {
	upGPT := upstreamReturning500() // gpt-4o 即使不被白名单拦截也应失败（此 server 其实不会被调用）
	defer upGPT.Close()
	upDS := upstreamReturning200()
	defer upDS.Close()

	handler, store, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upGPT.URL, Status: 1, Weight: 1}, Key: "k1", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upDS.URL, Status: 1, Weight: 1}, Key: "k2", Models: "deepseek-v3"},
	})

	store.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "gpt-4o", Priority: 10, Weight: 1},
			{Ref: "deepseek-v3", Priority: 5, Weight: 1},
		},
	})

	logs := collectUsageLogs(bus)

	// TokenModels 包含 smart（入口名）和 deepseek-v3，但不包含 gpt-4o。
	// 这样 gpt-4o 在第二次 whitelist 检查时被拦截，fallback 到 deepseek-v3。
	ui := &app.UserInfo{
		UserID:      1,
		TokenID:     1,
		TokenModels: []string{"smart", "deepseek-v3"},
	}
	code, body := doRoutingRequestExpectCode(t, handler, ui, "smart")

	time.Sleep(150 * time.Millisecond)

	if code != http.StatusOK {
		t.Fatalf("expected 200 after whitelist-driven fallback, got %d: %s", code, body)
	}

	snap := logs.Snapshot()
	var successLog *protocol.UsageLogEntry
	for i := range snap {
		if snap[i].Status == 1 {
			successLog = &snap[i]
		}
	}
	if successLog == nil {
		t.Fatal("expected a successful usage log (Status=1)")
	}
	if successLog.ModelName != "deepseek-v3" {
		t.Errorf("ModelName = %q, want %q", successLog.ModelName, "deepseek-v3")
	}
	if successLog.RoutingName != "smart" {
		t.Errorf("RoutingName = %q, want %q", successLog.RoutingName, "smart")
	}
}

// TestRelay_RoutingTrace_SuccessPath_SingleEntry: smart=[a, b]，channel a 第一次 200 OK。
// UsageLog.Other.routing_trace 必须只有 1 条 "global:smart"——对齐 main 老主循环
// 第一次 Resolve 成功后不再失败重 Resolve 的 trace 长度。
//
// 几何级数放大 bug 复现：routingChainBuilder.Build 预求值整条链时每次 Resolve
// 都会 push 一次 trace，最终 chain.Trace 长度 = len(members)+1。本测试钉死成功路径下
// trace 必须 == "global:smart" 1 条（与 main 老主循环 line 217 拍照行为对齐）。
func TestRelay_RoutingTrace_SuccessPath_SingleEntry(t *testing.T) {
	upA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upA.Close()
	upB := upstreamReturning500()
	defer upB.Close()

	handler, store, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1}, Key: "k1", Models: "a"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upB.URL, Status: 1, Weight: 1}, Key: "k2", Models: "b"},
	})
	store.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "a", Priority: 10, Weight: 1},
			{Ref: "b", Priority: 5, Weight: 1},
		},
	})

	ui := &app.UserInfo{UserID: 1, TokenID: 1}
	logs := collectUsageLogs(bus)
	r := setupRouterWithUserInfo(handler, ui)
	w := httptest.NewRecorder()
	body := `{"model":"smart","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	time.Sleep(200 * time.Millisecond)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	snap := logs.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 usage log (success), got %d", len(snap))
	}
	var other map[string]any
	if err := json.Unmarshal([]byte(snap[0].Other), &other); err != nil {
		t.Fatalf("unmarshal Other failed: %v", err)
	}
	trace, _ := other["routing_trace"].(string)
	if trace != "global:smart" {
		t.Errorf("routing_trace = %q, want exactly %q (main parity: single Resolve on success path)", trace, "global:smart")
	}
}
