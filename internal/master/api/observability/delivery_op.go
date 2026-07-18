package observability

import (
	"encoding/json"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"go.uber.org/zap"
)

type DeliveryOpRequest struct {
	AgentID    uint     `json:"agent_id"`
	Op         string   `json:"op"` // retry_now|degrade|drop
	RequestIDs []string `json:"request_ids,omitempty"`
	Level      int      `json:"level,omitempty"`
}

type DeliveryOpResponse struct {
	Affected int `json:"affected"`
}

var deliveryOps = map[string]struct{}{"retry_now": {}, "degrade": {}, "drop": {}}

// PostDeliveryOp 转发一次管理端旁路队列操作(retry_now/degrade/drop)给目标 agent。
// 校验全部在转发前完成——非法输入不产生 RPC。
func (h *Handler) PostDeliveryOp(c *app.Context, req DeliveryOpRequest) (DeliveryOpResponse, error) {
	if _, ok := deliveryOps[req.Op]; !ok {
		return DeliveryOpResponse{}, api.BadRequestError("unknown op", nil)
	}
	if req.Op == "drop" && len(req.RequestIDs) == 0 {
		// 危险操作必须逐条点名,禁止"drop 全部"
		return DeliveryOpResponse{}, api.BadRequestError("drop requires explicit request_ids", nil)
	}
	if req.Op == "degrade" && req.Level != 2 && req.Level != 3 {
		return DeliveryOpResponse{}, api.BadRequestError("degrade level must be 2 or 3", nil)
	}
	if h.HubCall == nil {
		return DeliveryOpResponse{}, api.InternalError("hub not available", nil)
	}
	agent, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Agent().GetByID(req.AgentID)
	if err != nil || agent == nil {
		return DeliveryOpResponse{}, api.BadRequestError("agent not found", err)
	}
	raw, err := h.HubCall(agent.AgentID, consts.RPCAgentUsageQueueOp, map[string]any{
		"op": req.Op, "request_ids": req.RequestIDs, "level": req.Level,
	}, 10*time.Second)
	if err != nil {
		return DeliveryOpResponse{}, api.InternalError("agent op failed", err)
	}
	var result DeliveryOpResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return DeliveryOpResponse{}, api.InternalError("decode agent result", err)
	}
	if req.Op == "drop" {
		// 审计:人工丢弃计费数据必须可追责(agent 侧另有 error 日志,双端对账)
		var operator uint
		if c.UserInfo != nil {
			operator = c.UserInfo.UserID
		}
		c.Logger.Warn("delivery-op drop executed",
			zap.Uint("operator_user_id", operator), zap.Uint("agent_id", req.AgentID),
			zap.Strings("request_ids", req.RequestIDs), zap.Int("affected", result.Affected))
	}
	return result, nil
}
