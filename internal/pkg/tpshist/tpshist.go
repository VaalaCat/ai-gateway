// Package tpshist 定义生成速度(TPS, tokens/s)直方图档位；插值复用 histutil。
// 档位偏 20-300 tokens/s 密集(TPS 主分布区)。改档必须配套 rebuild 回填。
package tpshist

import (
	"math"
	"math/bits"

	"github.com/VaalaCat/ai-gateway/internal/pkg/histutil"
)

const NumSlots = 17

var Edges = [NumSlots - 1]int64{
	5, 10, 20, 30, 40, 50, 75, 100,
	125, 150, 200, 250, 300, 400, 500, 750,
}

// TokensPerSecond computes tokens*1000/generationMs without overflowing.
func TokensPerSecond(tokens, generationMs int64) int64 {
	if tokens <= 0 || generationMs <= 0 {
		return 0
	}
	quotient, remainder := tokens/generationMs, tokens%generationMs
	if quotient > math.MaxInt64/1000 {
		return math.MaxInt64
	}
	whole := quotient * 1000
	hi, lo := bits.Mul64(uint64(remainder), 1000)
	fractional, _ := bits.Div64(hi, lo, uint64(generationMs))
	fraction := int64(fractional)
	if whole > math.MaxInt64-fraction {
		return math.MaxInt64
	}
	return whole + fraction
}

func SlotIndex(tps int64) int { return histutil.SlotIndex(Edges[:], tps) }

func EstimatePercentile(counts [NumSlots]int64, p float64, maxTps int64) int64 {
	return histutil.EstimatePercentile(Edges[:], counts[:], p, maxTps)
}

func EstimateP5(counts [NumSlots]int64, maxTps int64) int64 {
	return EstimatePercentile(counts, 0.05, maxTps)
}
