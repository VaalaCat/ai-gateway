package rpc

import (
	"encoding/json"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/agent/reporter"
)

type UsageQueueOpParams struct {
	Op         string   `json:"op"`
	RequestIDs []string `json:"request_ids,omitempty"`
	Level      int      `json:"level,omitempty"`
}

type UsageQueueOpResult struct {
	Affected int `json:"affected"`
}

// HandleUsageQueue 返回 usage 投递两级队列(主队列+旁路重试队列)的只读快照,
// 供 master 侧管理看板聚合展示(Task 10)。
func HandleUsageQueue(rep *reporter.Reporter) (any, error) {
	if rep == nil {
		return nil, fmt.Errorf("reporter not ready")
	}
	return rep.QueueSnapshot(), nil
}

// HandleUsageQueueOp 只做参数搬运:反序列化 params,委托给 Reporter.QueueOp——
// retry_now/degrade/drop 的语义校验和分发全在 reporter 门面里。
func HandleUsageQueueOp(rep *reporter.Reporter, params json.RawMessage) (any, error) {
	if rep == nil {
		return nil, fmt.Errorf("reporter not ready")
	}
	var p UsageQueueOpParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	n, err := rep.QueueOp(p.Op, p.RequestIDs, p.Level)
	if err != nil {
		return nil, err
	}
	return UsageQueueOpResult{Affected: n}, nil
}
