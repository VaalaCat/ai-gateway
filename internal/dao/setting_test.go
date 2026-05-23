package dao

import (
	"testing"

	"gorm.io/gorm"
)

func TestSettingLookup_Fallback(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).Setting()

	if got := q.LookupString("nonexistent", "fallback"); got != "fallback" {
		t.Fatalf("LookupString fallback: got %q", got)
	}
	if got := q.LookupInt("nonexistent", 42); got != 42 {
		t.Fatalf("LookupInt fallback: got %d", got)
	}
	if got := q.LookupBool("nonexistent", true); got != true {
		t.Fatalf("LookupBool fallback: got %v", got)
	}
	if got := q.LookupFloat("nonexistent", 3.14); got != 3.14 {
		t.Fatalf("LookupFloat fallback: got %v", got)
	}
}

func TestSettingLookup_RealValue(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).Setting()
	m := NewAdminMutation(ctx).Setting()

	mustSet := func(k, v string) {
		t.Helper()
		if err := m.Set(k, v); err != nil {
			t.Fatalf("Set(%q,%q): %v", k, v, err)
		}
	}
	mustSet("k_str", "value")
	mustSet("k_int", "100")
	mustSet("k_bool", "true")
	mustSet("k_float", "2.5")

	if got := q.LookupString("k_str", ""); got != "value" {
		t.Fatalf("LookupString: got %q", got)
	}
	if got := q.LookupInt("k_int", 0); got != 100 {
		t.Fatalf("LookupInt: got %d", got)
	}
	if got := q.LookupBool("k_bool", false); got != true {
		t.Fatalf("LookupBool: got %v", got)
	}
	if got := q.LookupFloat("k_float", 0); got != 2.5 {
		t.Fatalf("LookupFloat: got %v", got)
	}
}

func TestSettingLookup_BoolVariants(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).Setting()
	m := NewAdminMutation(ctx).Setting()

	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"garbage", true}, // unparseable falls back to provided fallback
	}
	for _, tc := range cases {
		if err := m.Set("bk", tc.raw); err != nil {
			t.Fatalf("Set %q: %v", tc.raw, err)
		}
		got := q.LookupBool("bk", true)
		if tc.raw == "garbage" {
			if got != true {
				t.Fatalf("LookupBool(%q) fallback=true: got %v", tc.raw, got)
			}
			continue
		}
		if got != tc.want {
			t.Fatalf("LookupBool(%q): got %v want %v", tc.raw, got, tc.want)
		}
	}
}

func TestSettingLookup_UnparseableNumericFallsBack(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).Setting()
	m := NewAdminMutation(ctx).Setting()

	if err := m.Set("bad_int", "not-a-number"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := m.Set("bad_float", "xyz"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := q.LookupInt("bad_int", 7); got != 7 {
		t.Fatalf("LookupInt unparseable: got %d", got)
	}
	if got := q.LookupFloat("bad_float", 1.23); got != 1.23 {
		t.Fatalf("LookupFloat unparseable: got %v", got)
	}
}

func TestSettingLookup_EmptyStringTreatedAsMissingForTyped(t *testing.T) {
	// An empty string value should fall back to the provided fallback for typed
	// lookups (Int/Bool/Float). For LookupString, the literal empty string is
	// the real value and is returned as-is.
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).Setting()
	m := NewAdminMutation(ctx).Setting()

	if err := m.Set("empty_val", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := q.LookupString("empty_val", "fb"); got != "" {
		t.Fatalf("LookupString empty: got %q", got)
	}
	if got := q.LookupInt("empty_val", 9); got != 9 {
		t.Fatalf("LookupInt empty: got %d", got)
	}
	if got := q.LookupBool("empty_val", true); got != true {
		t.Fatalf("LookupBool empty: got %v", got)
	}
	if got := q.LookupFloat("empty_val", 1.5); got != 1.5 {
		t.Fatalf("LookupFloat empty: got %v", got)
	}
}

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
