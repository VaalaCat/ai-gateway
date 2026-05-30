package cache

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// EntityNames 是固定的实体顺序，前后端共用。
var EntityNames = []string{
	"token", "user", "channel", "model_config", "agent", "user_group",
	"model_routing", "user_routings", "private_channels_visible",
}

// AgentSnapshot 是某个 agent 的 cache 快照与在线状态。
// Aggregate 的入参；Online == false 的 agent 会被跳过。
type AgentSnapshot struct {
	AgentID    string
	Online     bool
	CacheStats map[string]protocol.CacheEntityStats
}

// ClusterEntityStats 是单实体跨 agent 聚合后的结果。
// HitRate 与 Util 在分母为 0 时为 nil（前端渲染为 "—"）。
type ClusterEntityStats struct {
	Hits          int64    `json:"hits"`
	Misses        int64    `json:"misses"`
	Evictions     int64    `json:"evictions"`
	NegativeHits  int64    `json:"negative_hits"`
	LoadErrors    int64    `json:"load_errors"`
	Invalidations int64    `json:"invalidations"`
	Size          int      `json:"size"`
	Capacity      int      `json:"capacity"`
	HitRate       *float64 `json:"hit_rate"`
	Util          *float64 `json:"util"`
}

// Aggregate 把多个 agent 的快照按实体维度求和。
// 输入空 / 全 offline 时仍返回 EntityNames 中所有实体的零值，前端就不必判 nil。
func Aggregate(snapshots []AgentSnapshot) map[string]ClusterEntityStats {
	out := make(map[string]ClusterEntityStats, len(EntityNames))
	for _, name := range EntityNames {
		out[name] = ClusterEntityStats{}
	}
	for _, snap := range snapshots {
		if !snap.Online || snap.CacheStats == nil {
			continue
		}
		for _, name := range EntityNames {
			s, ok := snap.CacheStats[name]
			if !ok {
				continue
			}
			cur := out[name]
			cur.Hits += s.Hits
			cur.Misses += s.Misses
			cur.Evictions += s.Evictions
			cur.NegativeHits += s.NegativeHits
			cur.LoadErrors += s.LoadErrors
			cur.Invalidations += s.Invalidations
			cur.Size += s.Size
			cur.Capacity += s.Capacity
			out[name] = cur
		}
	}
	for name, agg := range out {
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
