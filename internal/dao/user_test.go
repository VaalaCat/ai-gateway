package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

func TestUserDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).User()
	m := NewAdminMutation(ctx).User()

	u1 := &models.User{Username: "alice", Password: "hash1", Role: 1, Quota: 1000}
	u2 := &models.User{Username: "bob", Password: "hash2", Role: 100}
	for _, u := range []*models.User{u1, u2} {
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("GetByID", func(t *testing.T) {
		u, err := q.GetByID(u1.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if u.Username != "alice" {
			t.Fatalf("expected alice, got %s", u.Username)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("GetByUsername", func(t *testing.T) {
		u, err := q.GetByUsername("bob")
		if err != nil {
			t.Fatalf("GetByUsername: %v", err)
		}
		if u.Role != 100 {
			t.Fatalf("expected role 100, got %d", u.Role)
		}
	})

	t.Run("List all", func(t *testing.T) {
		users, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, UserListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		_ = users
	})

	t.Run("List with search", func(t *testing.T) {
		users, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, UserListFilter{Search: "ali"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		_ = users
	})

	t.Run("List with role filter", func(t *testing.T) {
		role := 100
		users, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, UserListFilter{Role: &role})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		_ = users
	})

	t.Run("Create", func(t *testing.T) {
		u := &models.User{Username: "charlie", Password: "hash3"}
		if err := m.Create(u); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if u.ID == 0 {
			t.Fatal("expected ID set")
		}
	})

	t.Run("Update", func(t *testing.T) {
		if err := m.Update(u1.ID, map[string]any{"username": "alice_updated"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		u, _ := q.GetByID(u1.ID)
		if u.Username != "alice_updated" {
			t.Fatalf("expected alice_updated, got %s", u.Username)
		}
	})

	t.Run("UpdateQuota", func(t *testing.T) {
		if err := m.UpdateQuota(u1.ID, 500); err != nil {
			t.Fatalf("UpdateQuota: %v", err)
		}
		u, _ := q.GetByID(u1.ID)
		if u.Quota != 1500 {
			t.Fatalf("expected 1500, got %d", u.Quota)
		}
	})

	t.Run("DeductQuota", func(t *testing.T) {
		remaining, err := m.DeductQuota(u1.ID, 200)
		if err != nil {
			t.Fatalf("DeductQuota: %v", err)
		}
		if remaining != 1300 {
			t.Fatalf("expected 1300, got %d", remaining)
		}
		u, _ := q.GetByID(u1.ID)
		if u.UsedQuota != 200 {
			t.Fatalf("expected used_quota 200, got %d", u.UsedQuota)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(u2.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.GetByID(u2.ID)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})
}

func TestUserDAO_UserScoped(t *testing.T) {
	// Create a user first
	_, db := setupAdminContext(t)
	u := &models.User{Username: "testuser", Password: "oldhash"}
	db.Create(u)

	uctx, _ := setupUserContext(t, u.ID)
	// The user context has its own DB, so seed the user in that DB too
	uctx.(*userContextImpl).GetDB().Create(&models.User{Username: "testuser", Password: "oldhash"})

	uq := NewQuery(uctx).User()
	um := NewMutation(uctx).User()

	t.Run("GetProfile", func(t *testing.T) {
		profile, err := uq.GetProfile()
		if err != nil {
			t.Fatalf("GetProfile: %v", err)
		}
		if profile.Username != "testuser" {
			t.Fatalf("expected testuser, got %s", profile.Username)
		}
	})

	t.Run("UpdatePassword", func(t *testing.T) {
		if err := um.UpdatePassword("newhash"); err != nil {
			t.Fatalf("UpdatePassword: %v", err)
		}
		profile, _ := uq.GetProfile()
		if profile.Password != "newhash" {
			t.Fatalf("expected newhash, got %s", profile.Password)
		}
		if !profile.PasswordSet {
			t.Fatal("expected PasswordSet=true after UpdatePassword")
		}
	})
}

func TestUserModel_DisplayNameAndAvatarURL(t *testing.T) {
	_, db := setupAdminContext(t)

	u := &models.User{
		Username:    "alice_dn",
		DisplayName: "Alice 张三",
		AvatarURL:   "https://example.com/a.png",
	}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var got models.User
	if err := db.First(&got, u.ID).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.DisplayName != "Alice 张三" {
		t.Errorf("DisplayName mismatch: %q", got.DisplayName)
	}
	if got.AvatarURL != "https://example.com/a.png" {
		t.Errorf("AvatarURL mismatch: %q", got.AvatarURL)
	}
}

func TestUserMutation_UpdateProfile_Success(t *testing.T) {
	a, db := setupTestApp(t)
	u := &models.User{Username: "alice_up_success", Email: "old@x.com", DisplayName: "old"}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	userCtx := NewUserContext(a, &app.UserInfo{UserID: u.ID, Role: 1, Username: u.Username})
	m := NewMutation(userCtx)

	updates := map[string]any{
		"email":        "new@x.com",
		"display_name": "Alice 张三",
		"avatar_url":   "https://example.com/a.png",
	}
	if err := m.User().UpdateProfile(updates); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	var got models.User
	db.First(&got, u.ID)
	if got.Email != "new@x.com" || got.DisplayName != "Alice 张三" || got.AvatarURL != "https://example.com/a.png" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestUserMutation_UpdateProfile_Partial(t *testing.T) {
	a, db := setupTestApp(t)
	u := &models.User{Username: "bob_up_partial", Email: "bob@x.com", DisplayName: "Bobby"}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	userCtx := NewUserContext(a, &app.UserInfo{UserID: u.ID, Role: 1, Username: u.Username})
	m := NewMutation(userCtx)
	if err := m.User().UpdateProfile(map[string]any{"display_name": "B."}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	var got models.User
	db.First(&got, u.ID)
	if got.DisplayName != "B." || got.Email != "bob@x.com" {
		t.Errorf("partial mismatch: %+v", got)
	}
}

func TestUserMutation_UpdateProfile_EmptyMap(t *testing.T) {
	a, db := setupTestApp(t)
	u := &models.User{Username: "carol_up_empty", DisplayName: "Carol"}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	userCtx := NewUserContext(a, &app.UserInfo{UserID: u.ID, Role: 1, Username: u.Username})
	m := NewMutation(userCtx)
	if err := m.User().UpdateProfile(map[string]any{}); err != nil {
		t.Fatalf("empty map should not error: %v", err)
	}
	var got models.User
	db.First(&got, u.ID)
	if got.DisplayName != "Carol" {
		t.Errorf("row was changed by empty update: %+v", got)
	}
}
