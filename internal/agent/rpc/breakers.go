package rpc

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/genericapi"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
)

// HandleBreakers 返回当前所有渠道熔断器的快照(供 master 全局熔断看板聚合)。
// 快照已在 resilience 侧按 (source, channel_id) 确定性排序;渠道名由前端 EntityLabel 解析。
func HandleBreakers(reg *resilience.Registry) (any, error) {
	if reg == nil {
		return []resilience.BreakerSnapshot{}, nil
	}
	return reg.SnapshotBreakers(), nil
}

// HandleAPIBreakers returns Generic API breaker snapshots without folding
// APIUpstream IDs into the existing LLM channel snapshot contract.
func HandleAPIBreakers(reg *genericapi.APIBreakerRegistry) (any, error) {
	if reg == nil {
		return []genericapi.APIBreakerSnapshot{}, nil
	}
	return reg.SnapshotBreakers(), nil
}
