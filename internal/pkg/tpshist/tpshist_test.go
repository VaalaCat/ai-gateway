package tpshist

import (
	"math"
	"testing"
)

func TestSlotIndex(t *testing.T) {
	cases := []struct {
		tps  int64
		want int
	}{{0, 0}, {4, 0}, {5, 1}, {749, 15}, {750, 16}}
	for _, c := range cases {
		if got := SlotIndex(c.tps); got != c.want {
			t.Errorf("SlotIndex(%d) = %d, want %d", c.tps, got, c.want)
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
	if got := EstimatePercentile(one, 0.5, 750); got < 0 || got > 50 {
		t.Errorf("single-slot p50 = %d, want [0,50]", got)
	}
}

func TestTPSP5UsesSlowTailRatherThanP95(t *testing.T) {
	var counts [NumSlots]int64
	counts[0], counts[10] = 5, 95
	p5, p95 := EstimateP5(counts, 200), EstimatePercentile(counts, 0.95, 200)
	if p5 >= p95 {
		t.Fatalf("p5 = %d, p95 = %d, want slow lower tail below p95", p5, p95)
	}
}

func TestEstimateP5EmptyAndOverflowBoundaries(t *testing.T) {
	if got := EstimateP5([NumSlots]int64{}, 0); got != 0 {
		t.Fatalf("empty p5 = %d, want 0", got)
	}
	var overflow [NumSlots]int64
	overflow[NumSlots-1] = 100
	if got := EstimateP5(overflow, 900); got < Edges[len(Edges)-1] || got > 900 {
		t.Fatalf("overflow p5 = %d, want [%d,900]", got, Edges[len(Edges)-1])
	}
}

func TestTokensPerSecond(t *testing.T) {
	tests := []struct {
		name                     string
		tokens, generation, want int64
	}{
		{name: "ordinary", tokens: 50, generation: 1000, want: 50},
		{name: "zero generation", tokens: 50, generation: 0, want: 0},
		{name: "negative tokens", tokens: -1, generation: 1, want: 0},
		{name: "overflow clamps", tokens: math.MaxInt64, generation: 1, want: math.MaxInt64},
		{name: "large exact", tokens: math.MaxInt64, generation: 1000, want: math.MaxInt64},
		{name: "large fraction", tokens: math.MaxInt64 - 1, generation: math.MaxInt64, want: 999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TokensPerSecond(tt.tokens, tt.generation); got != tt.want {
				t.Fatalf("TokensPerSecond(%d, %d) = %d, want %d", tt.tokens, tt.generation, got, tt.want)
			}
		})
	}
}
