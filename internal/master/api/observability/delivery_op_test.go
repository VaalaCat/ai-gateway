package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// round1UsageQueueOpParams mirrors the a135b58 Agent RPC contract: queue_ids
// is unknown and ignored, while a missing/null request_ids means retry/degrade all.
type round1UsageQueueOpParams struct {
	Op         string   `json:"op"`
	RequestIDs []string `json:"request_ids,omitempty"`
	Level      int      `json:"level,omitempty"`
}

func round1AgentQueueOp(raw []byte, queued []string) ([]string, error) {
	var params round1UsageQueueOpParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if params.Op == "drop" && len(params.RequestIDs) == 0 {
		return nil, errors.New("drop requires explicit request_ids")
	}
	if params.RequestIDs == nil {
		return append([]string(nil), queued...), nil
	}
	targets := make(map[string]struct{}, len(params.RequestIDs))
	for _, id := range params.RequestIDs {
		if !strings.HasPrefix(id, "llm:") && !strings.HasPrefix(id, "api:") {
			id = "llm:" + id
		}
		targets[id] = struct{}{}
	}
	var touched []string
	for _, id := range queued {
		if _, ok := targets[id]; ok {
			touched = append(touched, id)
		}
	}
	return touched, nil
}

// newObsTestHandler 装配一个带 N 个在线 agent(按传入顺序建 DB 行,自增 ID 从 1 开始)的
// Handler + 测试 Context,供 board/op 两组测试复用,减少 setupTestDB+db.Create+newTestContext
// 的样板重复。
func newObsTestHandler(t *testing.T, agentUIDs []string) (*Handler, *app.Context) {
	t.Helper()
	db := setupTestDB(t)
	for _, uid := range agentUIDs {
		name := fmt.Sprintf("edge-%s", strings.TrimPrefix(uid, "uid-"))
		db.Create(&models.Agent{AgentID: uid, Name: name, Status: 1})
	}
	ctx := newTestContext(t, db)
	h := &Handler{GetOnlineAgentIDs: func() []string { return agentUIDs }}
	return h, ctx
}

func TestPostDeliveryOpValidation(t *testing.T) {
	h, appCtx := newObsTestHandler(t, []string{"uid-a"})
	called := false
	h.HubCall = func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{"affected":2}`), nil
	}
	// drop 不点名 → 400,不转发
	_, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "drop"})
	if err == nil || called {
		t.Fatal("drop without ids must be rejected before forwarding")
	}
	// 未知 op → 400
	if _, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "nuke"}); err == nil {
		t.Fatal("unknown op must be rejected")
	}
	// 合法 retry_now → 转发并透传 affected
	resp, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "retry_now"})
	if err != nil || resp.Affected != 2 {
		t.Fatalf("retry_now: %+v %v", resp, err)
	}
}

func TestPostDeliveryOp_DegradeValidation(t *testing.T) {
	h, appCtx := newObsTestHandler(t, []string{"uid-a"})
	called := false
	h.HubCall = func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{"affected":1}`), nil
	}
	// degrade 不带 level(零值 0)→ 400,不转发
	if _, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "degrade", RequestIDs: []string{"r1"}}); err == nil || called {
		t.Fatal("degrade without level must be rejected before forwarding")
	}
	// level=1 是 DegradeSlimBody(发送时现算的报告口径,不允许人工设置)→ 400
	if _, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "degrade", RequestIDs: []string{"r1"}, Level: 1}); err == nil || called {
		t.Fatal("degrade level=1 must be rejected")
	}
	// 合法 level=2(DegradeStripTrace)→ 转发
	resp, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "degrade", RequestIDs: []string{"r1"}, Level: 2})
	if err != nil || !called || resp.Affected != 1 {
		t.Fatalf("degrade level=2 should be forwarded: resp=%+v err=%v called=%v", resp, err, called)
	}
}

func TestPostDeliveryOpModernQueueIDsAreSafeForRound1Agent(t *testing.T) {
	for _, operation := range []string{"retry_now", "degrade", "drop"} {
		t.Run(operation, func(t *testing.T) {
			h, appCtx := newObsTestHandler(t, []string{"uid-a"})
			queued := []string{"llm:api:shared", "api:shared"}
			var touched []string
			h.HubCall = func(_ string, _ string, params any, _ time.Duration) (json.RawMessage, error) {
				raw, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				var decoded map[string]json.RawMessage
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				if string(decoded["request_ids"]) != "[]" {
					t.Fatalf("round1 safety requires explicit request_ids: [], got %s", decoded["request_ids"])
				}
				if string(decoded["queue_ids"]) != `["api:shared"]` {
					t.Fatalf("current Agent requires exact queue_ids, got %s", decoded["queue_ids"])
				}
				touched, err = round1AgentQueueOp(raw, queued)
				if err != nil {
					return nil, err
				}
				return json.RawMessage(fmt.Sprintf(`{"affected":%d}`, len(touched))), nil
			}

			level := 0
			if operation == "degrade" {
				level = 2
			}
			response, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{
				AgentID: 1, Op: operation, QueueIDs: []string{"api:shared"},
				RequestIDs: []string{"legacy-extra-must-not-union"}, Level: level,
			})
			if operation == "drop" {
				if err == nil {
					t.Fatal("round1 drop must fail closed instead of pretending success")
				}
			} else if err != nil || response.Affected != 0 {
				t.Fatalf("round1 %s must be a safe no-op: response=%+v err=%v", operation, response, err)
			}
			if len(touched) != 0 {
				t.Fatalf("round1 %s touched %v, want none", operation, touched)
			}
		})
	}
}

func TestPostDeliveryOpPreservesBareLegacyRequestIDs(t *testing.T) {
	for _, operation := range []string{"retry_now", "degrade", "drop"} {
		t.Run(operation, func(t *testing.T) {
			h, appCtx := newObsTestHandler(t, []string{"uid-a"})
			appCtx.Logger = zap.NewNop()
			var forwardedQueueIDs, forwardedRequestIDs []string
			h.HubCall = func(_ string, _ string, params any, _ time.Duration) (json.RawMessage, error) {
				payload := params.(map[string]any)
				forwardedQueueIDs, _ = payload["queue_ids"].([]string)
				forwardedRequestIDs, _ = payload["request_ids"].([]string)
				return json.RawMessage(`{"affected":1}`), nil
			}
			level := 0
			if operation == "degrade" {
				level = 2
			}
			response, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{
				AgentID: 1, Op: operation, RequestIDs: []string{"legacy-raw"}, Level: level,
			})
			if err != nil || response.Affected != 1 {
				t.Fatalf("legacy %s: response=%+v err=%v", operation, response, err)
			}
			if forwardedQueueIDs != nil || len(forwardedRequestIDs) != 1 || forwardedRequestIDs[0] != "legacy-raw" {
				t.Fatalf("legacy targets changed: queue_ids=%v request_ids=%v", forwardedQueueIDs, forwardedRequestIDs)
			}
		})
	}
}

func TestPostDeliveryOpRejectsRound1TypedRequestIDsBeforeFanout(t *testing.T) {
	for _, operation := range []string{"retry_now", "degrade", "drop"} {
		for _, requestID := range []string{"llm:shared", "api:shared"} {
			t.Run(operation+"/"+requestID[:3], func(t *testing.T) {
				h, appCtx := newObsTestHandler(t, []string{"uid-a"})
				core, logs := observer.New(zap.WarnLevel)
				appCtx.Logger = zap.New(core)
				calls := 0
				h.HubCall = func(_ string, _ string, _ any, _ time.Duration) (json.RawMessage, error) {
					calls++
					return json.RawMessage(`{"affected":1}`), nil
				}
				level := 0
				if operation == "degrade" {
					level = 2
				}
				_, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{
					AgentID: 1, Op: operation, RequestIDs: []string{requestID}, Level: level,
				})
				var apiErr *api.APIError
				if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || !strings.Contains(apiErr.Message, "refresh") {
					t.Fatalf("error = %#v, want 400 refresh guidance", err)
				}
				if calls != 0 {
					t.Fatalf("HubCall calls = %d, want 0", calls)
				}
				if entries := logs.FilterMessage("delivery-op drop executed").All(); len(entries) != 0 {
					t.Fatalf("rejected stale request wrote %d success audit entries", len(entries))
				}
			})
		}
	}
}

func TestPostDeliveryOp_UnknownAgent(t *testing.T) {
	h, appCtx := newObsTestHandler(t, []string{"uid-a"})
	called := false
	h.HubCall = func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{"affected":1}`), nil
	}
	if _, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 999, Op: "retry_now"}); err == nil || called {
		t.Fatal("op against unknown agent id must be rejected before forwarding")
	}
}

// TestPostDeliveryOp_DropAuditLog 验证 drop 成功执行后落一条 warn 审计日志,带操作者
// UserID + agent_id + request_ids + affected——人工丢弃计费数据必须可追责。
func TestPostDeliveryOp_DropAuditLog(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	h, appCtx := newObsTestHandler(t, []string{"uid-a"})
	appCtx.Logger = logger
	appCtx.UserInfo = &app.UserInfo{UserID: 42}
	h.HubCall = func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		return json.RawMessage(`{"affected":2}`), nil
	}

	resp, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "drop", RequestIDs: []string{"r1", "r2"}})
	if err != nil || resp.Affected != 2 {
		t.Fatalf("drop: %+v %v", resp, err)
	}

	entries := logs.FilterMessage("delivery-op drop executed").All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit log entry, got %d: %+v", len(entries), entries)
	}
	fields := entries[0].ContextMap()
	if fields["operator_user_id"] != uint64(42) {
		t.Fatalf("operator_user_id = %v (%T), want 42", fields["operator_user_id"], fields["operator_user_id"])
	}
	if fields["agent_id"] != uint64(1) {
		t.Fatalf("agent_id = %v (%T), want 1", fields["agent_id"], fields["agent_id"])
	}
	ids, ok := fields["request_ids"].([]interface{})
	if !ok || len(ids) != 2 || ids[0] != "r1" || ids[1] != "r2" {
		t.Fatalf("request_ids = %v", fields["request_ids"])
	}
	if fields["affected"] != int64(2) {
		t.Fatalf("affected = %v (%T), want 2", fields["affected"], fields["affected"])
	}
}

// TestPostDeliveryOp_NoAuditLogWithoutUserInfo 验证审计不因 c.UserInfo 为 nil 而 panic,
// operator_user_id 兜底为 0。
func TestPostDeliveryOp_NoAuditLogWithoutUserInfo(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	h, appCtx := newObsTestHandler(t, []string{"uid-a"})
	appCtx.Logger = logger
	appCtx.UserInfo = nil
	h.HubCall = func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		return json.RawMessage(`{"affected":1}`), nil
	}

	if _, err := h.PostDeliveryOp(appCtx, DeliveryOpRequest{AgentID: 1, Op: "drop", RequestIDs: []string{"r1"}}); err != nil {
		t.Fatalf("drop with nil UserInfo should not error: %v", err)
	}

	entries := logs.FilterMessage("delivery-op drop executed").All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit log entry, got %d", len(entries))
	}
	if fields := entries[0].ContextMap(); fields["operator_user_id"] != uint64(0) {
		t.Fatalf("operator_user_id = %v, want 0", fields["operator_user_id"])
	}
}
