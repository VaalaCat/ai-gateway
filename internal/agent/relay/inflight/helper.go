package inflight

import (
	"slices"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func cloneFallbackChain(chain []models.AttemptRecord) []models.AttemptRecord {
	// 1. 复制候选记录及其中的节点路径。
	result := slices.Clone(chain)
	for index := range result {
		result[index].AgentPaths = slices.Clone(result[index].AgentPaths)
	}
	return result
}
