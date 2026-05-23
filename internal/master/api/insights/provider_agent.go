package insights

import (
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

// agentInsightProvider 实现 InsightProvider for type=agent。
// 数据源:
//  - Meta:   models.Agent (JOIN 不到则 404 InsightNotFound)
//  - Summary/Trend/Breakdown: usage_hourly_buckets,过滤 agent_id
//  - StageLatency / RecentErrors: usage_logs, 过滤 agent_id
type agentInsightProvider struct{}

// Meta 查 models.Agent;找不到返回 404 InsightNotFound。
func (agentInsightProvider) Meta(ctx Context, id string) (EntityMeta, error) {
	if id == "" {
		return EntityMeta{}, api.ErrorWithCode(404, "InsightNotFound", "agent id is empty", nil)
	}
	a, err := ctx.DAO().Agent().GetByAgentID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return EntityMeta{}, api.ErrorWithCode(404, "InsightNotFound", "agent not found: "+id, map[string]any{"id": id, "type": "agent"})
		}
		return EntityMeta{}, api.InternalError("load agent", err)
	}
	return EntityMeta{
		ID:       a.AgentID,
		Name:     a.Name,
		Online:   a.Status == 1,
		LastSeen: a.LastSeen,
		JoinedAt: a.CreatedAt,
	}, nil
}

// Summary 拼装 agent 在窗口内的 KPI 概览。
//
// 各字段数据源:
//   Requests/Cost/Tokens/SuccessRate ← agentBucketAggregate (hourly buckets, agent_id 过滤)
//   TPSAvg                            ← AgentMetrics 已算好的 sum_completion/sum_gen_ms
//   TTFTP95Ms                         ← usage_logs (单 agent 短窗口 OFFSET 近似)
//
// 注意 buckets 聚合和 AgentMetrics 维度一致 (都按 agent_id 分组),只是 AgentMetrics
// 顺手把 24h spark/tps 算好了,我们这里只挑 tps_avg 复用。
func (agentInsightProvider) Summary(ctx Context, id string, r dao.ObsRange) (SummaryKpis, error) {
	out := SummaryKpis{}

	agg, err := agentBucketAggregate(ctx, id, r)
	if err != nil {
		return SummaryKpis{}, err
	}
	out.Requests = agg.Requests
	out.Cost = agg.Cost
	out.Tokens = agg.Tokens
	if agg.Requests > 0 {
		out.SuccessRate = float64(agg.Success) / float64(agg.Requests)
	}

	if metrics, err := ctx.DAO().Stats().AgentMetrics(r); err == nil {
		for i := range metrics {
			if metrics[i].ID == id {
				out.TPSAvg = metrics[i].TPSAvg
				break
			}
		}
	}
	if ttft, err := agentTTFTP95(ctx, id, r); err == nil {
		out.TTFTP95Ms = ttft
	}
	return out, nil
}

// Trend 按 agent_id 做小时/天的桶聚合,输出 cost/requests/tokens 三指标。
func (agentInsightProvider) Trend(ctx Context, id string, r dao.ObsRange) ([]dao.TimeBucket, error) {
	return agentBuckets(ctx, id, r)
}

// StageLatency 现阶段返回 nil:dao.StageLatencyP95 用 UsageLogListFilter,但 filter
// 没暴露 AgentID 字段。我们不在这里再手撸一遍 5 段 stage p95 (需要 5 次单列 OFFSET 近似),
// 等 Task 2.8 收尾时把 filter 扩展出 AgentID 再补。前端拿到 null 直接隐藏 stage block。
func (agentInsightProvider) StageLatency(_ Context, _ string, _ dao.ObsRange) (*dao.StageLatency, error) {
	return nil, nil
}

// Breakdown 给出 agent 内的 model + channel 下钻 (Top 10 by cost)。
// 复用 leaderboard 已封好的 SQL,但要带 agent_id 过滤 — 现成 method 没暴露这个维度,
// 于是这里走 raw 聚合 (与 leaderboard 同形)。
func (agentInsightProvider) Breakdown(ctx Context, id string, r dao.ObsRange) (Breakdown, error) {
	byModel, _ := agentBreakdownByModel(ctx, id, r)
	byChannel, _ := agentBreakdownByChannel(ctx, id, r)
	return Breakdown{ByModel: byModel, ByChannel: byChannel}, nil
}

// RecentErrors 从 usage_logs 取最近 limit 条失败行 (按 created_at DESC)。
func (agentInsightProvider) RecentErrors(ctx Context, id string, r dao.ObsRange, limit int) ([]ErrorSample, error) {
	if limit <= 0 {
		limit = 10
	}
	db := agentRawDB(ctx)
	if db == nil {
		return nil, nil
	}
	type row struct {
		CreatedAt    int64
		ErrorStage   string
		ChannelName  string
		ModelName    string
		ErrorMessage string
	}
	var rows []row
	err := db.Model(&models.UsageLog{}).
		Where("agent_id = ? AND status = 0 AND created_at >= ? AND created_at < ?", id, r.Start, r.End).
		Select("created_at, error_stage, channel_name, model_name, error_message").
		Order("created_at DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]ErrorSample, 0, len(rows))
	for _, x := range rows {
		out = append(out, ErrorSample{
			Ts:      x.CreatedAt,
			Stage:   x.ErrorStage,
			Channel: x.ChannelName,
			Model:   x.ModelName,
			Message: x.ErrorMessage,
		})
	}
	return out, nil
}

// ----- helpers -----

// agentBucketAgg 是 agent 维度 hourly 聚合结果。
type agentBucketAgg struct {
	Requests int64
	Success  int64
	Cost     int64
	Tokens   int64
}

// agentBucketAggregate 在 [r.Start, r.End) 内对 agent_id 聚合 cost/requests/tokens/success。
func agentBucketAggregate(ctx Context, agentID string, r dao.ObsRange) (agentBucketAgg, error) {
	db := agentRawDB(ctx)
	if db == nil {
		return agentBucketAgg{}, fmt.Errorf("agent insight: db unavailable")
	}
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	var a agentBucketAgg
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("agent_id = ? AND date >= ? AND date <= ?", agentID, startDate, endDate).
		Select(`COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(success_count), 0) AS success,
			COALESCE(SUM(total_cost), 0) AS cost,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens`).
		Scan(&a).Error
	return a, err
}

// agentBuckets 返回 agent 时间序列,逐桶 (hour/day) 给 cost/requests/tokens。
func agentBuckets(ctx Context, agentID string, r dao.ObsRange) ([]dao.TimeBucket, error) {
	db := agentRawDB(ctx)
	if db == nil {
		return nil, nil
	}
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	type row struct {
		Date     string
		Hour     int
		Requests int64
		Tokens   int64
		Cost     int64
	}
	groupCols := "date, hour"
	if r.Gran == dao.GranDay {
		groupCols = "date"
	}
	selectCols := groupCols + `,
		COALESCE(SUM(request_count), 0) AS requests,
		COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,
		COALESCE(SUM(total_cost), 0) AS cost`
	if r.Gran == dao.GranDay {
		selectCols = "date, 0 AS hour,\n\t\tCOALESCE(SUM(request_count), 0) AS requests,\n\t\tCOALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,\n\t\tCOALESCE(SUM(total_cost), 0) AS cost"
	}
	var rows []row
	if err := db.Model(&models.UsageHourlyBucket{}).
		Where("agent_id = ? AND date >= ? AND date <= ?", agentID, startDate, endDate).
		Select(selectCols).
		Group(groupCols).
		Order(groupCols).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]dao.TimeBucket, 0, len(rows))
	for _, x := range rows {
		ts, label := agentBucketTs(x.Date, x.Hour, r.Gran)
		if ts < r.Start || ts >= r.End {
			continue
		}
		out = append(out, dao.TimeBucket{
			Ts: ts, Label: label,
			Cost: x.Cost, Requests: x.Requests, Tokens: x.Tokens,
		})
	}
	return out, nil
}

// agentBreakdownByModel 在 agent 内做 by-model leaderboard (cost desc, top 10)。
func agentBreakdownByModel(ctx Context, agentID string, r dao.ObsRange) ([]dao.LeaderRow, error) {
	db := agentRawDB(ctx)
	if db == nil {
		return nil, nil
	}
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	type row struct {
		Name     string
		Cost     int64
		Requests int64
		Tokens   int64
	}
	var rows []row
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("agent_id = ? AND date >= ? AND date <= ?", agentID, startDate, endDate).
		Select(`model_name AS name,
			COALESCE(SUM(total_cost), 0) AS cost,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens`).
		Group("model_name").
		Order("cost DESC").
		Limit(10).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]dao.LeaderRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, dao.LeaderRow{Name: x.Name, Cost: x.Cost, Requests: x.Requests, Tokens: x.Tokens})
	}
	return out, nil
}

// agentBreakdownByChannel 在 agent 内做 by-channel leaderboard (cost desc, top 10)。
// channel_id > 0 → 排除 BYOK 私域行。
func agentBreakdownByChannel(ctx Context, agentID string, r dao.ObsRange) ([]dao.LeaderRow, error) {
	db := agentRawDB(ctx)
	if db == nil {
		return nil, nil
	}
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	type row struct {
		ID       uint
		Name     string
		Cost     int64
		Requests int64
		Tokens   int64
	}
	var rows []row
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("agent_id = ? AND date >= ? AND date <= ? AND channel_id > 0", agentID, startDate, endDate).
		Select(`channel_id AS id,
			COALESCE(MIN(NULLIF(channel_name, '')), '') AS name,
			COALESCE(SUM(total_cost), 0) AS cost,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens`).
		Group("channel_id").
		Order("cost DESC").
		Limit(10).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]dao.LeaderRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, dao.LeaderRow{ID: x.ID, Name: x.Name, Cost: x.Cost, Requests: x.Requests, Tokens: x.Tokens})
	}
	return out, nil
}

// agentTTFTP95 单 agent 的 ttft p95 (走 usage_logs)。
func agentTTFTP95(ctx Context, agentID string, r dao.ObsRange) (int64, error) {
	db := agentRawDB(ctx)
	if db == nil {
		return 0, nil
	}
	base := func() *gorm.DB {
		return db.Model(&models.UsageLog{}).
			Where("agent_id = ? AND created_at >= ? AND created_at < ? AND is_stream = 1 AND status = 1 AND completion_tokens > 0",
				agentID, r.Start, r.End)
	}
	var cnt int64
	if err := base().Count(&cnt).Error; err != nil {
		return 0, err
	}
	if cnt == 0 {
		return 0, nil
	}
	offset := cnt * 95 / 100
	if offset >= cnt {
		offset = cnt - 1
	}
	var v int64
	err := base().
		Select("first_response_ms").
		Order("first_response_ms ASC").
		Offset(int(offset)).Limit(1).
		Scan(&v).Error
	return v, err
}

// agentBucketTs 把 (date, hour) 翻译为 (ts, label)。复制 dao.bucketTsLabel 语义,
// 因为该 helper unexported。
func agentBucketTs(date string, hour int, gran dao.Gran) (int64, string) {
	t, _ := time.Parse("2006-01-02", date)
	if gran == dao.GranHour {
		ts := t.Add(time.Duration(hour) * time.Hour).Unix()
		return ts, fmt.Sprintf("%s %02d:00", t.Format("01-02"), hour)
	}
	return t.Unix(), date
}

// agentRawDB 从 Context 拿底层 *gorm.DB。
// Context 接口故意不暴露 rawDB() 方法 (保持 mock 友好),provider 实现走类型断言。
// 测试用真实 providerCtx 注入,所以断言总能成功;mock 实现拿到的是 nil → provider
// 内部函数都会 short-circuit 返回零值,避免 panic。
func agentRawDB(ctx Context) *gorm.DB {
	pc, ok := ctx.(*providerCtx)
	if !ok {
		return nil
	}
	return pc.rawDB()
}
