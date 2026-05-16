package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestOAuthIdentityDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)

	u := &models.User{Username: "alice", Password: "hash", PasswordSet: true}
	db.Create(u)
	p := &models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true}
	db.Create(p)

	q := NewAdminQuery(ctx).OAuthIdentity()
	m := NewAdminMutation(ctx).OAuthIdentity()

	t.Run("Create + GetByProviderSubject", func(t *testing.T) {
		ident := &models.OAuthIdentity{UserID: u.ID, ProviderID: p.ID, Subject: "sub-1", Email: "a@b"}
		if err := m.Create(ident); err != nil {
			t.Fatal(err)
		}
		got, found, err := q.GetByProviderSubject(p.ID, "sub-1")
		if err != nil {
			t.Fatal(err)
		}
		if !found || got.UserID != u.ID {
			t.Fatalf("missed: %+v", got)
		}
	})

	t.Run("GetByProviderSubject not found", func(t *testing.T) {
		_, found, err := q.GetByProviderSubject(p.ID, "no-such")
		if err != nil {
			t.Fatal(err)
		}
		if found {
			t.Fatal("expected not found")
		}
	})

	t.Run("ListByUserID", func(t *testing.T) {
		p2 := &models.OAuthProvider{Name: "google", DisplayName: "G", Enabled: true}
		db.Create(p2)
		m.Create(&models.OAuthIdentity{UserID: u.ID, ProviderID: p2.ID, Subject: "g-sub"})
		list, err := q.ListByUserID(u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 {
			t.Fatalf("len=%d", len(list))
		}
	})

	t.Run("CountByUserID", func(t *testing.T) {
		n, err := q.CountByUserID(u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("n=%d", n)
		}
	})

	t.Run("Delete by id with user check", func(t *testing.T) {
		list, _ := q.ListByUserID(u.ID)
		id := list[0].ID
		// wrong user_id should affect 0 rows
		affected, err := m.DeleteByIDForUser(id, 9999)
		if err != nil {
			t.Fatal(err)
		}
		if affected != 0 {
			t.Fatalf("wrong-user delete should affect 0, got %d", affected)
		}
		// correct user_id should affect 1 row
		affected, err = m.DeleteByIDForUser(id, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if affected != 1 {
			t.Fatalf("correct delete should affect 1, got %d", affected)
		}
	})
}
