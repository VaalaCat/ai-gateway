package observability

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestBreakerBoard_AggregatesAcrossAgents(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	db.Create(&models.Agent{AgentID: "uid-b", Name: "edge-b", Status: 1})
	ctx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-a", "uid-b"} },
		HubCallContext: func(_ context.Context, agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			// 同一渠道 admin/7:edge-a 上 open,edge-b 上 closed
			st := "closed"
			if agentID == "uid-a" {
				st = "open"
			}
			return json.Marshal([]map[string]any{
				{"source": "admin", "channel_id": 7, "state": st,
					"remaining_ms": 1200, "failures": 3, "successes": 0, "failure_rate": 1.0},
			})
		},
	}

	resp, err := h.GetBreakerBoard(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(resp.Channels) != 1 {
		t.Fatalf("channels len = %d, want 1 (grouped): %+v", len(resp.Channels), resp.Channels)
	}
	row := resp.Channels[0]
	if row.Source != "admin" || row.ChannelID != 7 {
		t.Fatalf("key = (%s,%d), want (admin,7)", row.Source, row.ChannelID)
	}
	if row.WorstState != "open" {
		t.Fatalf("WorstState = %q, want open", row.WorstState)
	}
	if row.OpenAgents != 1 || row.TotalAgents != 2 {
		t.Fatalf("open/total = %d/%d, want 1/2", row.OpenAgents, row.TotalAgents)
	}
	if len(row.Agents) != 2 || row.Agents[0].AgentID > row.Agents[1].AgentID {
		t.Fatalf("agents not sorted by AgentID: %+v", row.Agents)
	}
}

func TestBreakerBoard_IsolatesNodeFailure(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "uid-a", Name: "edge-a", Status: 1})
	db.Create(&models.Agent{AgentID: "uid-b", Name: "edge-b", Status: 1})
	ctx := newTestContext(t, db)

	h := &Handler{
		GetOnlineAgentIDs: func() []string { return []string{"uid-a", "uid-b"} },
		HubCallContext: func(_ context.Context, agentID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
			if agentID == "uid-a" {
				return json.Marshal([]map[string]any{
					{"source": "admin", "channel_id": 7, "state": "open"},
				})
			}
			return nil, testErr("node down")
		},
	}

	resp, err := h.GetBreakerBoard(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(resp.Channels) != 1 || resp.Channels[0].WorstState != "open" {
		t.Fatalf("channels = %+v, want one open row", resp.Channels)
	}
	if len(resp.FailedAgents) != 1 || resp.FailedAgents[0].AgentName != "edge-b" {
		t.Fatalf("failed_agents = %+v, want one edge-b", resp.FailedAgents)
	}
}

func TestSummarizeBreakerAgents_WorstAndOpenCount(t *testing.T) {
	worst, open := summarizeBreakerAgents([]AgentBreakerCell{
		{State: "closed"}, {State: "half-open"}, {State: "open"}, {State: "closed"},
	})
	if worst != "open" || open != 2 {
		t.Fatalf("worst=%q open=%d, want open/2", worst, open)
	}
	worst2, open2 := summarizeBreakerAgents([]AgentBreakerCell{{State: "closed"}, {State: "half-open"}})
	if worst2 != "half-open" || open2 != 1 {
		t.Fatalf("worst=%q open=%d, want half-open/1", worst2, open2)
	}
	worst3, open3 := summarizeBreakerAgents([]AgentBreakerCell{{State: "closed"}, {State: "closed"}})
	if worst3 != "closed" || open3 != 0 {
		t.Fatalf("worst=%q open=%d, want closed/0", worst3, open3)
	}
}
