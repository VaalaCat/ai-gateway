package app

import "context"

// Reporter 使用量指标上报器
// Agent 端组件，收集 API 请求的使用量数据（token 消耗、模型、耗时等），
// 批量聚合后通过 WebSocket 上报给 Master
type Reporter interface {
	Start(ctx context.Context)
	Stop()
	SetClient(client WSClient)
	PendingCount() int
}
