package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Agent()
	m := NewAdminMutation(ctx).Agent()

	a1 := &models.Agent{AgentID: "agent-1", Name: "Agent One", Status: 1}
	a2 := &models.Agent{AgentID: "agent-2", Name: "Agent Two", Status: 1}
	a3 := &models.Agent{AgentID: "agent-3", Name: "Inactive", Status: 1}
	for _, a := range []*models.Agent{a1, a2, a3} {
		if err := db.Create(a).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	db.Model(&models.Agent{}).Where("id = ?", a3.ID).Update("status", 0)

	t.Run("GetByID", func(t *testing.T) {
		a, err := q.GetByID(a1.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if a.AgentID != "agent-1" {
			t.Fatalf("expected agent-1, got %s", a.AgentID)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("GetByAgentID", func(t *testing.T) {
		a, err := q.GetByAgentID("agent-2")
		if err != nil {
			t.Fatalf("GetByAgentID: %v", err)
		}
		if a.Name != "Agent Two" {
			t.Fatalf("expected Agent Two, got %s", a.Name)
		}
	})

	t.Run("List with search", func(t *testing.T) {
		agents, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, AgentListFilter{Search: "One"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if agents[0].Name != "Agent One" {
			t.Fatalf("expected Agent One, got %s", agents[0].Name)
		}
	})

	t.Run("List with status filter", func(t *testing.T) {
		st := 0
		agents, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, AgentListFilter{Status: &st})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		_ = agents
	})

	t.Run("ListByAgentIDs", func(t *testing.T) {
		agents, err := q.ListByAgentIDs([]string{"agent-1", "agent-3"})
		if err != nil {
			t.Fatalf("ListByAgentIDs: %v", err)
		}
		if len(agents) != 2 {
			t.Fatalf("expected 2, got %d", len(agents))
		}
	})

	t.Run("ListActive", func(t *testing.T) {
		agents, err := q.ListActive("agent-1")
		if err != nil {
			t.Fatalf("ListActive: %v", err)
		}
		if len(agents) != 1 {
			t.Fatalf("expected 1 (agent-2), got %d", len(agents))
		}
		if agents[0].AgentID != "agent-2" {
			t.Fatalf("expected agent-2, got %s", agents[0].AgentID)
		}
	})

	t.Run("Create", func(t *testing.T) {
		a := &models.Agent{AgentID: "agent-new", Name: "New"}
		if err := m.Create(a); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if a.ID == 0 {
			t.Fatal("expected ID set")
		}
	})

	t.Run("Update", func(t *testing.T) {
		if err := m.Update(a1.ID, map[string]any{"name": "Updated"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		a, _ := q.GetByID(a1.ID)
		if a.Name != "Updated" {
			t.Fatalf("expected Updated, got %s", a.Name)
		}
	})

	t.Run("UpdateLastSeen", func(t *testing.T) {
		if err := m.UpdateLastSeen("agent-1", 123456); err != nil {
			t.Fatalf("UpdateLastSeen: %v", err)
		}
		a, _ := q.GetByAgentID("agent-1")
		if a.LastSeen != 123456 {
			t.Fatalf("expected 123456, got %d", a.LastSeen)
		}
	})

	t.Run("UpdateHTTPAddresses", func(t *testing.T) {
		if err := m.UpdateHTTPAddresses("agent-1", `[{"url":"http://localhost"}]`); err != nil {
			t.Fatalf("UpdateHTTPAddresses: %v", err)
		}
		a, _ := q.GetByAgentID("agent-1")
		if a.HTTPAddresses != `[{"url":"http://localhost"}]` {
			t.Fatalf("unexpected addresses: %s", a.HTTPAddresses)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(a3.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.GetByID(a3.ID)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})
}

func TestBatchUpdateLastSeen(t *testing.T) {
	ctx, db := setupAdminContext(t)

	// 准备 3 个 agent
	require.NoError(t, db.Create(&models.Agent{AgentID: "a1", Name: "a1", Status: 1}).Error)
	require.NoError(t, db.Create(&models.Agent{AgentID: "a2", Name: "a2", Status: 1}).Error)
	require.NoError(t, db.Create(&models.Agent{AgentID: "a3", Name: "a3", Status: 1}).Error)

	m := NewAdminMutation(ctx)

	// success: 批量更新两个 agent
	err := m.Agent().BatchUpdateLastSeen(map[string]int64{"a1": 1000, "a2": 2000})
	require.NoError(t, err)

	var a1, a2, a3 models.Agent
	require.NoError(t, db.Where("agent_id = ?", "a1").First(&a1).Error)
	require.NoError(t, db.Where("agent_id = ?", "a2").First(&a2).Error)
	require.NoError(t, db.Where("agent_id = ?", "a3").First(&a3).Error)
	require.Equal(t, int64(1000), a1.LastSeen)
	require.Equal(t, int64(2000), a2.LastSeen)
	require.Equal(t, int64(0), a3.LastSeen, "未在 updates 中的 agent 不应被改")

	// boundary: 空 map 立即返回 nil
	err = m.Agent().BatchUpdateLastSeen(map[string]int64{})
	require.NoError(t, err)

	// failure: 含不存在的 agent_id 不报错（affected=0），其他 agent 正常
	err = m.Agent().BatchUpdateLastSeen(map[string]int64{"a1": 3000, "ghost": 999})
	require.NoError(t, err)
	require.NoError(t, db.Where("agent_id = ?", "a1").First(&a1).Error)
	require.Equal(t, int64(3000), a1.LastSeen)
}
