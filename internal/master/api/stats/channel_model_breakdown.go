package stats

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// ChannelModelBreakdownRequest 是 /stats/channel-model-breakdown 的入参。
// start/end 为 unix 秒；end 缺省取 now、start 缺省取 end-86400（同 DashboardRequest 约定）。
type ChannelModelBreakdownRequest struct {
	ChannelID uint   `form:"channel_id"`
	Start     int64  `form:"start"`
	End       int64  `form:"end"`
	Gran      string `form:"gran"`
}

// ChannelModelBreakdownResponse 是 /stats/channel-model-breakdown 的返回。
type ChannelModelBreakdownResponse struct {
	Rows []dao.ChannelModelBreakdownRow `json:"rows"`
}

// ChannelModelBreakdown 返回单渠道按 model_name 分组的用量/计费细分(billed 折后 +
// raw 折前),给 Billing 展开行 + 渠道详情卡片共用。admin-only：非 admin 直接 403。
func (h *Handler) ChannelModelBreakdown(c *app.Context, req ChannelModelBreakdownRequest) (ChannelModelBreakdownResponse, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil || !scope.IsAdmin {
		return ChannelModelBreakdownResponse{}, api.ForbiddenError("admin only")
	}

	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return ChannelModelBreakdownResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	rows, err := q.Stats().ChannelModelBreakdown(req.ChannelID, r)
	if err != nil {
		return ChannelModelBreakdownResponse{}, api.InternalError("channel model breakdown query failed", err)
	}
	return ChannelModelBreakdownResponse{Rows: rows}, nil
}
