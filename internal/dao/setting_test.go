package dao

import (
	"testing"

	"gorm.io/gorm"
)

func TestSettingDAO(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).Setting()
	m := NewAdminMutation(ctx).Setting()

	t.Run("Get not found", func(t *testing.T) {
		_, err := q.Get("nonexistent")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("Lookup missing returns false without error", func(t *testing.T) {
		s, found, err := q.Lookup("missing_optional_setting")
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		if found {
			t.Fatalf("expected found=false, got true with %+v", s)
		}
		if s != nil {
			t.Fatalf("expected nil setting, got %+v", s)
		}
	})

	t.Run("Set creates new setting", func(t *testing.T) {
		if err := m.Set("site_name", "TestSite"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		s, err := q.Get("site_name")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if s.Value != "TestSite" {
			t.Fatalf("expected TestSite, got %s", s.Value)
		}
	})

	t.Run("Set upserts existing setting", func(t *testing.T) {
		if err := m.Set("site_name", "UpdatedSite"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		s, err := q.Get("site_name")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if s.Value != "UpdatedSite" {
			t.Fatalf("expected UpdatedSite, got %s", s.Value)
		}
	})

	t.Run("GetAll", func(t *testing.T) {
		_ = m.Set("another_key", "another_value")
		settings, err := q.GetAll()
		if err != nil {
			t.Fatalf("GetAll: %v", err)
		}
		if len(settings) < 2 {
			t.Fatalf("expected at least 2 settings, got %d", len(settings))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete("site_name"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.Get("site_name")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound after delete, got %v", err)
		}
	})
}
