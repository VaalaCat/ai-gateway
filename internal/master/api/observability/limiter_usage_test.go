package observability

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestContext(t *testing.T, db *gorm.DB) *app.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = httptest.NewRequest("GET", "/", nil)
	testApp := app.NewApplication()
	testApp.SetCoreDB(db)
	return &app.Context{Context: ginCtx, App: testApp}
}

func TestLimiterUsage_AggregatesAcrossAgents(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	db.Create(&models.Agent{AgentID: "uid-b", Name: "edge-b", Status: 1})
	ctx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-a", "uid-b"} },
		HubCall: func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			return json.Marshal([]protocol.LimiterBucketStat{
				{LimiterID: 7, Bucket: "u:1", Metric: "concurrency", Occupied: 1, Capacity: 5},
			})
		},
	}

	resp, err := h.GetLimiterUsage(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(resp.Buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1", len(resp.Buckets))
	}
	row := resp.Buckets[0]
	if row.LimiterID != 7 || row.Bucket != "u:1" {
		t.Fatalf("row key = (%d,%q), want (7,u:1)", row.LimiterID, row.Bucket)
	}
	if row.Occupied != 2 {
		t.Fatalf("Occupied = %d, want 2", row.Occupied)
	}
	if row.Capacity != 10 {
		t.Fatalf("Capacity = %d, want 10", row.Capacity)
	}
	if len(row.PerAgent) != 2 {
		t.Fatalf("PerAgent len = %d, want 2", len(row.PerAgent))
	}
}

func TestLimiterUsage_NoOnlineAgents(t *testing.T) {
	ctx := newTestContext(t, setupTestDB(t))
	h := &Handler{
		GetOnlineAgentIDs: func() []string { return nil },
		HubCall:           func(string, string, any, time.Duration) (json.RawMessage, error) { return nil, nil },
	}
	resp, err := h.GetLimiterUsage(ctx, api.EmptyRequest{})
	if err != nil || len(resp.Buckets) != 0 || len(resp.FailedAgents) != 0 {
		t.Fatalf("empty case: err=%v resp=%+v", err, resp)
	}
}

func TestLimiterUsage_IsolatesNodeFailure(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	db.Create(&models.Agent{AgentID: "uid-b", Name: "edge-b", Status: 1})
	ctx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-a", "uid-b"} },
		HubCall: func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			if agentID == "uid-a" {
				return json.Marshal([]protocol.LimiterBucketStat{
					{LimiterID: 7, Bucket: "u:1", Metric: "concurrency", Occupied: 3, Capacity: 5},
				})
			}
			return nil, testErr("node down")
		},
	}

	resp, err := h.GetLimiterUsage(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(resp.Buckets) != 1 || resp.Buckets[0].Occupied != 3 {
		t.Fatalf("buckets = %+v, want one row occupied=3", resp.Buckets)
	}
	if len(resp.FailedAgents) != 1 || resp.FailedAgents[0].AgentName != "edge-b" {
		t.Fatalf("failed_agents = %+v, want one edge-b", resp.FailedAgents)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

// TestSortLimiterResponse_Deterministic 验证聚合结果被确定性排序:
// Buckets 按 (LimiterID,Bucket)、PerAgent 按 AgentID、FailedAgents 按 AgentID。
// 修的是"限流 Tab 每次刷新行乱跳"(map/agent 完成顺序不定)。
func TestSortLimiterResponse_Deterministic(t *testing.T) {
	resp := &LimiterUsageResponse{
		Buckets: []LimiterBucketRow{
			{LimiterBucketStat: protocol.LimiterBucketStat{LimiterID: 2, Bucket: "b"},
				PerAgent: []AgentBucket{{AgentID: 3}, {AgentID: 1}}},
			{LimiterBucketStat: protocol.LimiterBucketStat{LimiterID: 1, Bucket: "z"}},
			{LimiterBucketStat: protocol.LimiterBucketStat{LimiterID: 1, Bucket: "a"}},
		},
		FailedAgents: []FailedAgent{{AgentID: 9}, {AgentID: 4}},
	}

	sortLimiterResponse(resp)

	type bk struct {
		id uint
		b  string
	}
	got := make([]bk, 0, len(resp.Buckets))
	for _, b := range resp.Buckets {
		got = append(got, bk{b.LimiterID, b.Bucket})
	}
	want := []bk{{1, "a"}, {1, "z"}, {2, "b"}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Buckets[%d] = %v, want %v (full: %v)", i, got[i], want[i], got)
		}
	}

	pa := resp.Buckets[2].PerAgent // (2,"b") 行
	if len(pa) != 2 || pa[0].AgentID != 1 || pa[1].AgentID != 3 {
		t.Fatalf("PerAgent not sorted by AgentID: %+v", pa)
	}
	if resp.FailedAgents[0].AgentID != 4 || resp.FailedAgents[1].AgentID != 9 {
		t.Fatalf("FailedAgents not sorted by AgentID: %+v", resp.FailedAgents)
	}
}
