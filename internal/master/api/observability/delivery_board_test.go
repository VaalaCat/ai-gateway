package observability

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestGetDeliveryBoardAggregates(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	db.Create(&models.Agent{AgentID: "uid-b", Name: "edge-b", Status: 1})
	appCtx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-a", "uid-b"} },
		HubCall: func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			if method != consts.RPCAgentUsageQueue {
				t.Fatalf("unexpected method %s", method)
			}
			if agentID == "uid-b" {
				return nil, errors.New("agent timeout")
			}
			return json.RawMessage(`{"store_len":3,"store_bytes":100,"retry_len":1,"retry_bytes":50,
				"oldest_ts":42,"last_success_at":99,"last_error":"","inflight":0,
				"items":[{"request_id":"r1","bytes":50,"attempts":4,"degrade_level":1,"next_at":123}]}`), nil
		},
	}

	resp, err := h.GetDeliveryBoard(appCtx, api.EmptyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 1 || len(resp.FailedAgents) != 1 {
		t.Fatalf("agents=%d failed=%d, want 1/1", len(resp.Agents), len(resp.FailedAgents))
	}
	row := resp.Agents[0]
	if row.StoreLen != 3 || row.TotalBytes != 150 || len(row.Items) != 1 {
		t.Fatalf("row mapping wrong: %+v", row)
	}
	if row.AgentID == 0 || row.AgentName != "edge-a" {
		t.Fatalf("row identity wrong: %+v", row)
	}
	if row.Items[0].RequestID != "r1" || row.Items[0].DegradeLevel != 1 || row.Items[0].NextAt != 123 {
		t.Fatalf("item mapping wrong: %+v", row.Items[0])
	}
	if resp.FailedAgents[0].AgentName != "edge-b" {
		t.Fatalf("failed agent wrong: %+v", resp.FailedAgents[0])
	}
}

func TestGetDeliveryBoard_NoOnlineAgents(t *testing.T) {
	appCtx := newTestContext(t, setupTestDB(t))
	h := &Handler{
		GetOnlineAgentIDs: func() []string { return nil },
		HubCall:           func(string, string, any, time.Duration) (json.RawMessage, error) { return nil, nil },
	}
	resp, err := h.GetDeliveryBoard(appCtx, api.EmptyRequest{})
	if err != nil || len(resp.Agents) != 0 || len(resp.FailedAgents) != 0 {
		t.Fatalf("empty case: err=%v resp=%+v", err, resp)
	}
}

func TestGetDeliveryBoard_SortedByAgentID(t *testing.T) {
	db := setupTestDB(t)
	// 故意反顺序插入,断言聚合结果按 AgentID 升序,不受完成顺序/插入顺序影响。
	db.Create(&models.Agent{AgentID: "uid-b", Name: "edge-b", Status: 1})
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	appCtx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-b", "uid-a"} },
		HubCall: func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			return json.RawMessage(`{"store_len":0,"store_bytes":0,"retry_len":0,"retry_bytes":0,
				"oldest_ts":0,"last_success_at":0,"last_error":"","inflight":0,"items":[]}`), nil
		},
	}

	resp, err := h.GetDeliveryBoard(appCtx, api.EmptyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 2 || resp.Agents[0].AgentID > resp.Agents[1].AgentID {
		t.Fatalf("agents not sorted by AgentID: %+v", resp.Agents)
	}
}

// 空旁路队列的 agent:snapshot 的 items 是 nil(JSON 缺省/null),看板行必须归一成
// 空数组——透传 null 会让前端展开行的 .map 直接崩(生产实证)。
func TestGetDeliveryBoard_NullItemsNormalizedToEmpty(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	appCtx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-a"} },
		HubCall: func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			// store 有积压但旁路为空:agent 侧 QueueSnapshot.Items 为 nil,
			// marshal 后 items 字段是 null
			return json.RawMessage(`{"store_len":5,"store_bytes":100,"retry_len":0,"retry_bytes":0,
				"oldest_ts":42,"last_success_at":99,"last_error":"","inflight":0,"items":null}`), nil
		},
	}

	resp, err := h.GetDeliveryBoard(appCtx, api.EmptyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("agents=%d, want 1", len(resp.Agents))
	}
	row := resp.Agents[0]
	if row.Items == nil {
		t.Fatal("Items must be normalized to empty slice, got nil (serializes as JSON null)")
	}
	if len(row.Items) != 0 {
		t.Fatalf("Items should be empty, got %+v", row.Items)
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"items":[]`) {
		t.Fatalf("row must serialize items as [], got %s", b)
	}
}
