package dao

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestOAuthProviderDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	if err := db.Create(&models.UserGroup{ID: models.DefaultUserGroupID, Name: "default"}).Error; err != nil {
		t.Fatal(err)
	}
	q := NewAdminQuery(ctx).OAuthProvider()
	m := NewAdminMutation(ctx).OAuthProvider()

	p := &models.OAuthProvider{Name: "github", DisplayName: "GitHub", ClientID: "cid", Enabled: true}
	if err := m.Create(p); err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("expected non-zero ID after Create")
	}
	if p.DefaultGroupID != models.DefaultUserGroupID {
		t.Fatalf("default group = %d", p.DefaultGroupID)
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
	t.Run("group validation", func(t *testing.T) {
		group := &models.UserGroup{Name: "selected"}
		if err := db.Create(group).Error; err != nil {
			t.Fatal(err)
		}
		if err := m.Update(p.ID, map[string]any{"default_group_id": group.ID}); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(group).Update("status", consts.StatusDisabled).Error; err != nil {
			t.Fatal(err)
		}
		if err := m.Create(&models.OAuthProvider{Name: "bad", DefaultGroupID: group.ID}); !errors.Is(err, ErrInvalidRegistrationGroup) {
			t.Fatalf("disabled create = %v", err)
		}
		for _, invalid := range []any{uint(0), uint(999), group.ID, "1", 1.5} {
			if err := m.Update(p.ID, map[string]any{"default_group_id": invalid, "display_name": "invalid"}); !errors.Is(err, ErrInvalidRegistrationGroup) {
				t.Fatalf("invalid update %v = %v", invalid, err)
			}
		}
		got, err := q.GetByID(p.ID)
		if err != nil || got.DefaultGroupID != group.ID || got.DisplayName != "GitHub.com" {
			t.Fatalf("invalid update changed provider: %+v, %v", got, err)
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

func TestRegistrationGroup_DefaultDisabledFlagRemainsUsable(t *testing.T) {
	_, db := setupAdminContext(t)
	if err := db.Create(&models.UserGroup{ID: models.DefaultUserGroupID, Name: "default", Status: 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateRegistrationGroup(db, models.DefaultUserGroupID); err != nil {
		t.Fatalf("default group rejected: %v", err)
	}
	if err := ValidateRegistrationGroup(db, 0); !errors.Is(err, ErrInvalidRegistrationGroup) {
		t.Fatalf("zero group validation = %v", err)
	}
}
