package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func TestChannelDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Channel()
	m := NewAdminMutation(ctx).Channel()

	// seed channels
	ch1 := &models.Channel{ChannelCore: models.ChannelCore{Name: "OpenAI", Type: 1, Status: 1}, Models: "gpt-4", Tag: "premium"}
	ch2 := &models.Channel{ChannelCore: models.ChannelCore{Name: "Claude", Type: 2, Status: 1}, Models: "claude-3", Tag: "premium"}
	ch3 := &models.Channel{ChannelCore: models.ChannelCore{Name: "Disabled", Type: 1, Status: 1}, Models: "gpt-3.5", Tag: "free"}
	for _, ch := range []*models.Channel{ch1, ch2, ch3} {
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Disable ch3 via raw update to bypass gorm defaults
	db.Model(&models.Channel{}).Where("id = ?", ch3.ID).Update("status", 0)

	t.Run("GetByID", func(t *testing.T) {
		ch, err := q.GetByID(ch1.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if ch.Name != "OpenAI" {
			t.Fatalf("expected OpenAI, got %s", ch.Name)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("List with pagination", func(t *testing.T) {
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 2}, ChannelListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected total 3, got %d", total)
		}
		if len(channels) != 2 {
			t.Fatalf("expected 2 channels, got %d", len(channels))
		}
	})

	t.Run("List with search filter", func(t *testing.T) {
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, ChannelListFilter{Search: "claude"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected total 1, got %d", total)
		}
		if channels[0].Name != "Claude" {
			t.Fatalf("expected Claude, got %s", channels[0].Name)
		}
	})

	t.Run("List with type filter", func(t *testing.T) {
		tp := 1
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, ChannelListFilter{Type: &tp})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected total 2, got %d", total)
		}
		_ = channels
	})

	t.Run("List with status filter", func(t *testing.T) {
		st := 0
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, ChannelListFilter{Status: &st})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected total 1, got %d", total)
		}
		if channels[0].Name != "Disabled" {
			t.Fatalf("expected Disabled, got %s", channels[0].Name)
		}
	})

	t.Run("ListAll", func(t *testing.T) {
		channels, err := q.ListAll()
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(channels) != 3 {
			t.Fatalf("expected 3, got %d", len(channels))
		}
	})

	t.Run("ListByTag", func(t *testing.T) {
		channels, err := q.ListByTag("premium")
		if err != nil {
			t.Fatalf("ListByTag: %v", err)
		}
		if len(channels) != 2 {
			t.Fatalf("expected 2, got %d", len(channels))
		}
	})

	t.Run("ListEnabled", func(t *testing.T) {
		channels, err := q.ListEnabled()
		if err != nil {
			t.Fatalf("ListEnabled: %v", err)
		}
		if len(channels) != 2 {
			t.Fatalf("expected 2, got %d", len(channels))
		}
	})

	t.Run("Create", func(t *testing.T) {
		ch := &models.Channel{ChannelCore: models.ChannelCore{Name: "NewCh", Type: 3}}
		if err := m.Create(ch); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if ch.ID == 0 {
			t.Fatal("expected ID to be set")
		}
	})

	t.Run("Update", func(t *testing.T) {
		if err := m.Update(ch1.ID, map[string]any{"name": "OpenAI-Updated"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		ch, _ := q.GetByID(ch1.ID)
		if ch.Name != "OpenAI-Updated" {
			t.Fatalf("expected OpenAI-Updated, got %s", ch.Name)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(ch3.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.GetByID(ch3.ID)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})
}
