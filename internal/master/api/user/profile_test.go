package user

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"golang.org/x/crypto/bcrypt"
)

func TestChangePassword_SetsPasswordSet(t *testing.T) {
	db := setupUserTestDB(t)

	// Simulate an OAuth-auto-created user: has no password, PasswordSet=false.
	// Give it a bcrypt hash so the old-password check can pass.
	hashed, err := bcrypt.GenerateFromPassword([]byte("oldpass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	u := &models.User{
		Username:    "oauth_user",
		Password:    string(hashed),
		PasswordSet: false,
		Role:        1,
		Status:      1,
	}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	c := newUserTestContext(t, db)
	c.UserInfo = &app.UserInfo{UserID: u.ID, Username: u.Username}

	h := &Handler{}
	resp, err := h.ChangePassword(c, ChangePasswordRequest{
		OldPassword: "oldpass",
		NewPassword: "newpass",
	})
	if err != nil {
		t.Fatalf("ChangePassword returned error: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %s", resp.Status)
	}

	// Re-fetch from DB and verify PasswordSet=true.
	var updated models.User
	if err := db.First(&updated, u.ID).Error; err != nil {
		t.Fatalf("fetch user: %v", err)
	}
	if !updated.PasswordSet {
		t.Fatal("expected PasswordSet=true after ChangePassword, got false")
	}
}
