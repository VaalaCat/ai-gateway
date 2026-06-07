package models

import (
	"testing"

	"gorm.io/datatypes"
)

func TestTokenTemplate_AllowsGroup(t *testing.T) {
	cases := []struct {
		name    string
		allowed []uint
		group   uint
		want    bool
	}{
		{"nil allows all", nil, 5, true},
		{"empty slice allows all", []uint{}, 0, true},
		{"member allowed", []uint{2, 5, 9}, 5, true},
		{"non-member blocked", []uint{2, 9}, 5, false},
		{"zero group not in non-empty list", []uint{2, 9}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := TokenTemplate{AllowedGroupIDs: datatypes.JSONSlice[uint](tc.allowed)}
			if got := tpl.AllowsGroup(tc.group); got != tc.want {
				t.Fatalf("AllowsGroup(%d) with %v = %v, want %v", tc.group, tc.allowed, got, tc.want)
			}
		})
	}
}
