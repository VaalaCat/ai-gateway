package monitoring

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// Handler 是 /v1/monitoring/* 端点的容器。
// 当前仅承载 Insights;后续若新增 monitoring 子端点 (alerts、heartbeat 等),
// 都挂在同一个 Handler 上,便于路由聚合。
type Handler struct {
	LogDatabaseReady     func() bool
	MonitoringDataFinder func(app.Application, context.Context) MonitoringDataFinder
}

type MonitoringDataFinder interface {
	ChannelMetrics(dao.ObsRange) ([]dao.ChannelMetric, error)
	AgentMetrics(dao.ObsRange) ([]dao.AgentMetric, error)
	ErrorDistribution(string, dao.ObsRange, dao.Scope) ([]dao.ErrBucket, error)
	DashboardKpis(dao.ObsRange, dao.Scope, dao.ObsFilter) (dao.KpiBundle, error)
	CacheSaving(dao.ObsRange, dao.Scope, dao.ObsFilter) (dao.CacheSaving, error)
}

func LogBacklogFrom(status masterlogqueue.DeliveryStatus) LogBacklog {
	return LogBacklog{
		Pending: status.Queue.Pending, Retry: status.Queue.Retry, Inflight: status.Queue.Inflight,
		Bytes: status.Queue.Bytes, OldestSeconds: int64(status.Queue.OldestAge.Seconds()),
		Dropped: status.Queue.Dropped, LastError: status.LastError, SchemaReady: status.SchemaReady,
	}
}
