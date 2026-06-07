package rpc

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// HandleLimiterUsage 采样 memStore 桶级读数，join LimiterIndex 规则元数据，
// 产出 []protocol.LimiterBucketStat（供 master 扇出聚合 + 前端限流看板）。
// store/index 为 nil 时安全降级。
func HandleLimiterUsage(store *limiter.MemStore, index *cache.LimiterIndex) (any, error) {
	if store == nil {
		return []protocol.LimiterBucketStat{}, nil
	}
	raw := store.SnapshotBuckets()
	out := make([]protocol.LimiterBucketStat, 0, len(raw))
	for _, b := range raw {
		row := protocol.LimiterBucketStat{
			LimiterID: b.Key.LimiterID,
			Bucket:    b.Key.Bucket,
			Occupied:  b.Occupied,
			Waiters:   b.Waiters,
		}
		if b.IsRate {
			row.Metric = "rate"
		} else {
			row.Metric = "concurrency"
		}
		if index != nil {
			if lim := index.Limiter(b.Key.LimiterID); lim != nil {
				row.Name = lim.Name
				row.KeyBy = lim.KeyBy
				row.Capacity = lim.Capacity
				row.Metric = lim.Metric
				if b.IsRate && lim.WindowMs > 0 {
					row.WindowEndMs = b.WindowStartMs + int64(lim.WindowMs)
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}
