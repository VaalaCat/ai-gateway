package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestOAuthProviderDAO(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).OAuthProvider()
	m := NewAdminMutation(ctx).OAuthProvider()

	p := &models.OAuthProvider{Name: "github", DisplayName: "GitHub", ClientID: "cid", Enabled: true}
	if err := m.Create(p); err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("expected non-zero ID after Create")
	}

	t.Run("GetByID", func(t *testing.T) {
		got, err := q.GetByID(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "github" {
			t.Fatal(got.Name)
		}
	})
	t.Run("GetByName", func(t *testing.T) {
		got, err := q.GetByName("github")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != p.ID {
			t.Fatal()
		}
	})
	t.Run("ListEnabled filters", func(t *testing.T) {
		dis := &models.OAuthProvider{Name: "disabled", DisplayName: "X", Enabled: false}
		if err := m.Create(dis); err != nil {
			t.Fatal(err)
		}
		all, err := q.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 {
			t.Fatalf("List=%d", len(all))
		}
		en, err := q.ListEnabled()
		if err != nil {
			t.Fatal(err)
		}
		if len(en) != 1 {
			t.Fatalf("ListEnabled=%d", len(en))
		}
	})
	t.Run("Update", func(t *testing.T) {
		if err := m.Update(p.ID, map[string]any{"display_name": "GitHub.com"}); err != nil {
			t.Fatal(err)
		}
		got, err := q.GetByID(p.ID)
		if err != nil {
			t.Fatalf("GetByID after Update: %v", err)
		}
		if got.DisplayName != "GitHub.com" {
			t.Fatal(got.DisplayName)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := q.GetByID(p.ID); err == nil {
			t.Fatal("expected not found")
		}
	})
}
