package api

import (
	"testing"
)

func TestValidateStatusValue(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		wantErr bool
	}{
		{"enabled int", 1, false},
		{"disabled int", 0, false},
		{"enabled float64 (from JSON)", float64(1), false},
		{"disabled float64 (from JSON)", float64(0), false},
		{"enabled int64", int64(1), false},
		{"illegal int 2", 2, true},
		{"illegal int -1", -1, true},
		{"illegal float 1.5", 1.5, true},
		{"illegal string", "1", true},
		{"illegal nil", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStatusValue(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
