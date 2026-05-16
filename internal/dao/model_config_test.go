package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func TestModelConfigDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).ModelConfig()
	m := NewAdminMutation(ctx).ModelConfig()

	mc1 := &models.ModelConfig{ModelName: "gpt-4", InputPrice: 30.0, OutputPrice: 60.0}
	mc2 := &models.ModelConfig{ModelName: "claude-3", InputPrice: 15.0, OutputPrice: 75.0}
	for _, mc := range []*models.ModelConfig{mc1, mc2} {
		if err := db.Create(mc).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("GetByID", func(t *testing.T) {
		mc, err := q.GetByID(mc1.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if mc.ModelName != "gpt-4" {
			t.Fatalf("expected gpt-4, got %s", mc.ModelName)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("GetByModelName", func(t *testing.T) {
		mc, err := q.GetByModelName("claude-3")
		if err != nil {
			t.Fatalf("GetByModelName: %v", err)
		}
		if mc.InputPrice != 15.0 {
			t.Fatalf("expected 15.0, got %f", mc.InputPrice)
		}
	})

	t.Run("List with search", func(t *testing.T) {
		configs, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, ModelConfigListFilter{Search: "gpt"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if configs[0].ModelName != "gpt-4" {
			t.Fatalf("expected gpt-4, got %s", configs[0].ModelName)
		}
	})

	t.Run("ListAll", func(t *testing.T) {
		configs, err := q.ListAll()
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(configs) != 2 {
			t.Fatalf("expected 2, got %d", len(configs))
		}
	})

	t.Run("Create", func(t *testing.T) {
		mc := &models.ModelConfig{ModelName: "new-model", InputPrice: 1.0}
		if err := m.Create(mc); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if mc.ID == 0 {
			t.Fatal("expected ID set")
		}
	})

	t.Run("Update", func(t *testing.T) {
		if err := m.Update(mc1.ID, map[string]any{"input_price": 35.0}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		mc, _ := q.GetByID(mc1.ID)
		if mc.InputPrice != 35.0 {
			t.Fatalf("expected 35.0, got %f", mc.InputPrice)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(mc1.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.GetByID(mc1.ID)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("DeleteByModelName", func(t *testing.T) {
		if err := m.DeleteByModelName("claude-3"); err != nil {
			t.Fatalf("DeleteByModelName: %v", err)
		}
		_, err := q.GetByModelName("claude-3")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})
}
