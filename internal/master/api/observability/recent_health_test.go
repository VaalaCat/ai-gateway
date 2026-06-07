package observability

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestGetRecentHealth(t *testing.T) {
	db := setupTestDB(t)
	now := time.Now().Unix()
	db.Create(&models.UsageLog{AgentID: "a1", Status: 1, CreatedAt: now - 10, RequestID: "x1"})
	db.Create(&models.UsageLog{AgentID: "a1", Status: 1, CreatedAt: now - 20, RequestID: "x2"})
	db.Create(&models.UsageLog{AgentID: "a1", Status: 0, CreatedAt: now - 30, RequestID: "x3"})
	db.Create(&models.UsageLog{AgentID: "a1", Status: 1, CreatedAt: now - 99999, RequestID: "old"})
	ctx := newTestContext(t, db)

	h := &Handler{}
	resp, err := h.GetRecentHealth(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.WindowSecs != 300 {
		t.Fatalf("WindowSecs = %d, want 300", resp.WindowSecs)
	}

	var a1 *AgentHealthRow
	for i := range resp.Agents {
		if resp.Agents[i].AgentID == "a1" {
			a1 = &resp.Agents[i]
		}
	}
	if a1 == nil {
		t.Fatalf("no row for agent a1 in %+v", resp.Agents)
	}
	if a1.Requests != 3 {
		t.Fatalf("Requests = %d, want 3", a1.Requests)
	}
	if a1.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", a1.Failed)
	}
	if a1.WindowSecs != 300 {
		t.Fatalf("row WindowSecs = %d, want 300", a1.WindowSecs)
	}
	if a1.ErrorRate < 0.33 || a1.ErrorRate > 0.34 {
		t.Fatalf("ErrorRate = %v, want ~0.333", a1.ErrorRate)
	}
}
