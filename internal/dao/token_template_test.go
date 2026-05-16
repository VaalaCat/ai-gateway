package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func TestTokenTemplateDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).TokenTemplate()
	m := NewAdminMutation(ctx).TokenTemplate()

	tpl1 := &models.TokenTemplate{Name: "Basic", Models: `["gpt-4o"]`, ExpiryDays: 30, Status: 1}
	tpl2 := &models.TokenTemplate{Name: "Premium", Models: `["*"]`, ExpiryDays: -1, Status: 1}
	tpl3 := &models.TokenTemplate{Name: "Disabled", Models: `["gpt-3.5"]`, ExpiryDays: 7, Status: 1}
	for _, tpl := range []*models.TokenTemplate{tpl1, tpl2, tpl3} {
		if err := db.Create(tpl).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Disable tpl3 after creation to bypass GORM default
	db.Model(tpl3).Update("status", 0)

	t.Run("GetByID", func(t *testing.T) {
		tpl, err := q.GetByID(tpl1.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if tpl.Name != "Basic" {
			t.Fatalf("expected Basic, got %s", tpl.Name)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("List all", func(t *testing.T) {
		templates, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, TokenTemplateListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		_ = templates
	})

	t.Run("List with status filter", func(t *testing.T) {
		status := 1
		templates, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, TokenTemplateListFilter{Status: &status})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2 enabled, got %d", total)
		}
		_ = templates
	})

	t.Run("List with search", func(t *testing.T) {
		templates, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, TokenTemplateListFilter{Search: "Prem"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		_ = templates
	})

	t.Run("Create", func(t *testing.T) {
		tpl := &models.TokenTemplate{Name: "New Template", Models: `["claude-*"]`, ExpiryDays: 60, Status: 1}
		if err := m.Create(tpl); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if tpl.ID == 0 {
			t.Fatal("expected ID set")
		}
	})

	t.Run("Update", func(t *testing.T) {
		if err := m.Update(tpl1.ID, map[string]any{"name": "Updated Basic"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		tpl, _ := q.GetByID(tpl1.ID)
		if tpl.Name != "Updated Basic" {
			t.Fatalf("expected Updated Basic, got %s", tpl.Name)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(tpl3.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.GetByID(tpl3.ID)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})
}
