// Package histutil 提供按 edges 参数化的直方图槽定位与分位数插值。
// 各领域包(durhist/ttfthist/tpshist)持有自己的 Edges，逻辑在此一处实现。
package histutil

// SlotIndex 返回 v 落入的槽下标(槽 i 覆盖 [edges[i-1], edges[i])，槽 0 下界 0，末槽为溢出槽)。
func SlotIndex(edges []int64, v int64) int {
	for i, edge := range edges {
		if v < edge {
			return i
		}
	}
	return len(edges)
}

// MergeCounts merges histogram rows slot by slot. Rows may be empty or have
// different widths; the result preserves every provided slot without panic.
func MergeCounts(rows [][]int64) []int64 {
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	merged := make([]int64, width)
	for _, row := range rows {
		for i, count := range row {
			merged[i] += count
		}
	}
	return merged
}

// EstimatePercentile 在合并直方图上求近似分位(p ∈ (0,1))。counts 长度须 = len(edges)+1。
func EstimatePercentile(edges []int64, counts []int64, p float64, maxVal int64) int64 {
	var total int64
	width := min(len(counts), len(edges)+1)
	for _, c := range counts[:width] {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := p * float64(total)
	last := len(edges) // 末槽下标
	var cum float64
	for i, c := range counts[:width] {
		if c == 0 {
			continue
		}
		next := cum + float64(c)
		if next >= target {
			lower := int64(0)
			if i > 0 {
				lower = edges[i-1]
			}
			upper := maxVal
			if i < last {
				upper = edges[i]
			}
			if upper < lower {
				return lower
			}
			frac := (target - cum) / float64(c)
			return lower + int64(frac*float64(upper-lower))
		}
		cum = next
	}
	return maxVal
}
