package agent

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
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
	testApp.SetDB(db)
	return &app.Context{Context: ginCtx, App: testApp}
}

func TestGetAllInflight_MergesAndIsolatesFailures(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	db.Create(&models.Agent{AgentID: "uid-b", Name: "edge-b", Status: 1})
	ctx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-a", "uid-b"} },
		HubCall: func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			if agentID == "uid-a" {
				return json.Marshal([]inflight.Snapshot{{ID: 7, ReqID: "r1", View: protocol.UsageLogEntry{ModelName: "gpt-4o"}, Stage: "upstream", ElapsedMs: 1200}})
			}
			return nil, assertErr("node down")
		},
	}

	resp, err := h.GetAllInflight(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(resp.Requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(resp.Requests))
	}
	row := resp.Requests[0]
	if row.ID != 7 || row.AgentName != "edge-a" || row.View.ModelName != "gpt-4o" {
		t.Fatalf("row = %+v, want id=7 agent=edge-a model=gpt-4o", row)
	}
	if len(resp.FailedAgents) != 1 || resp.FailedAgents[0].AgentName != "edge-b" {
		t.Fatalf("failed_agents = %+v, want one edge-b", resp.FailedAgents)
	}
}

func TestGetAllInflight_NoOnlineAgents(t *testing.T) {
	ctx := newTestContext(t, setupTestDB(t))
	h := &Handler{
		GetOnlineAgentIDs: func() []string { return nil },
		HubCall:           func(string, string, any, time.Duration) (json.RawMessage, error) { return nil, nil },
	}
	resp, err := h.GetAllInflight(ctx, api.EmptyRequest{})
	if err != nil || len(resp.Requests) != 0 || len(resp.FailedAgents) != 0 {
		t.Fatalf("empty case: err=%v resp=%+v", err, resp)
	}
}

func TestInterrupt_PassesThroughToAgent(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	ctx := newTestContext(t, db)

	var gotMethod string
	var gotParams any
	h := &Handler{
		HubCall: func(agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			gotMethod, gotParams = method, params
			return json.Marshal(map[string]bool{"interrupted": true})
		},
	}

	res, err := h.Interrupt(ctx, InterruptRequest{AgentID: 1, ID: 7})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !res.Interrupted {
		t.Fatalf("Interrupted = false, want true")
	}
	if gotMethod != "agent.interrupt" {
		t.Fatalf("method = %q, want agent.interrupt", gotMethod)
	}
	if m, ok := gotParams.(map[string]any); !ok || m["id"] != int64(7) {
		t.Fatalf("params = %+v, want {id:7}", gotParams)
	}
}

func TestInterrupt_AgentNotFound(t *testing.T) {
	ctx := newTestContext(t, setupTestDB(t))
	h := &Handler{HubCall: func(string, string, any, time.Duration) (json.RawMessage, error) { return nil, nil }}
	if _, err := h.Interrupt(ctx, InterruptRequest{AgentID: 999, ID: 1}); err == nil {
		t.Fatalf("missing agent should error")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
