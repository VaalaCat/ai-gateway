package ttfthist

import "testing"

func TestSlotIndex(t *testing.T) {
	cases := []struct {
		ms   int64
		want int
	}{
		{0, 0}, {49, 0}, {50, 1}, {99, 1}, {999, 6}, {1000, 7},
		{29999, 15}, {30000, 16}, {999999, 16},
	}
	for _, c := range cases {
		if got := SlotIndex(c.ms); got != c.want {
			t.Errorf("SlotIndex(%d) = %d, want %d", c.ms, got, c.want)
		}
	}
}

func TestEstimatePercentile_Boundaries(t *testing.T) {
	var empty [NumSlots]int64
	if got := EstimatePercentile(empty, 0.95, 0); got != 0 {
		t.Errorf("empty p95 = %d, want 0", got)
	}
	var one [NumSlots]int64
	one[0] = 1
	if got := EstimatePercentile(one, 0.5, 30); got < 0 || got > 50 {
		t.Errorf("single-slot p50 = %d, want [0,50]", got)
	}
}
