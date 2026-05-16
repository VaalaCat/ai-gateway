package dao

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func TestEnrollmentTokenDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).EnrollmentToken()
	m := NewAdminMutation(ctx).EnrollmentToken()

	future := time.Now().Unix() + 3600
	past := time.Now().Unix() - 3600

	et1 := &models.EnrollmentToken{Token: "valid-token", ExpiresAt: future}
	et2 := &models.EnrollmentToken{Token: "expired-token", ExpiresAt: past}
	for _, et := range []*models.EnrollmentToken{et1, et2} {
		if err := db.Create(et).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("GetValidByToken found", func(t *testing.T) {
		et, err := q.GetValidByToken("valid-token")
		if err != nil {
			t.Fatalf("GetValidByToken: %v", err)
		}
		if et.Token != "valid-token" {
			t.Fatalf("expected valid-token, got %s", et.Token)
		}
	})

	t.Run("GetValidByToken expired", func(t *testing.T) {
		_, err := q.GetValidByToken("expired-token")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound for expired, got %v", err)
		}
	})

	t.Run("GetValidByToken not found", func(t *testing.T) {
		_, err := q.GetValidByToken("nonexistent")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("List", func(t *testing.T) {
		tokens, err := q.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(tokens) != 2 {
			t.Fatalf("expected 2, got %d", len(tokens))
		}
	})

	t.Run("Create", func(t *testing.T) {
		et := &models.EnrollmentToken{Token: "new-token", ExpiresAt: future}
		if err := m.Create(et); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if et.ID == 0 {
			t.Fatal("expected ID set")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(et2.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		tokens, _ := q.List()
		for _, tok := range tokens {
			if tok.ID == et2.ID {
				t.Fatal("expected token to be deleted")
			}
		}
	})
}
