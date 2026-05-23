package monitoring

import (
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// errorWarnRatio 是 error 卡片的红线阈值;高于此值前端显示告警色。
// 5% 是按运营经验给的默认值,后续可改为可配置。
const errorWarnRatio = 0.05

// Insights 是 monitoring 页的总入口。
//
// 全 admin-only:user scope 直接 403 (monitoring 是运维向工具,
// 一般用户不关心 channel/agent 健康度)。
//
// 各子查询失败不阻断主响应:monitoring 是 best-effort 聚合面板,
// 单项失败时退化为零值/空数组。这一行为与 stats.Dashboard 一致。
//
// 唯一会阻断响应的是窗口校验和 KPI 环聚合错误 (后者依赖 DashboardKpis,
// 失败说明 DB 出问题,值得 500)。
func (h *Handler) Insights(c *app.Context, req InsightsRequest) (InsightsResponse, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil || !scope.IsAdmin {
		return InsightsResponse{}, api.ForbiddenError("monitoring is admin-only")
	}
	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return InsightsResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	s := dao.Scope{IsAdmin: true, UserID: scope.UserID}
	q := dao.NewAdminQuery(dao.NewContext(c.App))

	channels, _ := q.Stats().ChannelMetrics(r)
	agents, _ := q.Stats().AgentMetrics(r)
	byStage, _ := q.Stats().ErrorDistribution("stage", r, s)
	byChannel, _ := q.Stats().ErrorDistribution("channel", r, s)
	// nil → 空数组 (前端期待稳定数组)。
	if byStage == nil {
		byStage = []dao.ErrBucket{}
	}
	if byChannel == nil {
		byChannel = []dao.ErrBucket{}
	}

	rings, err := computeKpiRings(q, r, s, agents)
	if err != nil {
		return InsightsResponse{}, api.InternalError("kpi rings", err)
	}

	return InsightsResponse{
		KpiRings: rings,
		Channels: channels,
		Agents:   agents,
		Errors: ErrorBundles{
			ByStage:   byStage,
			ByChannel: byChannel,
		},
	}, nil
}

// computeKpiRings 组装 5 个环形卡片;每个卡片的数据源:
//
//	success  ← DashboardKpis (success_count / requests)
//	cache    ← CacheSaving   (hit_ratio / saved_tokens)
//	agents   ← AgentMetrics  (online/total + qps from total requests)
//	tps      ← AgentMetrics  (各 agent tps 简单算术平均, ratio 固定 1.0)
//	error    ← DashboardKpis (failed = requests - success_count)
//
// 子查询 (cache_saving) 失败不阻断主流程,会退化为零值环。
// DashboardKpis 失败才返回 error (主要数据源)。
func computeKpiRings(q dao.AdminQuery, r dao.ObsRange, s dao.Scope, agents []dao.AgentMetric) (KpiRings, error) {
	kpis, err := q.Stats().DashboardKpis(r, s)
	if err != nil {
		return KpiRings{}, err
	}
	cache, _ := q.Stats().CacheSaving(r, s)

	return KpiRings{
		Success: successRing(kpis),
		Cache:   cacheRing(cache),
		Agents:  agentsRing(agents, kpis, r),
		TPS:     tpsRing(agents),
		Error:   errorRing(kpis),
	}, nil
}

// successRing 计算成功率环;ratio = success_count / requests, value = total reqs。
func successRing(kpis dao.KpiBundle) KpiRing {
	var ratio float64
	if kpis.Requests.Value > 0 && kpis.SuccessRate != nil {
		ratio = float64(kpis.SuccessRate.Value) / float64(kpis.Requests.Value)
	}
	return KpiRing{Ratio: ratio, Value: kpis.Requests.Value, Sub: "reqs"}
}

// cacheRing 计算缓存命中率环;ratio 复用 DAO 已算好的 hit_ratio。
func cacheRing(cache dao.CacheSaving) KpiRing {
	return KpiRing{Ratio: cache.HitRatio, Value: cache.SavedTokens, Sub: "tokens"}
}

// agentsRing 计算 agent 在线率环;
// value 是 "online/total" 字符串 (前端两数共显), sub 写当前窗口的 qps。
func agentsRing(agents []dao.AgentMetric, kpis dao.KpiBundle, r dao.ObsRange) KpiRing {
	total := int64(len(agents))
	var online int64
	for _, a := range agents {
		if a.Online {
			online++
		}
	}
	var ratio float64
	if total > 0 {
		ratio = float64(online) / float64(total)
	}
	var qps float64
	if duration := r.End - r.Start; duration > 0 {
		qps = float64(kpis.Requests.Value) / float64(duration)
	}
	return KpiRing{
		Ratio: ratio,
		Value: fmt.Sprintf("%d/%d", online, total),
		Sub:   fmt.Sprintf("qps %.2f", qps),
	}
}

// tpsRing 计算 agent 平均流式 TPS 环;
// ratio 语义: avg>0 时取 1.0 (积极指标无上限),avg==0 时取 0 表达 "无健康数据"。
// 这样空 agent 列表或全零 stream tps 不会被画成 100% 误导用户。
func tpsRing(agents []dao.AgentMetric) KpiRing {
	var sum float64
	var n int
	for _, a := range agents {
		if a.TPSAvg > 0 {
			sum += a.TPSAvg
			n++
		}
	}
	var avg float64
	if n > 0 {
		avg = sum / float64(n)
	}
	ratio := 1.0
	if avg == 0 {
		ratio = 0
	}
	return KpiRing{Ratio: ratio, Value: avg, Sub: "avg stream"}
}

// errorRing 计算失败率环;
// failed = requests - success_count;ratio = failed/requests;
// warn_above 用 errorWarnRatio (默认 5%)。
func errorRing(kpis dao.KpiBundle) KpiRing {
	var failed int64
	if kpis.SuccessRate != nil {
		failed = kpis.Requests.Value - kpis.SuccessRate.Value
	}
	var ratio float64
	if kpis.Requests.Value > 0 {
		ratio = float64(failed) / float64(kpis.Requests.Value)
	}
	warn := errorWarnRatio
	return KpiRing{
		Ratio:     ratio,
		Value:     failed,
		Sub:       "failed",
		WarnAbove: &warn,
	}
}

// parseObsRange 是 monitoring 端点的统一 query 缺省值解析。
// (与 stats.parseObsRange 同语义, 故意复制一份避免跨 package 依赖。)
func parseObsRange(start, end int64, gran string) dao.ObsRange {
	if end <= 0 {
		end = time.Now().UTC().Unix()
	}
	if start <= 0 {
		start = end - 86400
	}
	g := dao.GranDay
	if gran == "hour" {
		g = dao.GranHour
	}
	return dao.ObsRange{Start: start, End: end, Gran: g}
}
