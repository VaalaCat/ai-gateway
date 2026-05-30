package channel

import "testing"

func TestValidatePriceRatio(t *testing.T) {
	cases := []struct {
		name    string
		v       float64
		wantErr bool
	}{
		{"zero is full price, ok", 0, false},
		{"normal discount", 0.8, false},
		{"one is full price", 1, false},
		{"markup ok", 2.5, false},
		{"upper bound ok", 1000, false},
		{"negative rejected", -0.1, true},
		{"over max rejected", 1000.1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePriceRatio(c.v)
			if (err != nil) != c.wantErr {
				t.Fatalf("validatePriceRatio(%v) err=%v, wantErr=%v", c.v, err, c.wantErr)
			}
		})
	}
}
