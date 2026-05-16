package app

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// Settler 计费结算器
// 监听 Agent 上报的使用量数据，计算费用并更新用户额度
type Settler interface {
	Start()
	Settle(ctx context.Context, agentID string, logs []protocol.UsageLogEntry)
}

// QuotaChecker 额度检查器
// 定期检查用户额度使用情况，对超额用户进行处理
type QuotaChecker interface {
	Start()
}
