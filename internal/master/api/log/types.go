package log

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
)

type Handler struct{}

// MaxLogsListDays 限制 logs list 的时间窗最大天数 — 防扫表上限。
const MaxLogsListDays = 365

type ListRequest struct {
	api.PaginationQuery
	listfilter.TimeWindowQuery
	UserID           string `form:"user_id"`
	TokenID          string `form:"token_id"`
	ChannelID        string `form:"channel_id"`
	ModelName        string `form:"model_name"`
	Status           string `form:"status"`
	PrivateChannelID string `form:"private_channel_id"`
}

// InsightsRequest 是 /v1/logs/insights 入参。
// start/end 为 unix 秒;end 缺省 now, start 缺省 end-86400。
// 注意:logs/insights 不接收 gran 参数 —— spark 固定 hour 粒度的最后 24 小时。
type InsightsRequest struct {
	Start int64 `form:"start"`
	End   int64 `form:"end"`
}

// InsightsResponse 是 /v1/logs/insights 返回。
// Totals 包含 total/failed/p95/最慢请求和三条 24-slot spark;
// ErrorByStage 是 stage 维度的失败分布 (admin-only,user scope 该字段为空)。
type InsightsResponse struct {
	Totals       dao.LogsTotals  `json:"totals"`
	ErrorByStage []dao.ErrBucket `json:"error_by_stage"`
}
