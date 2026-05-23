package listfilter_test

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type row struct {
	ID        uint  `gorm:"primaryKey"`
	CreatedAt int64 `gorm:"index"`
}

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&row{}); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{100, 200, 300, 400, 500} {
		if err := db.Create(&row{CreatedAt: ts}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func count(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&row{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApply_NoOpWhenZero(t *testing.T) {
	db := setupDB(t)
	w := listfilter.TimeWindow{}
	got := count(t, w.Apply(db, "created_at"))
	if got != 5 {
		t.Fatalf("expected all 5 rows, got %d", got)
	}
}

func TestApply_LowerBoundOnly(t *testing.T) {
	db := setupDB(t)
	w := listfilter.TimeWindow{Start: 300}
	got := count(t, w.Apply(db, "created_at"))
	if got != 3 { // 300, 400, 500
		t.Fatalf("expected 3 rows (>=300), got %d", got)
	}
}

func TestApply_UpperBoundOnly(t *testing.T) {
	db := setupDB(t)
	w := listfilter.TimeWindow{End: 300}
	got := count(t, w.Apply(db, "created_at"))
	if got != 2 { // 100, 200 (300 excluded)
		t.Fatalf("expected 2 rows (<300), got %d", got)
	}
}

func TestApply_BothBounds(t *testing.T) {
	db := setupDB(t)
	w := listfilter.TimeWindow{Start: 200, End: 500}
	got := count(t, w.Apply(db, "created_at"))
	if got != 3 { // 200, 300, 400 (500 excluded)
		t.Fatalf("expected 3 rows (200..500), got %d", got)
	}
}

func TestDays_VariousRanges(t *testing.T) {
	cases := []struct {
		name string
		w    listfilter.TimeWindow
		want int
	}{
		{"zero", listfilter.TimeWindow{}, 0},
		{"end<=start", listfilter.TimeWindow{Start: 100, End: 50}, 0},
		{"1 second", listfilter.TimeWindow{Start: 0, End: 1}, 1},
		{"exact 1 day", listfilter.TimeWindow{Start: 0, End: 86400}, 1},
		{"1 day + 1 sec", listfilter.TimeWindow{Start: 0, End: 86401}, 2},
		{"7 days", listfilter.TimeWindow{Start: 0, End: 7 * 86400}, 7},
		{"8 days", listfilter.TimeWindow{Start: 0, End: 7*86400 + 1}, 8},
	}
	for _, tc := range cases {
		if got := tc.w.Days(); got != tc.want {
			t.Errorf("%s: Days() = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestValidate_WithinLimit(t *testing.T) {
	w := listfilter.TimeWindow{Start: 0, End: 7 * 86400}
	if err := w.Validate(7); err != nil {
		t.Errorf("expected nil for 7 days <= 7 maxDays, got %v", err)
	}
}

func TestValidate_OverLimit(t *testing.T) {
	w := listfilter.TimeWindow{Start: 0, End: 8 * 86400}
	err := w.Validate(7)
	if !errors.Is(err, listfilter.ErrRangeOutOfBounds) {
		t.Errorf("expected ErrRangeOutOfBounds for 8 days > 7 maxDays, got %v", err)
	}
}

func TestValidate_NoLimit(t *testing.T) {
	w := listfilter.TimeWindow{Start: 0, End: 99999 * 86400}
	if err := w.Validate(0); err != nil {
		t.Errorf("maxDays=0 should always pass, got %v", err)
	}
}
