// Package ttfthist 定义首 token 时延(TTFT)直方图档位；插值复用 histutil。
// 档位偏 100ms–10s 密集(TTFT 主分布区)。改档必须配套 rebuild 回填。
package ttfthist

import "github.com/VaalaCat/ai-gateway/internal/pkg/histutil"

const NumSlots = 17

var Edges = [NumSlots - 1]int64{
	50, 100, 200, 300, 500, 750, 1000, 1500,
	2000, 3000, 5000, 7500, 10000, 15000, 20000, 30000,
}

func SlotIndex(ms int64) int { return histutil.SlotIndex(Edges[:], ms) }

func EstimatePercentile(counts [NumSlots]int64, p float64, maxMs int64) int64 {
	return histutil.EstimatePercentile(Edges[:], counts[:], p, maxMs)
}
