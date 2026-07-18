// Package durhist 定义请求耗时直方图的档位与分位数插值。
// 档位按生产实测分布定(2026-07-08,spec §8.2):质量集中在 1s-60s,
// p95≈60s;插值验证 p95 误差 0.25%。改档必须配套 rebuild 回填。
package durhist

// NumSlots = len(Edges)+1,末槽为溢出槽(>= 最后一个上界)。
const NumSlots = 17

// Edges 是各槽的上界(ms,exclusive)。槽 i 覆盖 [Edges[i-1], Edges[i]),槽 0 下界 0。
var Edges = [NumSlots - 1]int64{
	500, 1000, 2000, 3000, 5000, 7500, 10000, 15000,
	22000, 30000, 45000, 60000, 90000, 120000, 180000, 300000,
}

// SlotIndex 返回 durationMs 落入的槽下标。
func SlotIndex(durationMs int64) int {
	for i, edge := range Edges {
		if durationMs < edge {
			return i
		}
	}
	return NumSlots - 1
}

// EstimatePercentile 在合并后的直方图上求近似分位(p ∈ (0,1))。
// 累计计数定位目标槽,槽内线性插值;溢出槽上界取 maxMs(来自 MaxDurationMs 聚合)。
func EstimatePercentile(counts [NumSlots]int64, p float64, maxMs int64) int64 {
	var total int64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := p * float64(total)
	var cum float64
	for i, c := range counts {
		if c == 0 {
			continue
		}
		next := cum + float64(c)
		if next >= target {
			lower := int64(0)
			if i > 0 {
				lower = Edges[i-1]
			}
			upper := maxMs
			if i < NumSlots-1 {
				upper = Edges[i]
			}
			if upper < lower { // maxMs 缺失/异常时退化为槽下界
				return lower
			}
			frac := (target - cum) / float64(c)
			return lower + int64(frac*float64(upper-lower))
		}
		cum = next
	}
	return maxMs
}
