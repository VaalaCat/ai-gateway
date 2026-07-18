package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
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
	db.Create(&models.Agent{AgentID: "uid-c", Name: "edge-c", Status: 1, LastSeen: 9_999})
	ctx := newTestContext(t, db)
	control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
		"uid-a": {Generation: 11, ConnectedAt: 900, HeartbeatAt: 990},
		"uid-b": {Generation: 12, ConnectedAt: 900, HeartbeatAt: 990},
	}}

	var mu sync.Mutex
	calls := make(map[string]uint64)
	h := &Handler{
		Connections:       connectivity.NewService("epoch-all-inflight", connectivity.Sources{Control: control}, connectivity.Options{}),
		GetOnlineAgentIDs: func() []string { return []string{"uid-a", "uid-b", "uid-c"} },
		HubCallSession: func(agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			mu.Lock()
			calls[agentID] = generation
			mu.Unlock()
			if agentID == "uid-a" {
				return json.Marshal([]inflight.Snapshot{{ID: 7, ReqID: "r1", View: protocol.UsageLogEntry{ModelName: "gpt-4o"}, Stage: "upstream", ElapsedMs: 1200}})
			}
			if agentID == "uid-b" {
				return nil, fmt.Errorf("private stale detail: %w", connectivity.ErrConnectionGenerationChanged)
			}
			return nil, assertErr("disconnected agent must not be called")
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
	if len(resp.FailedAgents) != 2 {
		t.Fatalf("failed_agents = %+v, want edge-b and edge-c", resp.FailedAgents)
	}
	failed := make(map[string]string, len(resp.FailedAgents))
	for _, item := range resp.FailedAgents {
		failed[item.AgentName] = item.Error
	}
	if failed["edge-b"] != connectivity.ErrorCodeConnectionGenerationChanged {
		t.Fatalf("edge-b error = %q", failed["edge-b"])
	}
	if failed["edge-c"] != connectivity.DenialControlDisconnected {
		t.Fatalf("edge-c error = %q", failed["edge-c"])
	}
	if calls["uid-a"] != 11 || calls["uid-b"] != 12 {
		t.Fatalf("generation calls = %+v", calls)
	}
	if _, called := calls["uid-c"]; called {
		t.Fatalf("disconnected uid-c was called: %+v", calls)
	}
}

func TestGetAllInflight_OrdinaryFailuresStayIsolatedAndRedacted(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		err  error
		want string
	}{
		{name: "rpc failure", err: assertErr("secret inflight upstream body"), want: "agent operation failed"},
		{name: "invalid response", raw: json.RawMessage(`secret invalid response`), want: "invalid agent response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			require.NoError(t, db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1}).Error)
			ctx := newTestContext(t, db)
			h := &Handler{
				Connections: connectivity.NewService("epoch-redacted", connectivity.Sources{Control: &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
					"uid-a": {Generation: 11, ConnectedAt: 900, HeartbeatAt: 990},
				}}}, connectivity.Options{}),
				GetOnlineAgentIDs: func() []string { return []string{"uid-a"} },
				HubCallSession: func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
					return tt.raw, tt.err
				},
			}

			response, err := h.GetAllInflight(ctx, api.EmptyRequest{})
			require.NoError(t, err)
			require.Empty(t, response.Requests)
			require.Len(t, response.FailedAgents, 1)
			require.Equal(t, "edge-a", response.FailedAgents[0].AgentName)
			require.Equal(t, tt.want, response.FailedAgents[0].Error)
			require.NotContains(t, response.FailedAgents[0].Error, "secret")
		})
	}
}

func TestGetAllInflight_NoOnlineAgents(t *testing.T) {
	ctx := newTestContext(t, setupTestDB(t))
	h := &Handler{
		GetOnlineAgentIDs: func() []string { return nil },
		Connections:       connectivity.NewService("epoch-empty", connectivity.Sources{}, connectivity.Options{}),
		HubCallSession:    func(string, uint64, string, any, time.Duration) (json.RawMessage, error) { return nil, nil },
	}
	resp, err := h.GetAllInflight(ctx, api.EmptyRequest{})
	if err != nil || len(resp.Requests) != 0 || len(resp.FailedAgents) != 0 {
		t.Fatalf("empty case: err=%v resp=%+v", err, resp)
	}
}

func TestGetAllInflight_CanceledRequestPerformsNoRPC(t *testing.T) {
	db := setupTestDB(t)
	require.NoError(t, db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1}).Error)
	ctx := newTestContext(t, db)
	requestCtx, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
		"uid-a": {Generation: 11, ConnectedAt: 900, HeartbeatAt: 990},
	}}
	var calls atomic.Int32
	h := &Handler{
		Connections:       connectivity.NewService("epoch-canceled", connectivity.Sources{Control: control}, connectivity.Options{}),
		GetOnlineAgentIDs: func() []string { return []string{"uid-a"} },
		HubCallSession: func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
			calls.Add(1)
			return json.RawMessage(`[]`), nil
		},
	}
	_, err := h.GetAllInflight(ctx, api.EmptyRequest{})
	apiErr := requireAPIError(t, err)
	require.Equal(t, 408, apiErr.Status)
	require.Zero(t, calls.Load())
}

func TestGetAllInflight_NilDependenciesFailClosed(t *testing.T) {
	ctx := newTestContext(t, setupTestDB(t))
	h := &Handler{GetOnlineAgentIDs: func() []string { return []string{"uid-a"} }}
	_, err := h.GetAllInflight(ctx, api.EmptyRequest{})
	require.Equal(t, 500, requireAPIError(t, err).Status)
}

func TestInterrupt_PassesThroughToAgent(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	ctx := newTestContext(t, db)

	var gotMethod string
	var gotParams any
	h := &Handler{
		Connections: connectivity.NewService("epoch-interrupt-compat", connectivity.Sources{Control: &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
			"uid-a": {Generation: 9, ConnectedAt: 900, HeartbeatAt: 990},
		}}}, connectivity.Options{}),
		HubCallSession: func(agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			if generation != 9 {
				t.Fatalf("generation = %d, want 9", generation)
			}
			gotMethod, gotParams = method, params
			return json.Marshal(map[string]bool{"interrupted": true})
		},
	}
	attachTestOperations(h, ctx)

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
	h := &Handler{
		Connections:    connectivity.NewService("epoch-missing", connectivity.Sources{}, connectivity.Options{}),
		HubCallSession: func(string, uint64, string, any, time.Duration) (json.RawMessage, error) { return nil, nil },
	}
	if _, err := h.Interrupt(ctx, InterruptRequest{AgentID: 999, ID: 1}); err == nil {
		t.Fatalf("missing agent should error")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
