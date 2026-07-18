// internal/pkg/durhist/durhist_test.go
package durhist

import "testing"

func TestSlotIndex(t *testing.T) {
	cases := []struct {
		ms   int64
		want int
	}{
		{0, 0}, {499, 0},          // 首槽含 0
		{500, 1}, {999, 1},        // 边界值归上一档的下界
		{9999, 6}, {10000, 7},
		{299999, 15},
		{300000, 16}, {13390440, 16}, // 溢出槽
	}
	for _, c := range cases {
		if got := SlotIndex(c.ms); got != c.want {
			t.Errorf("SlotIndex(%d) = %d, want %d", c.ms, got, c.want)
		}
	}
}

func TestEstimatePercentile_UniformSlot(t *testing.T) {
	// 100 条全部落在槽 4([3000,5000)):p50 应插值到 ~4000
	var counts [NumSlots]int64
	counts[4] = 100
	got := EstimatePercentile(counts, 0.50, 5000)
	if got < 3900 || got > 4100 {
		t.Fatalf("p50 = %d, want ≈4000 (slot 内线性插值)", got)
	}
}

func TestEstimatePercentile_RealShape(t *testing.T) {
	// 用 spec §8.2 真库直方图(近7天成功 6 万条)对拍:精确 p95=59552,容差 ±3%
	counts := [NumSlots]int64{10, 310, 1834, 6079, 10974, 7413, 5172, 7986, 7617, 4236, 3596, 1817, 1557, 641, 335, 222, 209}
	p95 := EstimatePercentile(counts, 0.95, 13390440)
	if p95 < 57768 || p95 > 61340 { // 59552 ±3%
		t.Fatalf("p95 = %d, want 59552±3%%", p95)
	}
	p50 := EstimatePercentile(counts, 0.50, 13390440)
	if p50 < 8600 || p50 > 9550 { // 9070 ±5%
		t.Fatalf("p50 = %d, want 9070±5%%", p50)
	}
}

func TestEstimatePercentile_Boundaries(t *testing.T) {
	var empty [NumSlots]int64
	if got := EstimatePercentile(empty, 0.95, 0); got != 0 {
		t.Fatalf("empty histogram p95 = %d, want 0", got)
	}
	// 全在溢出槽:上界用 maxMs 插值,不会超过 maxMs 也不该低于 300000
	var over [NumSlots]int64
	over[16] = 10
	got := EstimatePercentile(over, 0.95, 400000)
	if got < 300000 || got > 400000 {
		t.Fatalf("overflow-slot p95 = %d, want within [300000,400000]", got)
	}
	// 单条数据
	var one [NumSlots]int64
	one[0] = 1
	if got := EstimatePercentile(one, 0.95, 100); got < 0 || got > 500 {
		t.Fatalf("single-entry p95 = %d, want within slot 0", got)
	}
}
