package histutil

import "testing"

func TestSlotIndex(t *testing.T) {
	edges := []int64{500, 1000, 2000}
	cases := []struct {
		v    int64
		want int
	}{{0, 0}, {499, 0}, {500, 1}, {1999, 2}, {2000, 3}, {9999, 3}}
	for _, c := range cases {
		if got := SlotIndex(edges, c.v); got != c.want {
			t.Errorf("SlotIndex(%d) = %d, want %d", c.v, got, c.want)
		}
	}
}

func TestEstimatePercentile(t *testing.T) {
	edges := []int64{500, 1000, 2000}
	if got := EstimatePercentile(edges, []int64{0, 0, 0, 0}, 0.95, 0); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
	// 100 条落槽 1 [500,1000)，p50 应 ≈750(槽内线性)
	counts := []int64{0, 100, 0, 0}
	if got := EstimatePercentile(edges, counts, 0.5, 1000); got < 730 || got > 770 {
		t.Errorf("p50 = %d, want ~750", got)
	}
	// upper<lower(max 小于末槽下界)退化返 lower
	if got := EstimatePercentile(edges, []int64{0, 0, 0, 5}, 0.95, 100); got != 2000 {
		t.Errorf("degenerate = %d, want 2000 (lower of overflow slot)", got)
	}
}

func TestEstimatePercentileSupportsLowerTailP5(t *testing.T) {
	edges := []int64{10, 100, 1000}
	counts := []int64{5, 0, 95, 0}
	p5 := EstimatePercentile(edges, counts, 0.05, 1000)
	p95 := EstimatePercentile(edges, counts, 0.95, 1000)
	if p5 != 10 {
		t.Fatalf("p5 = %d, want lower-tail boundary 10", p5)
	}
	if p5 >= p95 {
		t.Fatalf("p5 = %d, p95 = %d, want p5 < p95", p5, p95)
	}
}

func TestMergeCountsHandlesEmptyZeroAndMismatchedRows(t *testing.T) {
	if got := MergeCounts(nil); len(got) != 0 {
		t.Fatalf("MergeCounts(nil) = %v, want empty", got)
	}
	got := MergeCounts([][]int64{{1, 0, 3}, nil, {0, 2}, {4, 0, 0, 5}})
	want := []int64{5, 2, 3, 5}
	if len(got) != len(want) {
		t.Fatalf("len(MergeCounts) = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MergeCounts[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestEstimatePercentileIgnoresMismatchedExtraSlots(t *testing.T) {
	got := EstimatePercentile([]int64{10}, []int64{100, 0, 999}, 0.95, 10)
	if got < 9 || got > 10 {
		t.Fatalf("mismatched histogram percentile = %d, want [9,10]", got)
	}
}
