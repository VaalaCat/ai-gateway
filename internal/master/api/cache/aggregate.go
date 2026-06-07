package cache

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// AgentSnapshot 是某个 agent 的 cache 快照与在线状态。Online==false 的 agent 跳过。
type AgentSnapshot struct {
	AgentID    string
	Online     bool
	CacheStats map[string]protocol.CacheEntityStats
}

// ClusterEntityStats 是单实体/索引跨 agent 聚合后的结果。
// HitRate/Util 仅 lru 类且分母>0 时非 nil（前端渲染 "—"）。
type ClusterEntityStats struct {
	Kind          string           `json:"kind,omitempty"`
	Hits          int64            `json:"hits"`
	Misses        int64            `json:"misses"`
	Evictions     int64            `json:"evictions"`
	NegativeHits  int64            `json:"negative_hits"`
	LoadErrors    int64            `json:"load_errors"`
	Invalidations int64            `json:"invalidations"`
	Size          int              `json:"size"`
	Capacity      int              `json:"capacity"`
	Extra         map[string]int64 `json:"extra,omitempty"`
	HitRate       *float64         `json:"hit_rate"`
	Util          *float64         `json:"util"`
}

// Aggregate 把多个 agent 的快照按 union-of-keys 聚合——后端不再硬编码实体清单，
// 任何 agent 上报的新缓存 key 自动流通。lru 类算 hit_rate/util；index 类只求和 Size/Extra。
func Aggregate(snapshots []AgentSnapshot) map[string]ClusterEntityStats {
	out := map[string]ClusterEntityStats{}
	for _, snap := range snapshots {
		if !snap.Online || snap.CacheStats == nil {
			continue
		}
		for name, s := range snap.CacheStats {
			cur := out[name]
			if cur.Kind == "" {
				cur.Kind = s.Kind
			}
			cur.Hits += s.Hits
			cur.Misses += s.Misses
			cur.Evictions += s.Evictions
			cur.NegativeHits += s.NegativeHits
			cur.LoadErrors += s.LoadErrors
			cur.Invalidations += s.Invalidations
			cur.Size += s.Size
			cur.Capacity += s.Capacity
			for k, v := range s.Extra {
				if cur.Extra == nil {
					cur.Extra = map[string]int64{}
				}
				cur.Extra[k] += v
			}
			out[name] = cur
		}
	}
	for name, agg := range out {
		if agg.Kind == "index" {
			out[name] = agg
			continue
		}
		if denom := agg.Hits + agg.Misses; denom > 0 {
			r := float64(agg.Hits) / float64(denom)
			agg.HitRate = &r
		}
		if agg.Capacity > 0 {
			u := float64(agg.Size) / float64(agg.Capacity)
			agg.Util = &u
		}
		out[name] = agg
	}
	return out
}
