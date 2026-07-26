package api

import (
	"errors"
	"fmt"
	"testing"
)

func TestParseTopNDefaultsAndAcceptsSupportedValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  int
		want int
	}{
		{name: "default", raw: 0, want: 5},
		{name: "five", raw: 5, want: 5},
		{name: "ten", raw: 10, want: 10},
		{name: "twenty", raw: 20, want: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTopN(tc.raw)
			if err != nil {
				t.Fatalf("ParseTopN(%d): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ParseTopN(%d) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseTopNRejectsUnsupportedAndNegativeValues(t *testing.T) {
	for _, raw := range []int{1, 6, -5, 21} {
		t.Run(fmt.Sprint(raw), func(t *testing.T) {
			_, err := ParseTopN(raw)
			if err == nil {
				t.Fatalf("ParseTopN(%d) error = nil", raw)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("ParseTopN(%d) error = %T, want *APIError", raw, err)
			}
			if apiErr.Status != 400 || apiErr.Code != "InvalidTopN" {
				t.Fatalf("ParseTopN(%d) error = status %d code %q", raw, apiErr.Status, apiErr.Code)
			}
		})
	}
}
