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

func TestValidateAutoBanValue(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		wantErr bool
	}{
		{name: "enabled int", input: 1},
		{name: "disabled int", input: 0},
		{name: "enabled JSON number", input: float64(1)},
		{name: "disabled JSON number", input: float64(0)},
		{name: "rejects positive non-binary value", input: 2, wantErr: true},
		{name: "rejects negative value", input: -1, wantErr: true},
		{name: "rejects fractional value", input: 0.5, wantErr: true},
		{name: "rejects string value", input: "1", wantErr: true},
		{name: "rejects nil value", input: nil, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAutoBanValue(tc.input)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
