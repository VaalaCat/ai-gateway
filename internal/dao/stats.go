package dao

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/histutil"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"gorm.io/gorm"
)

type AdminStatsQuery interface {
	GetOverview() (*OverviewStats, error)
	GetTableCount(table KnownTable) (int64, error)
	GetTotalCost(filter UsageLogListFilter) (int64, error)
	GetTrend(days int, userID *uint) ([]TrendItem, error)
	HourlyTrend(r ObsRange, scope Scope, f ObsFilter) ([]TimeBucket, error)
	Distribution(by string, r ObsRange, scope Scope, f ObsFilter) ([]Bucket, error)
	ModelDistribution(r ObsRange, scope Scope, topN int, f ObsFilter) ([]Bucket, error)
	Leaderboard(by, metric string, limit int, r ObsRange, scope Scope, f ObsFilter) ([]LeaderRow, error)
	SpeedCompare(dimension string, r ObsRange, scope Scope, topN int, f ObsFilter) ([]SpeedRow, error)
	ChannelMetrics(r ObsRange) ([]ChannelMetric, error)
	ChannelModelBreakdown(channelID uint, r ObsRange) ([]ChannelModelBreakdownRow, error)
	AgentMetrics(r ObsRange) ([]AgentMetric, error)
	ErrorDistribution(by string, r ObsRange, scope Scope) ([]ErrBucket, error)
	StageLatencyP95(filter UsageLogListFilter, r ObsRange) (StageLatency, error)
	DashboardKpis(r ObsRange, scope Scope, f ObsFilter) (KpiBundle, error)
	DashboardTrend(r ObsRange, scope Scope, f ObsFilter) ([]TimeBucket, error)
	DashboardSuccessRate(r ObsRange, scope Scope, f ObsFilter) (KpiMetric, error)
	CostTrendStackedByModel(r ObsRange, scope Scope, topN int, f ObsFilter) (CostTrendStacked, error)
	MarketShareTrend(dim string, r ObsRange, scope Scope, topN int, f ObsFilter) (CostTrendStacked, error)
	MetricTrendGrouped(metric, stat, dim string, r ObsRange, scope Scope, topN int, f ObsFilter) (MetricTrendStacked, error)
	CacheSaving(r ObsRange, scope Scope, f ObsFilter) (CacheSaving, error)
	LogsTotals(r ObsRange, scope Scope) (LogsTotals, error)
	RecentAgentHealth(sinceUnix int64) ([]AgentRecentHealth, error)
}

// AgentRecentHealth 是近窗内某 agent 的请求/失败计数（算错误率与 QPS 用）。
type AgentRecentHealth struct {
	AgentID  string `gorm:"column:agent_id"`
	Requests int64  `gorm:"column:requests"`
	Failed   int64  `gorm:"column:failed"`
}

// RecentAgentHealth 统计 created_at >= sinceUnix 的 usage_log，按 agent_id 聚合请求数/失败数。
func (q *adminStatsQuery) RecentAgentHealth(sinceUnix int64) ([]AgentRecentHealth, error) {
	db, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	var out []AgentRecentHealth
	err = db.Model(&models.UsageLog{}).
		Select("agent_id, COUNT(*) AS requests, SUM(CASE WHEN status = 0 THEN 1 ELSE 0 END) AS failed").
		Where("created_at >= ? AND agent_id <> ''", sinceUnix).
		Group("agent_id").
		Scan(&out).Error
	return out, WrapLogDatabaseError(err)
}

// SpeedRow 是 SpeedCompare 输出的一行 (维度: model | channel)。
// ID 仅在 dimension=channel 时填充; model 维度无数字主键, ID=0。
// TTFTP95Ms/TPSP5 与 TTFTMs/TPS(均值) 并列,来自 ttft/tps 直方图侧表 (Task 7)。
type SpeedRow struct {
	ID        uint    `json:"id,omitempty"`
	Name      string  `json:"name"`
	TTFTMs    int64   `json:"ttft_ms"`
	TPS       float64 `json:"tps"`
	TTFTP95Ms int64   `json:"ttft_p95_ms"`
	TPSP5     float64 `json:"tps_p5"`
}

// LeaderRow 是 Leaderboard 输出的统一行。
// ID 字段含义随 by 维度变化: by="user" -> user_id, by="channel" -> channel_id,
// by="model" 时 ID = 0 (model 没有数字主键)。
// TPS/TTFTMs 仅在底层数据有 stream 累计时才有意义。
type LeaderRow struct {
	ID       uint    `json:"id,omitempty"`
	Name     string  `json:"name"`
	Cost     int64   `json:"cost"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
	TPS      float64 `json:"tps,omitempty"`
	TTFTMs   int64   `json:"ttft_ms,omitempty"`
}

// leaderboardScanRow 是 Leaderboard 各 helper 内部 Scan 的中间类型。
type leaderboardScanRow struct {
	ID       uint
	Name     string
	Cost     int64
	Requests int64
	Tokens   int64
	TPS      float64
	TTFTMs   int64
}

// Bucket 是 Distribution 输出的统一桶,包含归一化 ratio。
type Bucket struct {
	Name  string  `json:"name"`
	Value int64   `json:"value"`
	Ratio float64 `json:"ratio"`
}

// distributionScanRow 是 Distribution 各 scope helper 的 Scan 中间类型。
type distributionScanRow struct {
	Name  string
	Value int64
}

type adminStatsQuery struct{ ctx *baseContext }

func (q *adminStatsQuery) logDB() (*gorm.DB, error) {
	return q.ctx.LogDB()
}

func (q *adminStatsQuery) requestLogDB() (*gorm.DB, error) {
	db, err := q.logDB()
	if err != nil {
		return nil, err
	}
	mode, err := q.ctx.DatabaseLayoutMode()
	if err != nil {
		return nil, err
	}
	if mode == app.DatabaseLayoutSplit {
		return db.Table("request_logs").Session(&gorm.Session{}), nil
	}
	return db, nil
}

func (q *adminStatsQuery) GetOverview() (*OverviewStats, error) {
	db := q.ctx.GetCoreDB()
	s := &OverviewStats{}
	if err := db.Model(&models.User{}).Count(&s.UserCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.Token{}).Count(&s.TokenCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.Channel{}).Count(&s.ChannelCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.Agent{}).Count(&s.AgentCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.ModelConfig{}).Count(&s.ModelConfigCount).Error; err != nil {
		return nil, err
	}
	logDB, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	if err := logDB.Model(&models.UsageLog{}).Count(&s.UsageLogCount).Error; err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	costQuery := logDB.Model(&models.UsageLog{})
	if err := costQuery.Select("COALESCE(SUM(total_cost), 0)").Scan(&s.TotalCost).Error; err != nil {
		return nil, err
	}
	return s, nil
}

func (q *adminStatsQuery) GetTableCount(table KnownTable) (int64, error) {
	var count int64
	logOwned := table == TableUsageLogs || table == TableUsageLogTraces
	db := q.ctx.GetCoreDB()
	tableName := string(table)
	if table == TableUsageLogs || table == TableUsageLogTraces {
		var err error
		db, err = q.logDB()
		if err != nil {
			return 0, err
		}
		mode, err := q.ctx.DatabaseLayoutMode()
		if err != nil {
			return 0, err
		}
		if mode == app.DatabaseLayoutSplit {
			if table == TableUsageLogs {
				tableName = "request_logs"
			} else {
				tableName = "request_traces"
			}
		}
	}
	err := db.Table(tableName).Count(&count).Error
	if logOwned {
		err = WrapLogDatabaseError(err)
	}
	return count, err
}

func (q *adminStatsQuery) GetTotalCost(filter UsageLogListFilter) (int64, error) {
	db, err := q.requestLogDB()
	if err != nil {
		return 0, err
	}
	db = db.Model(&models.UsageLog{})
	db = applyUsageLogFilter(db, filter)
	var cost int64
	err = db.Select("COALESCE(SUM(total_cost), 0)").Scan(&cost).Error
	return cost, WrapLogDatabaseError(err)
}

func (q *adminStatsQuery) GetTrend(days int, userID *uint) ([]TrendItem, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	db, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	db = db.Model(&models.UsageLog{})
	db = db.Where("created_at >= ?", cutoff)
	if userID != nil {
		db = db.Where("user_id = ?", *userID)
	}

	var items []TrendItem
	err = db.Select(
		"DATE(created_at, 'unixepoch') as date, " +
			"COUNT(*) as requests, " +
			"COALESCE(SUM(prompt_tokens), 0) as prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) as completion_tokens, " +
			"COALESCE(SUM(total_cost), 0) as cost",
	).Group("date").Order("date ASC").Find(&items).Error
	return items, WrapLogDatabaseError(err)
}

func (q *adminStatsQuery) HourlyTrend(r ObsRange, scope Scope, f ObsFilter) ([]TimeBucket, error) {
	if r.End <= r.Start {
		return nil, nil
	}
	uid := f.EffectiveUserID(scope)
	if uid == 0 {
		if f.TokenID != 0 || f.ModelName != "" {
			db, err := q.requestLogDB()
			if err != nil {
				return nil, err
			}
			rows, err := hourlyTrendFromUsageLog(db, r, uid, f.TokenID, f.ModelName)
			return rows, WrapLogDatabaseError(err)
		}
		hourlyDB, err := q.logDB()
		if err != nil {
			return nil, err
		}
		requestDB, err := q.requestLogDB()
		if err != nil {
			return nil, err
		}
		rows, err := hourlyTrendHybrid(hourlyDB, requestDB, r)
		return rows, WrapLogDatabaseError(err)
	}
	if f.TokenID != 0 || f.ModelName != "" || r.Gran == GranHour {
		db, err := q.requestLogDB()
		if err != nil {
			return nil, err
		}
		rows, err := hourlyTrendFromUsageLog(db, r, uid, f.TokenID, f.ModelName)
		return rows, WrapLogDatabaseError(err)
	}
	db, err := q.logDB()
	if err != nil {
		return nil, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	rows, err := hourlyTrendFromTokenDailyHybrid(db, requestDB, r, uid)
	return rows, WrapLogDatabaseError(err)
}

func hourlyTrendHybrid(hourlyDB, requestDB *gorm.DB, r ObsRange) ([]TimeBucket, error) {
	window := splitExactBillingWindow(r.Start, r.End)
	if !window.hasFullBuckets() {
		return hourlyTrendFromUsageLog(requestDB, r, 0, 0, "")
	}
	rows, err := trendAggregatesFromBuckets(hourlyDB, r.Gran, window.fullStart, window.fullEnd)
	if err != nil {
		return nil, err
	}
	for _, boundary := range window.boundaries {
		partial, err := trendAggregatesFromRequestFacts(requestDB, r.Gran, boundary.start, boundary.end, 0)
		if err != nil {
			return nil, err
		}
		rows = append(rows, partial...)
	}
	return mergeTrendAggregates(rows), nil
}

type trendAggregate struct {
	Ts                        int64
	Label                     string
	Cost                      int64
	Requests                  int64
	PromptTokens              int64
	CompletionTokens          int64
	CacheReadTokens           int64
	CacheWriteTokens          int64
	SumFirstResponseMs        int64
	StreamRequestCount        int64
	SumStreamCompletionTokens int64
	SumGenerationMs           int64
}

func trendAggregatesFromBuckets(db *gorm.DB, gran Gran, start, end int64) ([]trendAggregate, error) {
	type row struct {
		Date                      string
		Hour                      int
		Cost                      int64
		Requests                  int64
		PromptTokens              int64
		CompletionTokens          int64
		CacheReadTokens           int64
		CacheWriteTokens          int64
		SumFirstResponseMs        int64
		StreamRequestCount        int64
		SumStreamCompletionTokens int64
		SumGenerationMs           int64
	}
	groupCols := "date, hour"
	if gran == GranDay {
		groupCols = "date"
	}
	var scanned []row
	err := alignedHourWindow(db.Model(&models.UsageHourlyBucket{}), start, end).
		Select(groupCols + `,
			COALESCE(SUM(total_cost),0) cost, COALESCE(SUM(request_count),0) requests,
			COALESCE(SUM(prompt_tokens),0) prompt_tokens, COALESCE(SUM(completion_tokens),0) completion_tokens,
			COALESCE(SUM(cache_read_tokens),0) cache_read_tokens, COALESCE(SUM(cache_write_tokens),0) cache_write_tokens,
			COALESCE(SUM(sum_first_response_ms),0) sum_first_response_ms,
			COALESCE(SUM(stream_request_count),0) stream_request_count,
			COALESCE(SUM(sum_stream_completion_tokens),0) sum_stream_completion_tokens,
			COALESCE(SUM(sum_generation_ms),0) sum_generation_ms`).
		Group(groupCols).Scan(&scanned).Error
	if err != nil {
		return nil, err
	}
	rows := make([]trendAggregate, 0, len(scanned))
	for _, item := range scanned {
		ts, label := bucketTsLabel(item.Date, item.Hour, gran)
		rows = append(rows, trendAggregate{
			Ts: ts, Label: label, Cost: item.Cost, Requests: item.Requests,
			PromptTokens: item.PromptTokens, CompletionTokens: item.CompletionTokens,
			CacheReadTokens: item.CacheReadTokens, CacheWriteTokens: item.CacheWriteTokens,
			SumFirstResponseMs: item.SumFirstResponseMs, StreamRequestCount: item.StreamRequestCount,
			SumStreamCompletionTokens: item.SumStreamCompletionTokens, SumGenerationMs: item.SumGenerationMs,
		})
	}
	return rows, nil
}

func trendAggregatesFromRequestFacts(db *gorm.DB, gran Gran, start, end int64, userID uint) ([]trendAggregate, error) {
	bucketSeconds := int64(3600)
	if gran == GranDay {
		bucketSeconds = 86400
	}
	var rows []trendAggregate
	query := db.Model(&models.UsageLog{}).
		Where("created_at >= ? AND created_at < ?", start, end)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	err := query.
		Select(fmt.Sprintf(`(created_at-created_at %% %d) ts,
			COALESCE(SUM(total_cost),0) cost, COUNT(*) requests,
			COALESCE(SUM(prompt_tokens),0) prompt_tokens, COALESCE(SUM(completion_tokens),0) completion_tokens,
			COALESCE(SUM(cache_read_tokens),0) cache_read_tokens, COALESCE(SUM(cache_write_tokens),0) cache_write_tokens,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN first_response_ms ELSE 0 END),0) sum_first_response_ms,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN 1 ELSE 0 END),0) stream_request_count,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN completion_tokens ELSE 0 END),0) sum_stream_completion_tokens,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN duration-first_response_ms ELSE 0 END),0) sum_generation_ms`, bucketSeconds)).
		Group("ts").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Label = formatBucketLabel(rows[i].Ts, gran)
	}
	return rows, nil
}

func mergeTrendAggregates(rows []trendAggregate) []TimeBucket {
	merged := make(map[int64]trendAggregate)
	for _, row := range rows {
		current := merged[row.Ts]
		current.Ts, current.Label = row.Ts, row.Label
		current.Cost += row.Cost
		current.Requests += row.Requests
		current.PromptTokens += row.PromptTokens
		current.CompletionTokens += row.CompletionTokens
		current.CacheReadTokens += row.CacheReadTokens
		current.CacheWriteTokens += row.CacheWriteTokens
		current.SumFirstResponseMs += row.SumFirstResponseMs
		current.StreamRequestCount += row.StreamRequestCount
		current.SumStreamCompletionTokens += row.SumStreamCompletionTokens
		current.SumGenerationMs += row.SumGenerationMs
		merged[row.Ts] = current
	}
	out := make([]TimeBucket, 0, len(merged))
	for _, row := range merged {
		var ttft int64
		if row.StreamRequestCount > 0 {
			ttft = row.SumFirstResponseMs / row.StreamRequestCount
		}
		var tps float64
		if row.SumGenerationMs > 0 {
			tps = float64(row.SumStreamCompletionTokens) * 1000 / float64(row.SumGenerationMs)
		}
		var cacheHitRate float64
		if denominator := row.PromptTokens + row.CacheReadTokens; denominator > 0 {
			cacheHitRate = float64(row.CacheReadTokens) * 100 / float64(denominator)
		}
		out = append(out, newTimeBucket(row.Ts, row.Label, row.Cost, row.Requests,
			row.PromptTokens, row.CompletionTokens, row.CacheReadTokens, row.CacheWriteTokens,
			ttft, tps, cacheHitRate))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ts < out[j].Ts })
	return out
}

// newTimeBucket 组装 TimeBucket,Tokens = 4 类之和(含 cache),
// 三条聚合路径共用以避免口径漂移。ttftMs/tps/cacheHitRate 是派生速度/命中率序列;
// hourlyTrendHybrid/hourlyTrendFromUsageLog 能算全 3 项;完整 token_daily 日
// 缺逐请求 stream 明细，只能算 cacheHitRate，ttft/tps 保持 0。
func newTimeBucket(ts int64, label string, cost, requests, prompt, completion, cacheRead, cacheWrite, ttftMs int64, tps, cacheHitRate float64) TimeBucket {
	return TimeBucket{
		Ts: ts, Label: label, Cost: cost, Requests: requests,
		Tokens:           prompt + completion + cacheRead + cacheWrite,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		TTFTMs:           ttftMs,
		TPS:              tps,
		CacheHitRate:     cacheHitRate,
	}
}

func hourlyTrendFromUsageLog(db *gorm.DB, r ObsRange, userID, tokenID uint, modelName string) ([]TimeBucket, error) {
	bucketSec := int64(3600)
	if r.Gran == GranDay {
		bucketSec = 86400
	}

	type row struct {
		Bucket           int64
		Requests         int64
		PromptTokens     int64
		CompletionTokens int64
		CacheReadTokens  int64
		CacheWriteTokens int64
		Cost             int64
		TTFTMs           int64
		TPS              float64
		CacheHitRate     float64
	}
	var rows []row
	query := db.Model(&models.UsageLog{}).Where("created_at >= ? AND created_at < ?", r.Start, r.End)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if tokenID != 0 {
		query = query.Where("token_id = ?", tokenID)
	}
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	err := query.
		Select(fmt.Sprintf(`(created_at - (created_at %% %d)) AS bucket,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
			COALESCE(SUM(total_cost), 0) AS cost,`, bucketSec) + usageLogStreamSelect + `,
			CASE WHEN SUM(prompt_tokens) + SUM(cache_read_tokens) > 0
			     THEN SUM(cache_read_tokens) * 100.0 / (SUM(prompt_tokens) + SUM(cache_read_tokens))
			     ELSE 0 END AS cache_hit_rate`).
		Group("bucket").
		Order("bucket").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]TimeBucket, 0, len(rows))
	for _, x := range rows {
		out = append(out, newTimeBucket(x.Bucket, formatBucketLabel(x.Bucket, r.Gran),
			x.Cost, x.Requests, x.PromptTokens, x.CompletionTokens, x.CacheReadTokens, x.CacheWriteTokens,
			x.TTFTMs, x.TPS, x.CacheHitRate))
	}
	return out, nil
}

// hourlyTrendFromTokenDailyHybrid merges complete UTC days from
// token_daily_billings with exact request facts from partial boundary days.
func hourlyTrendFromTokenDailyHybrid(dailyDB, requestDB *gorm.DB, r ObsRange, userID uint) ([]TimeBucket, error) {
	window := splitExactDailyWindow(r.Start, r.End)
	if !window.hasFullBuckets() {
		return hourlyTrendFromUsageLog(requestDB, r, userID, 0, "")
	}
	rows, err := trendAggregatesFromTokenDaily(dailyDB, window.fullStart, window.fullEnd, userID)
	if err != nil {
		return nil, err
	}
	for _, boundary := range window.boundaries {
		partial, err := trendAggregatesFromRequestFacts(requestDB, GranDay, boundary.start, boundary.end, userID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, partial...)
	}
	return mergeTrendAggregates(rows), nil
}

// hourlyTrendFromTokenDaily reads complete UTC days only. Production callers
// that may receive partial days use hourlyTrendFromTokenDailyHybrid.
func hourlyTrendFromTokenDaily(db *gorm.DB, r ObsRange, userID uint) ([]TimeBucket, error) {
	rows, err := trendAggregatesFromTokenDaily(db, r.Start, r.End, userID)
	if err != nil {
		return nil, err
	}
	return mergeTrendAggregates(rows), nil
}

func trendAggregatesFromTokenDaily(db *gorm.DB, start, end int64, userID uint) ([]trendAggregate, error) {
	startDate := time.Unix(start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(end, 0).UTC().Format("2006-01-02")
	type row struct {
		Date             string
		Requests         int64
		PromptTokens     int64
		CompletionTokens int64
		CacheReadTokens  int64
		CacheWriteTokens int64
		Cost             int64
	}
	var rows []row
	err := db.Model(&models.TokenDailyBilling{}).
		Where("user_id = ? AND date >= ? AND date < ?", userID, startDate, endDate).
		Select(`date,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
			COALESCE(SUM(total_cost), 0) AS cost`).
		Group("date").
		Order("date").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]trendAggregate, 0, len(rows))
	for _, x := range rows {
		t, _ := time.Parse("2006-01-02", x.Date)
		ts := t.Unix()
		out = append(out, trendAggregate{
			Ts: ts, Label: x.Date, Cost: x.Cost, Requests: x.Requests,
			PromptTokens: x.PromptTokens, CompletionTokens: x.CompletionTokens,
			CacheReadTokens: x.CacheReadTokens, CacheWriteTokens: x.CacheWriteTokens,
		})
	}
	return out, nil
}

func (q *adminStatsQuery) Distribution(by string, r ObsRange, scope Scope, f ObsFilter) (out []Bucket, err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if by != "model" {
		return nil, fmt.Errorf("distribution: unsupported dimension %q", by)
	}
	hourlyDB, err := q.logDB()
	if err != nil {
		return nil, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	return distributionByModelHybrid(hourlyDB, requestDB, r, f.EffectiveUserID(scope), f.ModelName)
}

// ModelDistribution returns request-count-ranked models with the remainder
// folded into a final "others" bucket.
func (q *adminStatsQuery) ModelDistribution(r ObsRange, scope Scope, topN int, f ObsFilter) ([]Bucket, error) {
	if topN <= 0 {
		topN = 5
	}
	rows, err := q.Distribution("model", r, scope, f)
	if err != nil {
		return nil, err
	}
	return foldDistributionBuckets(rows, topN), nil
}

func foldDistributionBuckets(buckets []Bucket, topN int) []Bucket {
	sort.SliceStable(buckets, func(i, j int) bool {
		if buckets[i].Value != buckets[j].Value {
			return buckets[i].Value > buckets[j].Value
		}
		return buckets[i].Name < buckets[j].Name
	})
	selected := buckets
	if len(buckets) > topN {
		var othersValue int64
		for _, bucket := range buckets[topN:] {
			othersValue += bucket.Value
		}
		selected = append([]Bucket(nil), buckets[:topN]...)
		selected = append(selected, Bucket{Name: "others", Value: othersValue})
	}
	var total int64
	for _, bucket := range selected {
		total += bucket.Value
	}
	for i := range selected {
		if total > 0 {
			selected[i].Ratio = float64(selected[i].Value) / float64(total)
		}
	}
	return selected
}

func distributionByModelFromBuckets(db *gorm.DB, start, end int64) ([]Bucket, error) {
	var rows []distributionScanRow
	query := alignedHourWindow(db.Model(&models.UsageHourlyBucket{}), start, end)
	err := query.
		Select("model_name AS name, COALESCE(SUM(request_count), 0) AS value").
		Group("model_name").
		Order("value DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return normalizeBuckets(rows), nil
}

func distributionByModelFromUsageLog(db *gorm.DB, r ObsRange, userID uint, modelName string) ([]Bucket, error) {
	var rows []distributionScanRow
	query := db.Model(&models.UsageLog{}).
		Where("created_at >= ? AND created_at < ?", r.Start, r.End)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	err := query.
		Select("model_name AS name, COUNT(*) AS value").
		Group("model_name").
		Order("value DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return normalizeBuckets(rows), nil
}

func distributionByModelHybrid(hourlyDB, requestDB *gorm.DB, r ObsRange, userID uint, modelName string) ([]Bucket, error) {
	if userID != 0 || modelName != "" {
		return distributionByModelFromUsageLog(requestDB, r, userID, modelName)
	}
	window := splitExactBillingWindow(r.Start, r.End)
	merged := make(map[string]int64)
	add := func(rows []Bucket) {
		for _, row := range rows {
			merged[row.Name] += row.Value
		}
	}
	if window.hasFullBuckets() {
		rows, err := distributionByModelFromBuckets(hourlyDB, window.fullStart, window.fullEnd)
		if err != nil {
			return nil, err
		}
		add(rows)
	}
	for _, boundary := range window.boundaries {
		rows, err := distributionByModelFromUsageLog(requestDB, ObsRange{Start: boundary.start, End: boundary.end, Gran: r.Gran}, 0, "")
		if err != nil {
			return nil, err
		}
		add(rows)
	}
	rows := make([]distributionScanRow, 0, len(merged))
	for name, value := range merged {
		rows = append(rows, distributionScanRow{Name: name, Value: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Value != rows[j].Value {
			return rows[i].Value > rows[j].Value
		}
		return rows[i].Name < rows[j].Name
	})
	return normalizeBuckets(rows), nil
}

// normalizeBuckets converts internal scan rows to []Bucket with ratio = value/total.
func normalizeBuckets(rows []distributionScanRow) []Bucket {
	var total int64
	for _, r := range rows {
		total += r.Value
	}
	out := make([]Bucket, 0, len(rows))
	for _, r := range rows {
		var ratio float64
		if total > 0 {
			ratio = float64(r.Value) / float64(total)
		}
		out = append(out, Bucket{Name: r.Name, Value: r.Value, Ratio: ratio})
	}
	return out
}

func bucketTsLabel(date string, hour int, gran Gran) (int64, string) {
	t, _ := time.Parse("2006-01-02", date)
	if gran == GranHour {
		ts := t.Add(time.Duration(hour) * time.Hour).Unix()
		return ts, fmt.Sprintf("%s %02d:00", t.Format("01-02"), hour)
	}
	return t.Unix(), date
}

func formatBucketLabel(ts int64, gran Gran) string {
	t := time.Unix(ts, 0).UTC()
	if gran == GranHour {
		return t.Format("01-02 15:00")
	}
	return t.Format("2006-01-02")
}

func (q *adminStatsQuery) Leaderboard(by, metric string, limit int, r ObsRange, scope Scope, f ObsFilter) ([]LeaderRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	metric = normalizeLeaderboardMetric(metric)
	uid := f.EffectiveUserID(scope)
	switch by {
	case "user":
		if !scope.IsAdmin {
			return nil, nil // 非 admin:用户榜无意义(只能看自己)
		}
		if uid != 0 {
			return nil, nil // admin 锁定了某个用户:用户榜退化为单行,前端隐藏
		}
		db, err := q.requestLogDB()
		if err != nil {
			return nil, err
		}
		rows, err := leaderboardByUserFromRequestFacts(db, metric, limit, r, f.ModelName)
		if err != nil {
			return nil, WrapLogDatabaseError(err)
		}
		return q.withUserNames(rows)
	case "model":
		hourlyDB, err := q.logDB()
		if err != nil {
			return nil, err
		}
		requestDB, err := q.requestLogDB()
		if err != nil {
			return nil, err
		}
		rows, err := leaderboardByDimensionHybrid(hourlyDB, requestDB, "model", metric, limit, r, uid, f.ModelName)
		return rows, WrapLogDatabaseError(err)
	case "channel":
		hourlyDB, err := q.logDB()
		if err != nil {
			return nil, err
		}
		requestDB, err := q.requestLogDB()
		if err != nil {
			return nil, err
		}
		rows, err := leaderboardByDimensionHybrid(hourlyDB, requestDB, "channel", metric, limit, r, uid, f.ModelName)
		return rows, WrapLogDatabaseError(err)
	default:
		return nil, fmt.Errorf("leaderboard: unsupported by %q", by)
	}
}

type leaderboardComponentsRow struct {
	ID                        uint
	Name                      string
	Cost                      int64
	Requests                  int64
	Tokens                    int64
	SumFirstResponseMs        int64
	StreamRequestCount        int64
	SumStreamCompletionTokens int64
	SumGenerationMs           int64
}

func leaderboardByDimensionHybrid(hourlyDB, requestDB *gorm.DB, dimension, metric string, limit int, r ObsRange, userID uint, modelName string) ([]LeaderRow, error) {
	var components []leaderboardComponentsRow
	if userID != 0 || modelName != "" {
		rows, err := leaderboardComponentsFromRequestFacts(requestDB, dimension, r.Start, r.End, userID, modelName)
		if err != nil {
			return nil, err
		}
		components = append(components, rows...)
	} else {
		window := splitExactBillingWindow(r.Start, r.End)
		if window.hasFullBuckets() {
			rows, err := leaderboardComponentsFromHourly(hourlyDB, dimension, window.fullStart, window.fullEnd)
			if err != nil {
				return nil, err
			}
			components = append(components, rows...)
		}
		for _, boundary := range window.boundaries {
			rows, err := leaderboardComponentsFromRequestFacts(requestDB, dimension, boundary.start, boundary.end, 0, "")
			if err != nil {
				return nil, err
			}
			components = append(components, rows...)
		}
	}
	rows := mergeLeaderboardComponents(components, dimension, metric)
	sortLeaderboardRows(rows, metric)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func mergeLeaderboardComponents(components []leaderboardComponentsRow, dimension, metric string) []LeaderRow {
	merged := make(map[string]leaderboardComponentsRow)
	for _, row := range components {
		key := row.Name
		if dimension == "channel" {
			key = fmt.Sprint(row.ID)
		}
		current := merged[key]
		current.ID = row.ID
		if current.Name == "" || row.Name != "" && row.Name < current.Name {
			current.Name = row.Name
		}
		current.Cost += row.Cost
		current.Requests += row.Requests
		current.Tokens += row.Tokens
		current.SumFirstResponseMs += row.SumFirstResponseMs
		current.StreamRequestCount += row.StreamRequestCount
		current.SumStreamCompletionTokens += row.SumStreamCompletionTokens
		current.SumGenerationMs += row.SumGenerationMs
		merged[key] = current
	}
	rows := make([]LeaderRow, 0, len(merged))
	for _, row := range merged {
		item := LeaderRow{ID: row.ID, Name: row.Name, Cost: row.Cost, Requests: row.Requests, Tokens: row.Tokens}
		if row.SumGenerationMs > 0 {
			item.TPS = float64(row.SumStreamCompletionTokens) * 1000 / float64(row.SumGenerationMs)
		}
		if row.StreamRequestCount > 0 {
			item.TTFTMs = row.SumFirstResponseMs / row.StreamRequestCount
		}
		if !leaderboardNeedsStream(metric) || row.StreamRequestCount > 0 {
			rows = append(rows, item)
		}
	}
	return rows
}

func sortLeaderboardRows(rows []LeaderRow, metric string) {
	sort.Slice(rows, func(i, j int) bool {
		switch metric {
		case "requests":
			if rows[i].Requests != rows[j].Requests {
				return rows[i].Requests > rows[j].Requests
			}
		case "tokens":
			if rows[i].Tokens != rows[j].Tokens {
				return rows[i].Tokens > rows[j].Tokens
			}
		case "tps":
			if rows[i].TPS != rows[j].TPS {
				return rows[i].TPS > rows[j].TPS
			}
		case "ttft":
			if rows[i].TTFTMs != rows[j].TTFTMs {
				return rows[i].TTFTMs < rows[j].TTFTMs
			}
		default:
			if rows[i].Cost != rows[j].Cost {
				return rows[i].Cost > rows[j].Cost
			}
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].ID < rows[j].ID
	})
}

func leaderboardComponentsFromHourly(db *gorm.DB, dimension string, start, end int64) ([]leaderboardComponentsRow, error) {
	id, name, group, err := leaderboardDimensionSQL(dimension)
	if err != nil {
		return nil, err
	}
	var rows []leaderboardComponentsRow
	query := alignedHourWindow(db.Model(&models.UsageHourlyBucket{}), start, end)
	if dimension == "channel" {
		query = query.Where("channel_id > 0")
	}
	err = query.Select(fmt.Sprintf(`%s AS id, %s AS name,
		COALESCE(SUM(total_cost),0) cost, COALESCE(SUM(request_count),0) requests,
		COALESCE(SUM(prompt_tokens+completion_tokens+cache_read_tokens+cache_write_tokens),0) tokens,
		COALESCE(SUM(sum_first_response_ms),0) sum_first_response_ms,
		COALESCE(SUM(stream_request_count),0) stream_request_count,
		COALESCE(SUM(sum_stream_completion_tokens),0) sum_stream_completion_tokens,
		COALESCE(SUM(sum_generation_ms),0) sum_generation_ms`, id, name)).Group(group).Scan(&rows).Error
	return rows, err
}

func leaderboardComponentsFromRequestFacts(db *gorm.DB, dimension string, start, end int64, userID uint, modelName string) ([]leaderboardComponentsRow, error) {
	id, name, group, err := leaderboardDimensionSQL(dimension)
	if err != nil {
		return nil, err
	}
	query := db.Model(&models.UsageLog{}).Where("created_at >= ? AND created_at < ?", start, end)
	if dimension == "channel" {
		query = query.Where("channel_id > 0")
	}
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	var rows []leaderboardComponentsRow
	err = query.Select(fmt.Sprintf(`%s AS id, %s AS name,
		COALESCE(SUM(total_cost),0) cost, COUNT(*) requests,
		COALESCE(SUM(prompt_tokens+completion_tokens+cache_read_tokens+cache_write_tokens),0) tokens,
		COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND first_response_ms>0 THEN first_response_ms ELSE 0 END),0) sum_first_response_ms,
		COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND first_response_ms>0 THEN 1 ELSE 0 END),0) stream_request_count,
		COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 AND duration-first_response_ms>0 THEN completion_tokens ELSE 0 END),0) sum_stream_completion_tokens,
		COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 AND duration-first_response_ms>0 THEN duration-first_response_ms ELSE 0 END),0) sum_generation_ms`, id, name)).Group(group).Scan(&rows).Error
	return rows, err
}

func leaderboardDimensionSQL(dimension string) (id, name, group string, err error) {
	switch dimension {
	case "model":
		return "0", "model_name", "model_name", nil
	case "channel":
		return "channel_id", "COALESCE(MIN(NULLIF(channel_name, '')), '')", "channel_id", nil
	default:
		return "", "", "", fmt.Errorf("leaderboard: unsupported by %q", dimension)
	}
}

func normalizeLeaderboardMetric(m string) string {
	switch m {
	case "cost", "requests", "tokens", "tps", "ttft":
		return m
	default:
		return "cost"
	}
}

// leaderboardOrderClause 返回排序子句; ttft 越小越好其它 DESC。
func leaderboardOrderClause(metric string) string {
	switch metric {
	case "requests":
		return "requests DESC"
	case "tokens":
		return "tokens DESC"
	case "tps":
		return "tps DESC"
	case "ttft":
		return "ttft_ms ASC"
	default:
		return "cost DESC"
	}
}

// leaderboardNeedsStream 标记 metric 是否依赖 stream 累计字段; 用于附加 HAVING。
func leaderboardNeedsStream(metric string) bool {
	return metric == "tps" || metric == "ttft"
}

func rowsToLeaderRows(rows []leaderboardScanRow) []LeaderRow {
	out := make([]LeaderRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, LeaderRow{
			ID: r.ID, Name: r.Name,
			Cost: r.Cost, Requests: r.Requests, Tokens: r.Tokens,
			TPS: r.TPS, TTFTMs: r.TTFTMs,
		})
	}
	return out
}

// hourlyBucketStreamSelect 是 UsageHourlyBucket 上 tps/ttft 的累计聚合表达式。
const hourlyBucketStreamSelect = `
	CASE WHEN SUM(sum_generation_ms) > 0
	     THEN (SUM(sum_stream_completion_tokens) * 1000.0) / SUM(sum_generation_ms)
	     ELSE 0 END AS tps,
	CASE WHEN SUM(stream_request_count) > 0
	     THEN SUM(sum_first_response_ms) / SUM(stream_request_count)
	     ELSE 0 END AS ttft_ms`

// usageLogStreamSelect 是 UsageLog 上 tps/ttft 的累计表达式 (无聚合列, 现算)。
const usageLogStreamSelect = `
	CASE WHEN SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN duration - first_response_ms ELSE 0 END) > 0
	     THEN (SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN completion_tokens ELSE 0 END) * 1000.0)
	          / SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN duration - first_response_ms ELSE 0 END)
	     ELSE 0 END AS tps,
	CASE WHEN SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN 1 ELSE 0 END) > 0
	     THEN SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN first_response_ms ELSE 0 END)
	          / SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN 1 ELSE 0 END)
	     ELSE 0 END AS ttft_ms`

func (q *adminStatsQuery) SpeedCompare(dimension string, r ObsRange, scope Scope, topN int, f ObsFilter) (out []SpeedRow, err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if !scope.IsAdmin {
		return nil, nil
	}
	hourlyDB, err := q.logDB()
	if err != nil {
		return nil, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	finder, ok := speedCompareFinders[dimension]
	if !ok {
		return nil, fmt.Errorf("speed_compare: unsupported dimension %q", dimension)
	}
	return finder(hourlyDB, requestDB, r, topN, f.ModelName)
}

type speedCompareFinder func(*gorm.DB, *gorm.DB, ObsRange, int, string) ([]SpeedRow, error)

var speedCompareFinders = map[string]speedCompareFinder{
	"model":   speedCompareByModel,
	"channel": speedCompareByChannel,
}

// ---- Task 7: TTFT p95 / TPS p5 percentile queries ----
//
// 完整小时从 UsageTTFTHistogram / UsageTPSHistogram / UsageDurationHistogram
// 一次性分组读取；部分边界小时从 request facts 构造同档位样本，合并后再估算。

// histSumSelectFrag 是 SUM(h0)..SUM(h16) 的公共 SELECT 片段;duration/ttft/tps
// 三张直方图侧表列名一致,可直接共用同一个片段与同一组扫描行结构体。
const histSumSelectFrag = `COALESCE(SUM(h0),0) AS s0, COALESCE(SUM(h1),0) AS s1, COALESCE(SUM(h2),0) AS s2,
	COALESCE(SUM(h3),0) AS s3, COALESCE(SUM(h4),0) AS s4, COALESCE(SUM(h5),0) AS s5,
	COALESCE(SUM(h6),0) AS s6, COALESCE(SUM(h7),0) AS s7, COALESCE(SUM(h8),0) AS s8,
	COALESCE(SUM(h9),0) AS s9, COALESCE(SUM(h10),0) AS s10, COALESCE(SUM(h11),0) AS s11,
	COALESCE(SUM(h12),0) AS s12, COALESCE(SUM(h13),0) AS s13, COALESCE(SUM(h14),0) AS s14,
	COALESCE(SUM(h15),0) AS s15, COALESCE(SUM(h16),0) AS s16`

// histGroupRowStr scans exact full-hour histograms grouped by a stable string
// identity (model name, agent ID, or a decimal channel ID).
type histGroupRowStr struct {
	Key string `gorm:"column:grp_key"`
	Max int64  `gorm:"column:max_ms"`
	H0  int64  `gorm:"column:s0"`
	H1  int64  `gorm:"column:s1"`
	H2  int64  `gorm:"column:s2"`
	H3  int64  `gorm:"column:s3"`
	H4  int64  `gorm:"column:s4"`
	H5  int64  `gorm:"column:s5"`
	H6  int64  `gorm:"column:s6"`
	H7  int64  `gorm:"column:s7"`
	H8  int64  `gorm:"column:s8"`
	H9  int64  `gorm:"column:s9"`
	H10 int64  `gorm:"column:s10"`
	H11 int64  `gorm:"column:s11"`
	H12 int64  `gorm:"column:s12"`
	H13 int64  `gorm:"column:s13"`
	H14 int64  `gorm:"column:s14"`
	H15 int64  `gorm:"column:s15"`
	H16 int64  `gorm:"column:s16"`
}

func (h histGroupRowStr) counts17() [17]int64 {
	return [17]int64{h.H0, h.H1, h.H2, h.H3, h.H4, h.H5, h.H6, h.H7, h.H8, h.H9, h.H10, h.H11, h.H12, h.H13, h.H14, h.H15, h.H16}
}

// ttftP95ByChannel 按 channel_id 一次性分组取回窗口内每渠道的 TTFT p95(ms);
// modelFilter=="" 表示不筛模型。避免 ChannelMetrics/speedCompareByChannel 逐渠道各查一次。
func ttftP95ByChannel(db *gorm.DB, r ObsRange, modelFilter string, requestDB ...*gorm.DB) (map[uint]int64, error) {
	values, err := findExactPercentiles(db, requestFactsDB(db, requestDB), r, "channel", "ttft", modelFilter, "")
	if err != nil {
		return nil, err
	}
	return exactUintPercentiles(values)
}

// tpsP5ByChannel 是 ttftP95ByChannel 的 TPS 版(speedCompareByChannel 用)。
func tpsP5ByChannel(db *gorm.DB, r ObsRange, modelFilter string, requestDB ...*gorm.DB) (map[uint]float64, error) {
	values, err := findExactPercentiles(db, requestFactsDB(db, requestDB), r, "channel", "tps", modelFilter, "")
	if err != nil {
		return nil, err
	}
	parsed, err := exactUintPercentiles(values)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]float64, len(parsed))
	for id, value := range parsed {
		out[id] = float64(value)
	}
	return out, nil
}

// latencyP95ByChannel 按 channel_id 分组取回窗口内每渠道的请求耗时 p95(ms,durhist)。
// ChannelMetrics.LatencyP95Ms 用。
func latencyP95ByChannel(db *gorm.DB, r ObsRange, requestDB ...*gorm.DB) (map[uint]int64, error) {
	values, err := findExactPercentiles(db, requestFactsDB(db, requestDB), r, "channel", "duration", "", "")
	if err != nil {
		return nil, err
	}
	return exactUintPercentiles(values)
}

// ttftP95ByAgent 按 agent_id 一次性分组取回窗口内每 agent 的 TTFT p95(ms)。
func ttftP95ByAgent(db *gorm.DB, r ObsRange, requestDB ...*gorm.DB) (map[string]int64, error) {
	return findExactPercentiles(db, requestFactsDB(db, requestDB), r, "agent", "ttft", "", "agent_id <> ''")
}

// tpsP5ByAgent 是 ttftP95ByAgent 的 TPS 版(tpshist),AgentMetrics.TPSP5 用(Task 16)。
func tpsP5ByAgent(db *gorm.DB, r ObsRange, requestDB ...*gorm.DB) (map[string]float64, error) {
	values, err := findExactPercentiles(db, requestFactsDB(db, requestDB), r, "agent", "tps", "", "agent_id <> ''")
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(values))
	for id, value := range values {
		out[id] = float64(value)
	}
	return out, nil
}

// latencyP95ByAgent 是 ttftP95ByAgent 的耗时版(durhist),AgentMetrics.LatencyP95Ms 用。
func latencyP95ByAgent(db *gorm.DB, r ObsRange, requestDB ...*gorm.DB) (map[string]int64, error) {
	return findExactPercentiles(db, requestFactsDB(db, requestDB), r, "agent", "duration", "", "agent_id <> ''")
}

// ttftP95ByModel / tpsP5ByModel 按 model_name 一次性分组取回慢尾分位,
// speedCompareByModel 用(避免逐模型各查一次)。
func ttftP95ByModel(db *gorm.DB, r ObsRange, modelFilter string, requestDB ...*gorm.DB) (map[string]int64, error) {
	return findExactPercentiles(db, requestFactsDB(db, requestDB), r, "model", "ttft", modelFilter, "")
}

func tpsP5ByModel(db *gorm.DB, r ObsRange, modelFilter string, requestDB ...*gorm.DB) (map[string]float64, error) {
	values, err := findExactPercentiles(db, requestFactsDB(db, requestDB), r, "model", "tps", modelFilter, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(values))
	for name, value := range values {
		out[name] = float64(value)
	}
	return out, nil
}

func speedCompareByModel(hourlyDB, requestDB *gorm.DB, r ObsRange, topN int, modelName string) ([]SpeedRow, error) {
	rows, err := findExactUsageRows(hourlyDB, requestDB, r, "model", modelName, "")
	if err != nil {
		return nil, err
	}
	out := speedRowsFromExactUsage(rows)
	ttftP95s, err := ttftP95ByModel(hourlyDB, r, modelName, requestDB)
	if err != nil {
		return nil, err
	}
	tpsP5s, err := tpsP5ByModel(hourlyDB, r, modelName, requestDB)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].TTFTP95Ms = ttftP95s[out[i].Name]
		out[i].TPSP5 = tpsP5s[out[i].Name]
	}
	return pickSpeedRankingRows(out, topN), nil
}

func speedCompareByChannel(hourlyDB, requestDB *gorm.DB, r ObsRange, topN int, modelName string) ([]SpeedRow, error) {
	rows, err := findExactUsageRows(hourlyDB, requestDB, r, "channel", modelName, "")
	if err != nil {
		return nil, err
	}
	out := speedRowsFromExactUsage(rows)
	ttftP95s, err := ttftP95ByChannel(hourlyDB, r, modelName, requestDB)
	if err != nil {
		return nil, err
	}
	tpsP5s, err := tpsP5ByChannel(hourlyDB, r, modelName, requestDB)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].TTFTP95Ms = ttftP95s[out[i].ID]
		out[i].TPSP5 = tpsP5s[out[i].ID]
	}
	return pickSpeedRankingRows(out, topN), nil
}

func speedRowsFromExactUsage(rows []exactUsageRow) []SpeedRow {
	out := make([]SpeedRow, 0, len(rows))
	for _, row := range rows {
		hasTTFT := row.StreamRequests > 0 && row.SumFirstResponseMs > 0
		hasTPS := row.SumGenerationMs > 0 && row.SumStreamTokens > 0
		if !hasTTFT && !hasTPS {
			continue
		}
		speed := SpeedRow{ID: row.ID, Name: row.Name}
		if hasTTFT {
			speed.TTFTMs = row.SumFirstResponseMs / row.StreamRequests
		}
		if hasTPS {
			speed.TPS = float64(row.SumStreamTokens) * 1000 / float64(row.SumGenerationMs)
		}
		out = append(out, speed)
	}
	return out
}

func pickSpeedRankingRows(rows []SpeedRow, topN int) []SpeedRow {
	if topN <= 0 || len(rows) == 0 {
		return nil
	}

	byTTFT := append([]SpeedRow(nil), rows...)
	sort.SliceStable(byTTFT, func(i, j int) bool {
		left, right := byTTFT[i], byTTFT[j]
		leftValid, rightValid := left.TTFTP95Ms > 0, right.TTFTP95Ms > 0
		if leftValid != rightValid {
			return leftValid
		}
		if leftValid && left.TTFTP95Ms != right.TTFTP95Ms {
			return left.TTFTP95Ms < right.TTFTP95Ms
		}
		return speedRowIdentityLess(left, right)
	})

	byTPS := append([]SpeedRow(nil), rows...)
	sort.SliceStable(byTPS, func(i, j int) bool {
		left, right := byTPS[i], byTPS[j]
		leftValid, rightValid := left.TPSP5 > 0, right.TPSP5 > 0
		if leftValid != rightValid {
			return leftValid
		}
		if leftValid && left.TPSP5 != right.TPSP5 {
			return left.TPSP5 > right.TPSP5
		}
		return speedRowIdentityLess(left, right)
	})

	type rowKey struct {
		id   uint
		name string
	}
	selected := make(map[rowKey]SpeedRow, min(len(rows), topN*2))
	for _, ranked := range [][]SpeedRow{byTTFT, byTPS} {
		limit := min(topN, len(ranked))
		for _, row := range ranked[:limit] {
			selected[rowKey{id: row.ID, name: row.Name}] = row
		}
	}

	out := make([]SpeedRow, 0, len(selected))
	for _, row := range selected {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TTFTMs != out[j].TTFTMs {
			return out[i].TTFTMs < out[j].TTFTMs
		}
		return speedRowIdentityLess(out[i], out[j])
	})
	return out
}

func speedRowIdentityLess(left, right SpeedRow) bool {
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.ID < right.ID
}

// ChannelMetric 是 Monitoring 页面 channel 维度的一行,聚合 24h 内 channel 用量。
// TTFTP95Ms 来自 UsageTTFTHistogram、LatencyP95Ms 来自 UsageDurationHistogram,
// 均按 channel_id 一次性分组查询后回填(Task 7)。
type ChannelMetric struct {
	ID           uint    `json:"id"`
	Name         string  `json:"name"`
	Requests     int64   `json:"requests"`
	ErrorRatio   float64 `json:"error_ratio"`
	TTFTAvgMs    int64   `json:"ttft_avg_ms"`
	TTFTP95Ms    int64   `json:"ttft_p95_ms"`
	TPSAvg       float64 `json:"tps_avg"`
	TPSP5        float64 `json:"tps_p5"`
	LatencyP95Ms int64   `json:"latency_p95_ms"`
	Spark24h     []int64 `json:"spark_24h"`
}

// AgentMetric 是 Monitoring 页面 agent 维度的一行,聚合 24h 内 agent 用量,
// 并从 core DB 批量读取 models.Agent 的 Name/Status/LastSeen 后在内存合并。
// TTFTP95Ms / LatencyP95Ms 同 ChannelMetric,按 agent_id 一次性分组回填(Task 7)。
// TTFTAvgMs / TPSP5 补齐 TTFT avg+p95 与 TPS avg+p5 双值口径(Task 16),与 ChannelMetric 对称。
type AgentMetric struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Online       bool    `json:"online"`
	LastSeen     int64   `json:"last_seen"`
	Requests     int64   `json:"requests"`
	TTFTAvgMs    int64   `json:"ttft_avg_ms"`
	TTFTP95Ms    int64   `json:"ttft_p95_ms"`
	TPSAvg       float64 `json:"tps_avg"`
	TPSP5        float64 `json:"tps_p5"`
	LatencyP95Ms int64   `json:"latency_p95_ms"`
	Spark24h     []int64 `json:"spark_24h"`
}

// ChannelMetrics 返回 Monitoring 页面 channel 维度的指标行;
// 过滤 channel_id > 0 → 排除 BYOK 行 (Monitoring 页只看 admin channel)。
func (q *adminStatsQuery) ChannelMetrics(r ObsRange) (out []ChannelMetric, err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	hourlyDB, err := q.logDB()
	if err != nil {
		return nil, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	aggs, err := findExactUsageRows(hourlyDB, requestDB, r, "channel", "", "channel_id > 0")
	if err != nil {
		return nil, err
	}
	sort.Slice(aggs, func(i, j int) bool {
		if aggs[i].Requests != aggs[j].Requests {
			return aggs[i].Requests > aggs[j].Requests
		}
		return aggs[i].ID < aggs[j].ID
	})

	sparks, err := channelSpark24h(hourlyDB, requestDB, r)
	if err != nil {
		return nil, err
	}

	ttftP95s, err := ttftP95ByChannel(hourlyDB, r, "", requestDB)
	if err != nil {
		return nil, err
	}
	tpsP5s, err := tpsP5ByChannel(hourlyDB, r, "", requestDB)
	if err != nil {
		return nil, err
	}
	latencyP95s, err := latencyP95ByChannel(hourlyDB, r, requestDB)
	if err != nil {
		return nil, err
	}

	out = make([]ChannelMetric, 0, len(aggs))
	for _, a := range aggs {
		var errorRatio float64
		if a.Requests > 0 {
			errorRatio = float64(a.FailedCount) / float64(a.Requests)
		}
		var tps float64
		if a.SumGenerationMs > 0 {
			tps = float64(a.SumStreamTokens) * 1000.0 / float64(a.SumGenerationMs)
		}
		var ttftAvg int64
		if a.StreamRequests > 0 {
			ttftAvg = a.SumFirstResponseMs / a.StreamRequests
		}
		out = append(out, ChannelMetric{
			ID:           a.ID,
			Name:         a.Name,
			Requests:     a.Requests,
			ErrorRatio:   errorRatio,
			TTFTAvgMs:    ttftAvg,
			TPSAvg:       tps,
			TTFTP95Ms:    ttftP95s[a.ID],
			TPSP5:        tpsP5s[a.ID],
			LatencyP95Ms: latencyP95s[a.ID],
			Spark24h:     sparks[a.ID],
		})
	}
	return out, nil
}

// ChannelModelBreakdownRow 是单渠道按 model_name 分组的计费细分行。
// billed = TotalCost(折后实付), raw = RawCost(折前原价);两者差额即渠道折扣/免费让利。
type ChannelModelBreakdownRow struct {
	ModelName        string `json:"model_name"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TotalCost        int64  `json:"total_cost"`
	RawCost          int64  `json:"raw_cost"`
}

// ChannelModelBreakdown 返回指定渠道在 [r.Start, r.End) 窗口内按 model_name 分组
// 的用量/计费细分,按 total_cost 降序排列。给 Billing 展开行 + 渠道详情卡片共用。
func (q *adminStatsQuery) ChannelModelBreakdown(channelID uint, r ObsRange) ([]ChannelModelBreakdownRow, error) {
	hourlyDB, err := q.logDB()
	if err != nil {
		return nil, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	aggregates, err := findExactUsageRows(hourlyDB, requestDB, r, "model", "", "channel_id = ?", channelID)
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	rows := make([]ChannelModelBreakdownRow, 0, len(aggregates))
	for _, aggregate := range aggregates {
		rows = append(rows, ChannelModelBreakdownRow{
			ModelName: aggregate.Identity, Requests: aggregate.Requests,
			PromptTokens: aggregate.PromptTokens, CompletionTokens: aggregate.CompletionTokens,
			CacheReadTokens: aggregate.CacheReadTokens, CacheWriteTokens: aggregate.CacheWriteTokens,
			TotalCost: aggregate.TotalCost, RawCost: aggregate.RawCost,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalCost != rows[j].TotalCost {
			return rows[i].TotalCost > rows[j].TotalCost
		}
		return rows[i].ModelName < rows[j].ModelName
	})
	return rows, nil
}

// AgentMetrics 返回 Monitoring 页面 agent 维度的指标行;
// 过滤 agent_id <> ” → 排除未归属 agent 的旧行。JOIN agents 表拿 Name/Status/LastSeen。
func (q *adminStatsQuery) AgentMetrics(r ObsRange) (out []AgentMetric, err error) {
	hourlyDB, err := q.logDB()
	if err != nil {
		return nil, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	aggs, err := findExactUsageRows(hourlyDB, requestDB, r, "agent", "", "agent_id <> ''")
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	sort.Slice(aggs, func(i, j int) bool {
		if aggs[i].Requests != aggs[j].Requests {
			return aggs[i].Requests > aggs[j].Requests
		}
		return aggs[i].Identity < aggs[j].Identity
	})

	agentIDs := make([]string, 0, len(aggs))
	for _, agg := range aggs {
		agentIDs = append(agentIDs, agg.Identity)
	}
	var agents []models.Agent
	if len(agentIDs) > 0 {
		err = q.ctx.GetCoreDB().Where("agent_id IN ?", agentIDs).Find(&agents).Error
	}
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*models.Agent, len(agents))
	for i := range agents {
		byID[agents[i].AgentID] = &agents[i]
	}

	sparks, err := agentSpark24h(hourlyDB, requestDB, r)
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}

	ttftP95s, err := ttftP95ByAgent(hourlyDB, r, requestDB)
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	tpsP5s, err := tpsP5ByAgent(hourlyDB, r, requestDB)
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	latencyP95s, err := latencyP95ByAgent(hourlyDB, r, requestDB)
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}

	out = make([]AgentMetric, 0, len(aggs))
	for _, a := range aggs {
		am := AgentMetric{
			ID:           a.Identity,
			Requests:     a.Requests,
			TTFTP95Ms:    ttftP95s[a.Identity],
			TPSP5:        tpsP5s[a.Identity],
			LatencyP95Ms: latencyP95s[a.Identity],
			Spark24h:     sparks[a.Identity],
		}
		if a.SumGenerationMs > 0 {
			am.TPSAvg = float64(a.SumStreamTokens) * 1000.0 / float64(a.SumGenerationMs)
		}
		if a.StreamRequests > 0 {
			am.TTFTAvgMs = a.SumFirstResponseMs / a.StreamRequests
		}
		if agent, ok := byID[a.Identity]; ok {
			am.Name = agent.Name
			am.LastSeen = agent.LastSeen
			am.Online = agent.Status == 1
		}
		out = append(out, am)
	}
	return out, nil
}

// channelSpark24h 返回 channel_id -> [24]int64 的请求数;
// 24 个槽位对应 r.End 之前的最后 24 小时,顺序为 [winStart, winStart+1h, ..., winStart+23h]。
// winStart = max(r.End - 24h, r.Start) (clamp 到 ObsRange 起点)。
// 没有数据的 entity 不会在结果 map 中出现 (调用方读到 nil slice);
// 有数据的 entity 槽位长度恒为 24,缺失小时填 0。
func channelSpark24h(hourlyDB, requestDB *gorm.DB, r ObsRange) (map[uint][]int64, error) {
	rows, err := findExactSparkSlots(hourlyDB, requestDB, r, "channel", "channel_id > 0")
	if err != nil {
		return nil, err
	}
	return exactUintSparkSlots(rows)
}

// agentSpark24h 与 channelSpark24h 同语义,但维度为 agent_id (string)。
func agentSpark24h(hourlyDB, requestDB *gorm.DB, r ObsRange) (map[string][]int64, error) {
	return findExactSparkSlots(hourlyDB, requestDB, r, "agent", "agent_id <> ''")
}

// ErrBucket 是 ErrorDistribution 输出的一行。
// by="stage" 时仅 Stage/Count/Ratio 有效; by="channel" 时仅 ID/Name/Count/Ratio 有效。
type ErrBucket struct {
	ID    uint    `json:"id,omitempty"`    // populated for by=channel
	Stage string  `json:"stage,omitempty"` // populated for by=stage
	Name  string  `json:"name,omitempty"`  // channel name when by=channel
	Count int64   `json:"count"`
	Ratio float64 `json:"ratio"`
}

// ErrorDistribution 聚合失败 (status=0) 请求按 stage 或 channel 维度的占比。
// scope 非 admin 时返回 nil,nil; by=channel 先聚合 log DB，再从 core DB 批量补渠道名称。
func (q *adminStatsQuery) ErrorDistribution(by string, r ObsRange, scope Scope) (out []ErrBucket, err error) {
	if !scope.IsAdmin {
		return nil, nil
	}
	db, err := q.requestLogDB()
	if err != nil {
		return nil, err
	}
	mode, err := q.ctx.DatabaseLayoutMode()
	if err != nil {
		return nil, err
	}
	if mode == app.DatabaseLayoutLegacySingle {
		db = db.Table("usage_logs")
	}
	switch by {
	case "stage":
		rows, err := errorDistributionByStage(db, r)
		return rows, WrapLogDatabaseError(err)
	case "channel":
		return q.errorDistributionByChannel(db, r)
	default:
		return nil, fmt.Errorf("error_distribution: unsupported by %q", by)
	}
}

func errorDistributionByStage(db *gorm.DB, r ObsRange) ([]ErrBucket, error) {
	type row struct {
		Stage string
		Count int64
	}
	var rows []row
	err := db.
		Where("status = 0 AND created_at >= ? AND created_at < ?", r.Start, r.End).
		Select("error_stage AS stage, COUNT(*) AS count").
		Group("error_stage").
		Order("count DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	var total int64
	for _, x := range rows {
		total += x.Count
	}
	out := make([]ErrBucket, 0, len(rows))
	for _, x := range rows {
		var ratio float64
		if total > 0 {
			ratio = float64(x.Count) / float64(total)
		}
		out = append(out, ErrBucket{Stage: x.Stage, Count: x.Count, Ratio: ratio})
	}
	return out, nil
}

func (q *adminStatsQuery) errorDistributionByChannel(db *gorm.DB, r ObsRange) ([]ErrBucket, error) {
	type row struct {
		ID    uint
		Count int64
	}
	var rows []row
	err := db.
		Where("status = 0 AND created_at >= ? AND created_at < ?", r.Start, r.End).
		Select("channel_id AS id, COUNT(*) AS count").
		Group("channel_id").
		Order("count DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	channelIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		if row.ID > 0 {
			channelIDs = append(channelIDs, row.ID)
		}
	}
	type channelName struct {
		ID   uint
		Name string
	}
	var channels []channelName
	if len(channelIDs) > 0 {
		if err := q.ctx.GetCoreDB().Model(&models.Channel{}).Where("id IN ?", channelIDs).Select("id, name").Scan(&channels).Error; err != nil {
			return nil, err
		}
	}
	names := make(map[uint]string, len(channels))
	for _, channel := range channels {
		names[channel.ID] = channel.Name
	}
	var total int64
	for _, x := range rows {
		total += x.Count
	}
	out := make([]ErrBucket, 0, len(rows))
	for _, x := range rows {
		var ratio float64
		if total > 0 {
			ratio = float64(x.Count) / float64(total)
		}
		out = append(out, ErrBucket{ID: x.ID, Name: names[x.ID], Count: x.Count, Ratio: ratio})
	}
	return out, nil
}

// StageLatency 是 StageLatencyP95 输出, 固定 5 个 stage, 顺序由 stageLatencyColumns 决定。
type StageLatency struct {
	Stages []StageP95 `json:"stages"`
}

// StageP95 是 StageLatency 的单条记录。
type StageP95 struct {
	Name  string `json:"name"`
	P95Ms int64  `json:"p95_ms"`
}

// stageLatencyColumns 固定输出顺序; Name 为前端展示用 key, Column 为 usage_logs 列名。
var stageLatencyColumns = []struct {
	Name   string
	Column string
}{
	{"inbound_decode", "inbound_decode_ms"},
	{"upstream_dispatch", "upstream_dispatch_ms"},
	{"upstream_decode", "upstream_decode_ms"},
	{"outbound_encode", "outbound_encode_ms"},
	{"client_encode", "client_encode_ms"},
}

// StageLatencyP95 对 5 个 stage_ms 列分别计算 p95 (SQLite 友好的近似算法:
// 按列升序排序后, 取 OFFSET = floor(cnt * 95 / 100), LIMIT 1)。
// status=1 (成功) 且 created_at IN [r.Start, r.End) 之外, 还应用 applyUsageLogFilter。
func (q *adminStatsQuery) StageLatencyP95(filter UsageLogListFilter, r ObsRange) (result StageLatency, err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	db, err := q.requestLogDB()
	if err != nil {
		return StageLatency{}, err
	}
	out := StageLatency{Stages: make([]StageP95, 0, len(stageLatencyColumns))}
	for _, sc := range stageLatencyColumns {
		v, err := stageP95(db, filter, r, sc.Column)
		if err != nil {
			return StageLatency{}, err
		}
		out.Stages = append(out.Stages, StageP95{Name: sc.Name, P95Ms: v})
	}
	return out, nil
}

// stageP95 单列 p95 helper; cnt=0 直接返回 0。
func stageP95(db *gorm.DB, filter UsageLogListFilter, r ObsRange, stageCol string) (int64, error) {
	baseFilter := func() *gorm.DB {
		q := applyUsageLogFilter(db.Model(&models.UsageLog{}), filter)
		return q.Where("status = 1 AND created_at >= ? AND created_at < ?", r.Start, r.End)
	}
	var cnt int64
	if err := baseFilter().Count(&cnt).Error; err != nil {
		return 0, err
	}
	if cnt == 0 {
		return 0, nil
	}
	offset := cnt * 95 / 100
	if offset >= cnt {
		offset = cnt - 1
	}
	if offset < 0 {
		offset = 0
	}
	var v int64
	err := baseFilter().
		Select(stageCol).
		Order(stageCol + " ASC").
		Offset(int(offset)).Limit(1).
		Scan(&v).Error
	return v, err
}

// KpiBundle 是 Dashboard KPI 卡片的统一返回结构。
// admin scope 填充 Users / SuccessRate; user scope 填充 Quota。
type KpiBundle struct {
	Requests    KpiMetric  `json:"requests"`
	Cost        KpiMetric  `json:"cost"`
	Tokens      KpiMetric  `json:"tokens"`
	Users       *KpiUsers  `json:"users,omitempty"`        // admin only
	SuccessRate *KpiMetric `json:"success_rate,omitempty"` // admin only
	Quota       *KpiQuota  `json:"quota,omitempty"`        // user only
}

// KpiMetric 是单个 KPI 卡片的统一格式: Value=当前周期总量, Spark=逐小时序列,
// Delta=(current - prev) / prev (前一同长度周期); prev=0 时 Delta=0。
// Spark 长度与 HourlyTrend 输出对齐 (range < 24h 时可能 < 24)。
type KpiMetric struct {
	Value int64   `json:"value"`
	Spark []int64 `json:"spark"`
	Delta float64 `json:"delta"`
}

// KpiUsers 仅 admin 返回; Value=总用户数, Active=range 内有 usage_log 的用户数, New=range 内注册用户数。
type KpiUsers struct {
	Value  int64 `json:"value"`
	Active int64 `json:"active"`
	New    int64 `json:"new"`
}

// KpiQuota 仅 user 返回; 直接读 users 表的 quota/used_quota。
type KpiQuota struct {
	Quota     int64 `json:"quota"`
	UsedQuota int64 `json:"used_quota"`
}

func dashboardKPIGranularity(r ObsRange) Gran {
	if r.End-r.Start > int64(MaxHourRangeDays)*86_400 {
		return GranDay
	}
	return GranHour
}

// DashboardKpis 组合 HourlyTrend + 周期对比 + admin/user 专属字段, 输出 Dashboard 顶部卡片所需的 KpiBundle。
// Spark 在 7 天内走 hour 粒度,长范围走 day 粒度;admin scope 额外输出 SuccessRate / Users,
// user scope 额外输出 Quota。previous 周期为紧邻 r.Start 之前等长度窗口,用于计算 Delta。
func (q *adminStatsQuery) DashboardKpis(r ObsRange, scope Scope, f ObsFilter) (KpiBundle, error) {
	sparkRange := r
	sparkRange.Gran = dashboardKPIGranularity(r)

	currentBuckets, err := q.HourlyTrend(sparkRange, scope, f)
	if err != nil {
		return KpiBundle{}, err
	}

	duration := r.End - r.Start
	prevR := ObsRange{Start: r.Start - duration, End: r.Start, Gran: sparkRange.Gran}
	prevBuckets, err := q.HourlyTrend(prevR, scope, f)
	if err != nil {
		return KpiBundle{}, err
	}

	bundle := KpiBundle{
		Requests: kpiMetric(currentBuckets, prevBuckets, func(b TimeBucket) int64 { return b.Requests }),
		Cost:     kpiMetric(currentBuckets, prevBuckets, func(b TimeBucket) int64 { return b.Cost }),
		Tokens:   kpiMetric(currentBuckets, prevBuckets, func(b TimeBucket) int64 { return b.Tokens }),
	}

	if scope.IsAdmin {
		logDB, requestLogModel, err := q.ctx.RequestLogModel()
		if err != nil {
			return KpiBundle{}, err
		}
		successRate, err := kpiSuccessRate(logDB, requestLogModel, r, sparkRange, scope, f)
		if err != nil {
			return KpiBundle{}, WrapLogDatabaseError(err)
		}
		bundle.SuccessRate = &successRate

		users, err := kpiUsers(q.ctx.GetCoreDB(), logDB, requestLogModel, r, f)
		if err != nil {
			return KpiBundle{}, err
		}
		bundle.Users = &users
		return bundle, nil
	}

	quota, err := kpiQuota(q.ctx.GetCoreDB(), scope.UserID)
	if err != nil {
		return KpiBundle{}, err
	}
	bundle.Quota = &quota
	return bundle, nil
}

// DashboardTrend returns cost, request and token series from log-owned request facts.
func (q *adminStatsQuery) DashboardTrend(r ObsRange, scope Scope, f ObsFilter) ([]TimeBucket, error) {
	return q.HourlyTrend(r, scope, f)
}

// DashboardSuccessRate returns the log-owned success KPI without loading the
// request and token metrics a second time.
func (q *adminStatsQuery) DashboardSuccessRate(r ObsRange, scope Scope, f ObsFilter) (KpiMetric, error) {
	sparkRange := r
	sparkRange.Gran = dashboardKPIGranularity(r)
	db, requestLogModel, err := q.ctx.RequestLogModel()
	if err != nil {
		return KpiMetric{}, err
	}
	metric, err := kpiSuccessRate(db, requestLogModel, r, sparkRange, scope, f)
	return metric, WrapLogDatabaseError(err)
}

// kpiMetric 用 value 选择器将 current/previous TimeBucket 切片折叠为 KpiMetric (Value/Spark/Delta)。
// prev 总量为 0 时 Delta=0,避免除零。
func kpiMetric(curr, prev []TimeBucket, value func(TimeBucket) int64) KpiMetric {
	spark := make([]int64, 0, len(curr))
	var sum int64
	for _, b := range curr {
		v := value(b)
		sum += v
		spark = append(spark, v)
	}
	var prevSum int64
	for _, b := range prev {
		prevSum += value(b)
	}
	var delta float64
	if prevSum > 0 {
		delta = float64(sum-prevSum) / float64(prevSum)
	}
	return KpiMetric{Value: sum, Spark: spark, Delta: delta}
}

// kpiSuccessRate 计算 admin scope 的成功请求 KPI;
// Value 语义: 成功请求总数 (success count, 非比率) —— KpiMetric.Value 是 int64,
// 选择计数而非 ratio 以避免精度损失,前端需要 ratio 时按 success/requests 算。
// Spark 在 7 天内为逐小时 success 计数,长范围按天聚合。Delta 暂固定为 0。
//
// 过滤策略:
//   - 有 EffectiveUserID 时走当前布局的 request log 表 (uhb 无 user_id), 按 sparkRange 粒度聚合 status=1。
//   - 否则走 usage_hourly_buckets (预聚合 success_count), 额外按 model_name 过滤
//     (与 HourlyTrend/Distribution/Leaderboard 一致: 重查询走预聚合表, 不碰原始 request log 表)。
//     SQL 仅按 date 粗筛 (避免按 hour 算 ts 后跨日 join 复杂度),
//     然后在 Go 里按 sparkRange.Start/End 二次过滤,保证边界外的桶不被计入 total。
type kpiSuccessRow struct {
	Date    string
	Hour    int
	Success int64
}

func kpiSuccessRate(db *gorm.DB, requestLogModel any, r, sparkRange ObsRange, scope Scope, f ObsFilter) (KpiMetric, error) {
	if uid := f.EffectiveUserID(scope); uid != 0 || f.ModelName != "" {
		return kpiSuccessRateFromRequestLog(db, requestLogModel, sparkRange, uid, f.ModelName)
	}
	window := splitExactBillingWindow(r.Start, r.End)
	var rows []kpiSuccessRow
	if window.hasFullBuckets() {
		var full []kpiSuccessRow
		if err := alignedHourWindow(db.Model(&models.UsageHourlyBucket{}), window.fullStart, window.fullEnd).
			Select("date, hour, COALESCE(SUM(success_count), 0) AS success").
			Group("date, hour").Scan(&full).Error; err != nil {
			return KpiMetric{}, err
		}
		rows = append(rows, full...)
	}
	for _, boundary := range window.boundaries {
		var partial []struct {
			Bucket  int64
			Success int64
		}
		if err := db.Session(&gorm.Session{}).Model(requestLogModel).
			Where("created_at >= ? AND created_at < ? AND status = 1", boundary.start, boundary.end).
			Select("(created_at - created_at % 3600) AS bucket, COUNT(*) AS success").
			Group("bucket").Scan(&partial).Error; err != nil {
			return KpiMetric{}, err
		}
		for _, row := range partial {
			date, hour := utcDateHour(row.Bucket)
			rows = append(rows, kpiSuccessRow{Date: date, Hour: hour, Success: row.Success})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date < rows[j].Date
		}
		return rows[i].Hour < rows[j].Hour
	})
	return kpiSuccessMetric(rows, sparkRange), nil
}

func kpiSuccessMetric(rows []kpiSuccessRow, sparkRange ObsRange) KpiMetric {
	var success int64
	spark := make([]int64, 0, len(rows))
	lastDate := ""
	for _, x := range rows {
		success += x.Success
		if sparkRange.Gran == GranDay && x.Date == lastDate {
			spark[len(spark)-1] += x.Success
			continue
		}
		spark = append(spark, x.Success)
		lastDate = x.Date
	}
	return KpiMetric{Value: success, Spark: spark, Delta: 0}
}

// kpiSuccessRateFromRequestLog 是单用户成功请求 KPI(uhb 无 user_id),按 sparkRange 粒度聚合 status=1。
func kpiSuccessRateFromRequestLog(db *gorm.DB, requestLogModel any, sparkRange ObsRange, userID uint, modelName string) (KpiMetric, error) {
	type row struct {
		Bucket  int64
		Success int64
	}
	query := db.Model(requestLogModel).
		Where("created_at >= ? AND created_at < ? AND status = 1", sparkRange.Start, sparkRange.End)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	bucketSec := int64(3600)
	if sparkRange.Gran == GranDay {
		bucketSec = 86400
	}
	var rows []row
	if err := query.
		Select("(created_at - (created_at % ?)) AS bucket, COUNT(*) AS success", bucketSec).
		Group("bucket").Order("bucket").
		Scan(&rows).Error; err != nil {
		return KpiMetric{}, err
	}
	var success int64
	spark := make([]int64, 0, len(rows))
	for _, x := range rows {
		success += x.Success
		spark = append(spark, x.Success)
	}
	return KpiMetric{Value: success, Spark: spark, Delta: 0}, nil
}

// kpiUsers 统计 admin scope 的用户 KPI:
// Value=总用户数 (全表 count), Active=range 内有 billing hourly fact 的 distinct user_id 数,
// New=range 内 created_at 落在窗口内的 users 数。
// f.ModelName 非空时 Active 仅统计用了该 model 的用户; total/new 始终全局不变。
func kpiUsers(coreDB, logDB *gorm.DB, requestLogModel any, r ObsRange, f ObsFilter) (KpiUsers, error) {
	var total int64
	if err := coreDB.Model(&models.User{}).Count(&total).Error; err != nil {
		return KpiUsers{}, err
	}
	var newCount int64
	if err := coreDB.Model(&models.User{}).
		Where("created_at >= ? AND created_at < ?", r.Start, r.End).
		Count(&newCount).Error; err != nil {
		return KpiUsers{}, err
	}
	query := logDB.Model(requestLogModel).Where("created_at >= ? AND created_at < ? AND user_id > 0", r.Start, r.End)
	if f.ModelName != "" {
		query = query.Where("model_name = ?", f.ModelName)
	}
	var active int64
	if err := query.Distinct("user_id").Count(&active).Error; err != nil {
		return KpiUsers{}, WrapLogDatabaseError(err)
	}
	return KpiUsers{Value: total, Active: active, New: newCount}, nil
}

// kpiQuota 读取 user scope 自身 quota / used_quota; 找不到用户时返回错误。
func kpiQuota(db *gorm.DB, userID uint) (KpiQuota, error) {
	var user models.User
	if err := db.First(&user, userID).Error; err != nil {
		return KpiQuota{}, err
	}
	return KpiQuota{Quota: user.Quota, UsedQuota: user.UsedQuota}, nil
}

// StackedBucket 是 CostTrendStackedByModel 输出的一行 (一个时间槽)。
// Series 的 key 是 model_name (或 "others" 折叠桶); Value 是该槽内该 series 的总成本。
type StackedBucket struct {
	Ts     int64            `json:"ts"`
	Label  string           `json:"label"`
	Series map[string]int64 `json:"series"`
}

// CostTrendStacked 是 CostTrendStackedByModel 输出。
// SeriesOrder 按总成本降序列出 top-N model_name, 多余的折叠在末尾的 "others"。
type CostTrendStacked struct {
	Buckets     []StackedBucket `json:"buckets"`
	SeriesOrder []string        `json:"series_order"`
}

// CacheSaving 是 CacheSaving DAO 输出。
// HitRatio = cache_read_tokens / (prompt_tokens + cache_read_tokens), 零安全。
// SavedTokens = sum(cache_read_tokens) (本来要付费的 prompt token, 命中缓存后没付)。
// SavedCost = saved_tokens * (sum(input_cost) / sum(prompt_tokens)); prompt_tokens=0 时回退为 0。
// VsLabel 当前固定 "vs no-cache", 给前端展示对照基线用。
// ReadTokens = sum(cache_read_tokens) 原始量;当前与 SavedTokens 等值,保留两字段以便后续语义分离(如引入折扣系数)。
// WriteTokens = sum(cache_write_tokens),反映本期请求触发的缓存写入量。
type CacheSaving struct {
	HitRatio    float64 `json:"hit_ratio"`
	SavedTokens int64   `json:"saved_tokens"`
	SavedCost   int64   `json:"saved_cost"`
	VsLabel     string  `json:"vs_label"`
	ReadTokens  int64   `json:"read_tokens"`
	WriteTokens int64   `json:"write_tokens"`
}

// stackRow 是堆叠聚合的统一中间行(date+hour+series名字+累计值)。
// Name 原叫 ModelName(仅 model 维度用过);现复用给 channel 维度(MarketShareTrend),
// 字段改通用名,SQL 侧靠 "AS name" 别名喂给 gorm Scan,行为不变。
type stackRow struct {
	Date string
	Hour int
	Name string
	Cost int64
}

// assembleCostStacked 把 (date,hour,name,value) 行按 top-N series 折叠成 CostTrendStacked。
// 与原 CostTrendStackedByModel 第二段逻辑等价,仅抽成可复用纯函数。
// 注:字段名叫 Cost,但 MarketShareTrend 拿它承载 token 量——纯数值堆叠,语义与字段名无关。
func assembleCostStacked(rows []stackRow, r ObsRange, topN int) CostTrendStacked {
	modelTotals := make(map[string]int64)
	for _, x := range rows {
		modelTotals[x.Name] += x.Cost
	}
	type mt struct {
		Name string
		Cost int64
	}
	mts := make([]mt, 0, len(modelTotals))
	for k, v := range modelTotals {
		mts = append(mts, mt{Name: k, Cost: v})
	}
	sort.Slice(mts, func(i, j int) bool {
		if mts[i].Cost != mts[j].Cost {
			return mts[i].Cost > mts[j].Cost
		}
		return mts[i].Name < mts[j].Name
	})
	selected := min(topN, len(mts))
	topSet := make(map[string]bool, selected)
	seriesOrder := make([]string, 0, selected+1)
	for _, item := range mts[:selected] {
		topSet[item.Name] = true
		seriesOrder = append(seriesOrder, item.Name)
	}
	hasOthers := len(mts) > selected

	type slot struct {
		Ts    int64
		Label string
	}
	bucketSec := int64(3600)
	if r.Gran == GranDay {
		bucketSec = 86400
	}
	slotIdx := make(map[slot]int)
	out := make([]StackedBucket, 0)
	for _, x := range rows {
		ts, label := bucketTsLabel(x.Date, x.Hour, r.Gran)
		if ts+bucketSec <= r.Start || ts >= r.End {
			continue
		}
		key := slot{Ts: ts, Label: label}
		idx, ok := slotIdx[key]
		if !ok {
			out = append(out, StackedBucket{Ts: ts, Label: label, Series: map[string]int64{}})
			idx = len(out) - 1
			slotIdx[key] = idx
		}
		seriesName := x.Name
		if !topSet[seriesName] {
			seriesName = "others"
		}
		out[idx].Series[seriesName] += x.Cost
	}
	if hasOthers {
		seriesOrder = append(seriesOrder, "others")
	}
	return CostTrendStacked{Buckets: out, SeriesOrder: seriesOrder}
}

// costStackRowsFromRequestFacts reads exact log-owned request facts.
func costStackRowsFromRequestFacts(db *gorm.DB, r ObsRange, userID, tokenID uint, modelName string) ([]stackRow, error) {
	bucketSeconds := int64(3600)
	if r.Gran == GranDay {
		bucketSeconds = 86400
	}
	var rawRows []struct {
		Bucket int64
		Name   string
		Cost   int64
	}
	query := filterBillingStats(db.Model(&models.UsageLog{}).Where("created_at >= ? AND created_at < ?", r.Start, r.End), userID, tokenID, modelName)
	err := query.Select(fmt.Sprintf("(created_at - created_at %% %d) AS bucket, model_name AS name, COALESCE(SUM(total_cost), 0) AS cost", bucketSeconds)).
		Group("bucket, model_name").Scan(&rawRows).Error
	if err != nil {
		return nil, err
	}
	rows := make([]stackRow, 0, len(rawRows))
	for _, row := range rawRows {
		date, hour := utcDateHour(row.Bucket)
		if r.Gran == GranDay {
			hour = 0
		}
		rows = append(rows, stackRow{Date: date, Hour: hour, Name: row.Name, Cost: row.Cost})
	}
	return sortedStackRowsFromSlice(rows), nil
}

func costStackRowsHybrid(hourlyDB, requestDB *gorm.DB, r ObsRange, userID, tokenID uint, modelName string) ([]stackRow, error) {
	if userID != 0 || tokenID != 0 || modelName != "" {
		return costStackRowsFromRequestFacts(requestDB, r, userID, tokenID, modelName)
	}
	window := splitExactBillingWindow(r.Start, r.End)
	rows := make([]stackRow, 0)
	if window.hasFullBuckets() {
		selectCols := "date, hour, model_name AS name, COALESCE(SUM(total_cost),0) AS cost"
		groupCols := "date, hour, model_name"
		if r.Gran == GranDay {
			selectCols, groupCols = "date, 0 AS hour, model_name AS name, COALESCE(SUM(total_cost),0) AS cost", "date, model_name"
		}
		var hourly []stackRow
		if err := alignedHourWindow(hourlyDB.Model(&models.UsageHourlyBucket{}), window.fullStart, window.fullEnd).
			Select(selectCols).Group(groupCols).Scan(&hourly).Error; err != nil {
			return nil, err
		}
		rows = append(rows, hourly...)
	}
	for _, boundary := range window.boundaries {
		partial, err := costStackRowsFromRequestFacts(requestDB, ObsRange{Start: boundary.start, End: boundary.end, Gran: r.Gran}, 0, 0, "")
		if err != nil {
			return nil, err
		}
		rows = append(rows, partial...)
	}
	return mergeCostStackRows(rows), nil
}

func mergeCostStackRows(rows []stackRow) []stackRow {
	type key struct {
		date string
		hour int
		name string
	}
	merged := make(map[key]int64)
	for _, row := range rows {
		merged[key{row.Date, row.Hour, row.Name}] += row.Cost
	}
	out := make([]stackRow, 0, len(merged))
	for item, cost := range merged {
		out = append(out, stackRow{Date: item.date, Hour: item.hour, Name: item.name, Cost: cost})
	}
	return sortedStackRowsFromSlice(out)
}

func filterBillingStats(query *gorm.DB, userID, tokenID uint, modelName string) *gorm.DB {
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if tokenID != 0 {
		query = query.Where("token_id = ?", tokenID)
	}
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	return query
}

// CostTrendStackedByModel 按 (time-bucket × model_name) 聚合 total_cost,
// 时间槽由 r.Gran 决定: hour → (date, hour) 桶; day → date 桶。
// 仅返回 series 总成本 top-N 的 model, 其余合并为 "others"。
//
// 数据源路由: global 无精确筛选时合并完整小时投影与边界 request facts；
// user/token/model 精确筛选全程读取 request facts。非 admin 无 uid 返回空。
func (q *adminStatsQuery) CostTrendStackedByModel(r ObsRange, scope Scope, topN int, f ObsFilter) (result CostTrendStacked, err error) {
	empty := CostTrendStacked{Buckets: []StackedBucket{}, SeriesOrder: []string{}}
	if topN <= 0 {
		topN = 5
	}
	if r.End <= r.Start {
		return empty, nil
	}
	uid := f.EffectiveUserID(scope)

	if uid == 0 && !scope.IsAdmin {
		return empty, nil
	}
	hourlyDB, err := q.logDB()
	if err != nil {
		return CostTrendStacked{}, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return CostTrendStacked{}, err
	}
	rows, err := costStackRowsHybrid(hourlyDB, requestDB, r, uid, f.TokenID, f.ModelName)
	if err != nil {
		return CostTrendStacked{}, err
	}
	if len(rows) == 0 {
		return empty, nil
	}
	return assembleCostStacked(rows, r, topN), nil
}

// ErrUnsupportedMarketShareDim 表示 MarketShareTrend 的 dim 参数非 model|channel。
// author 维本期不支持(无 author 数据源),handler 侧应把它转成 400。
var ErrUnsupportedMarketShareDim = fmt.Errorf("unsupported market share dim: only model|channel supported")

type billingDimensionRow struct {
	Bucket      int64
	Identity    string
	DisplayName string
	Value       int64
}

func requestDimensionStackRows(hourlyDB, requestDB *gorm.DB, dim, value string, r ObsRange, userID, tokenID uint, modelName string) ([]stackRow, error) {
	window := splitExactBillingWindow(r.Start, r.End)
	rows := make([]billingDimensionRow, 0)
	if userID == 0 && tokenID == 0 && modelName == "" && window.hasFullBuckets() {
		full, err := billingDimensionRowsFromHourly(hourlyDB, dim, value, r, window, userID, tokenID, modelName)
		if err != nil {
			return nil, err
		}
		rows = append(rows, full...)
	} else if window.hasFullBuckets() {
		window.boundaries = append(window.boundaries, billingBoundary{start: window.fullStart, end: window.fullEnd})
	}
	for _, boundary := range window.boundaries {
		partial, err := billingDimensionRowsFromLogs(requestDB, dim, value, r, boundary, userID, tokenID, modelName)
		if err != nil {
			return nil, err
		}
		rows = append(rows, partial...)
	}
	return mergeBillingDimensionRows(rows), nil
}

func billingDimensionSQL(dim string) (identity, display, group string, err error) {
	switch dim {
	case "model":
		return "model_name", "model_name", "model_name", nil
	case "channel":
		identity, display, group = channelDimensionSQL()
		return identity, display, group, nil
	default:
		return "", "", "", ErrUnsupportedMarketShareDim
	}
}

func channelDimensionSQL() (identity, display, group string) {
	private := "owner_type = 'private'"
	identity = fmt.Sprintf("CASE WHEN %s THEN 'private:' || CAST(private_channel_id AS TEXT) ELSE 'admin:' || CAST(channel_id AS TEXT) END", private)
	display = fmt.Sprintf("COALESCE(NULLIF(MAX(channel_name), ''), CASE WHEN %s THEN CAST(private_channel_id AS TEXT) ELSE CAST(channel_id AS TEXT) END)", private)
	group = "owner_type, channel_id, private_channel_id"
	return identity, display, group
}

func billingDimensionRowsFromHourly(db *gorm.DB, dim, value string, r ObsRange, window exactBillingWindow, userID, tokenID uint, modelName string) ([]billingDimensionRow, error) {
	identity, display, groupDim, err := billingDimensionSQL(dim)
	if err != nil {
		return nil, err
	}
	bucket := "CAST(strftime('%s', date || ' 00:00:00') AS INTEGER) + hour * 3600"
	group := "date, hour, " + groupDim
	if r.Gran == GranDay {
		bucket = "CAST(strftime('%s', date || ' 00:00:00') AS INTEGER)"
		group = "date, " + groupDim
	}
	valueSQL := "COALESCE(SUM(request_count),0)"
	if value == "tokens" {
		valueSQL = "COALESCE(SUM(prompt_tokens + completion_tokens + cache_read_tokens + cache_write_tokens),0)"
	}
	var rows []billingDimensionRow
	query := filterBillingStats(alignedHourWindow(db.Model(&models.UsageHourlyBucket{}), window.fullStart, window.fullEnd), userID, tokenID, modelName)
	err = query.Select(fmt.Sprintf("%s bucket, %s identity, %s display_name, %s value", bucket, identity, display, valueSQL)).Group(group).Scan(&rows).Error
	return rows, err
}

func billingDimensionRowsFromLogs(db *gorm.DB, dim, value string, r ObsRange, boundary billingBoundary, userID, tokenID uint, modelName string) ([]billingDimensionRow, error) {
	identity, display, groupDim, err := billingDimensionSQL(dim)
	if err != nil {
		return nil, err
	}
	bucketSec := int64(3600)
	if r.Gran == GranDay {
		bucketSec = 86400
	}
	valueSQL := "COUNT(*)"
	if value == "tokens" {
		valueSQL = "COALESCE(SUM(prompt_tokens + completion_tokens + cache_read_tokens + cache_write_tokens),0)"
	}
	var rows []billingDimensionRow
	query := filterBillingStats(db.Model(&models.UsageLog{}).Where("created_at >= ? AND created_at < ?", boundary.start, boundary.end), userID, tokenID, modelName)
	err = query.Select(fmt.Sprintf("(created_at - created_at %% %d) bucket, %s identity, %s display_name, %s value", bucketSec, identity, display, valueSQL)).Group("bucket, " + groupDim).Scan(&rows).Error
	return rows, err
}

func mergeBillingDimensionRows(rows []billingDimensionRow) []stackRow {
	labels := stableDimensionLabels(rows, func(row billingDimensionRow) (string, string) { return row.Identity, row.DisplayName })
	type key struct {
		bucket   int64
		identity string
	}
	merged := make(map[key]int64)
	for _, row := range rows {
		merged[key{row.Bucket, row.Identity}] += row.Value
	}
	out := make([]stackRow, 0, len(merged))
	for item, value := range merged {
		date, hour := utcDateHour(item.bucket)
		out = append(out, stackRow{Date: date, Hour: hour, Name: labels[item.identity], Cost: value})
	}
	return sortedStackRowsFromSlice(out)
}

func stableDimensionLabels[T any](rows []T, identityDisplay func(T) (string, string)) map[string]string {
	canonical := make(map[string]string)
	for _, row := range rows {
		identity, display := identityDisplay(row)
		if current, ok := canonical[identity]; !ok || display < current {
			canonical[identity] = display
		}
	}
	conflicts := make(map[string]int)
	for _, display := range canonical {
		conflicts[display]++
	}
	labels := make(map[string]string, len(canonical))
	for identity, display := range canonical {
		labels[identity] = display
		if conflicts[display] > 1 {
			suffix := identity
			if len(identity) > len("admin:") && identity[:len("admin:")] == "admin:" {
				suffix = identity[len("admin:"):]
			}
			labels[identity] = fmt.Sprintf("%s (#%s)", display, suffix)
		}
	}
	return labels
}

func sortedStackRowsFromSlice(rows []stackRow) []stackRow {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date < rows[j].Date
		}
		if rows[i].Hour != rows[j].Hour {
			return rows[i].Hour < rows[j].Hour
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// MarketShareTrend 按 dim(model|channel) 分组的时间序列，token 量包含四类 token。
// 供前端纵向堆叠柱状图(横轴时间、每根柱按 series 堆叠)。复用 assembleCostStacked 折叠
// top-N + "others",字段名叫 Cost 但这里承载的是 token 量——纯数值堆叠,语义与字段名无关。
// admin-only:非 admin scope 返回空(handler 侧已 403,这里是防御性兜底,与
// CostTrendStackedByModel 的非 admin 分支一致)。dim 非 model|channel 返回
// ErrUnsupportedMarketShareDim(handler 转 400)。
func (q *adminStatsQuery) MarketShareTrend(dim string, r ObsRange, scope Scope, topN int, f ObsFilter) (result CostTrendStacked, err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if dim != "model" && dim != "channel" {
		return CostTrendStacked{}, ErrUnsupportedMarketShareDim
	}
	empty := CostTrendStacked{Buckets: []StackedBucket{}, SeriesOrder: []string{}}
	if topN <= 0 {
		topN = 5
	}
	if r.End <= r.Start {
		return empty, nil
	}
	if !scope.IsAdmin {
		return empty, nil
	}
	hourlyDB, err := q.logDB()
	if err != nil {
		return CostTrendStacked{}, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return CostTrendStacked{}, err
	}
	rows, err := requestDimensionStackRows(hourlyDB, requestDB, dim, "tokens", r, f.EffectiveUserID(scope), 0, f.ModelName)
	if err != nil {
		return CostTrendStacked{}, err
	}
	if len(rows) == 0 {
		return empty, nil
	}
	return assembleCostStacked(rows, r, topN), nil
}

// ---- MetricTrendGrouped: per-model/channel time series for arbitrary metrics ----
//
// MarketShareTrend/CostTrendStackedByModel only ever fold a single summable
// int64 value (tokens/cost). Several metrics we want to chart per-model here
// are *ratios* (ttft = sum_first_response_ms/stream_request_count, tps =
// completion_tokens*1000/generation_ms, cache_hit_rate = cache_read/(prompt+
// cache_read)) — averaging or summing the pre-divided per-model values would
// be wrong (e.g. folding two models' ttft into "others" via naive average
// ignores how many requests backed each model's number). So each row here
// carries a numerator/denominator pair; a single value is computed once, as
// late as possible, via Num/Den.

// MetricStackedBucket is one time-slot of MetricTrendGrouped output.
// Series keys are top-N model/channel names (or the "others" fold); values
// are the metric's already-divided per-bucket number (float64, since ttft/tps/
// cache_hit_rate are not integers).
type MetricStackedBucket struct {
	Ts     int64              `json:"ts"`
	Label  string             `json:"label"`
	Series map[string]float64 `json:"series"`
}

// MetricTrendStacked is MetricTrendGrouped's output; SeriesOrder is ranked by
// total request volume (see assembleMetricStacked), independent of the metric
// being charted, so switching metrics on the same topN doesn't reshuffle which
// entities are "top" out from under the user.
type MetricTrendStacked struct {
	Buckets     []MetricStackedBucket `json:"buckets"`
	SeriesOrder []string              `json:"series_order"`
}

// metricStackRow is the per-(bucket,name) intermediate row for MetricTrendGrouped.
// Num/Den semantics depend on the metric (see metricTrendStackRows); Requests
// is the row's raw request count, used only to rank top-N (independent of
// whatever metric is being charted, so the ranked set of series is stable
// across metric switches for the same dim/range).
type metricStackRow struct {
	Date     string
	Hour     int
	Identity string
	Name     string
	Num      float64
	Den      float64
	Requests float64
}

// ErrUnsupportedTrendDim mirrors ErrUnsupportedMarketShareDim for MetricTrendGrouped.
var ErrUnsupportedTrendDim = fmt.Errorf("unsupported metric trend dim: only model|channel supported")

// ErrUnsupportedTrendMetric indicates MetricTrendGrouped's metric parameter
// isn't one of the known cost|requests|tokens|ttft|tps|cache_hit_rate metrics.
var ErrUnsupportedTrendMetric = fmt.Errorf("unsupported metric trend metric: only cost|requests|tokens|ttft|tps|cache_hit_rate supported")

var ErrUnsupportedTrendStat = fmt.Errorf("unsupported metric trend statistic")

// isValidTrendMetric reports whether metric is one MetricTrendGrouped understands.
func isValidTrendMetric(metric string) bool {
	switch metric {
	case "cost", "requests", "tokens", "ttft", "tps", "cache_hit_rate":
		return true
	default:
		return false
	}
}

// trendMetricWeighted reports whether metric is a ratio (avg-style) metric
// that must be folded via weighted-average (Σnum/Σden), as opposed to a plain
// summable metric (cost/requests/tokens) that folds via Σnum with Den pinned
// to 1 regardless of how many series get merged into "others".
func trendMetricWeighted(metric string) bool {
	switch metric {
	case "ttft", "tps", "cache_hit_rate":
		return true
	default:
		return false
	}
}

// assembleMetricStacked folds (date,hour,name,Num,Den,Requests) rows into
// top-N + "others", mirroring assembleCostStacked's time-bucket enumeration
// and top-N selection, but generalized to numerator/denominator folding.
//
// Ranking: top-N is chosen by total Requests per name (descending) — NOT by
// the metric's own magnitude — so the same topN/dim query ranks identically
// no matter which metric (cost/ttft/tps/...) is requested.
//
// Value: for weighted=true metrics, each series' value = ΣNum/ΣDen, where the
// sums are taken over every row folded into that series for that bucket; this
// is a correct weighted average (e.g. folding two models' ttft numerators/
// denominators together, then dividing once) rather than a naive average of
// pre-divided per-model values. For weighted=false metrics, Den is pinned to a
// constant 1 (never accumulated), so ΣNum/ΣDen degenerates to a plain sum
// regardless of how many names fold into "others" — this is what makes
// cost/requests/tokens "others" a real sum instead of an average across the
// folded models. Den==0 (weighted case, no data) safely yields 0, never a
// divide-by-zero panic.
func assembleMetricStacked(rows []metricStackRow, r ObsRange, topN int, weighted bool) MetricTrendStacked {
	nameRequests := make(map[string]float64)
	for _, x := range rows {
		nameRequests[x.Name] += x.Requests
	}
	type nr struct {
		Name     string
		Requests float64
	}
	nrs := make([]nr, 0, len(nameRequests))
	for k, v := range nameRequests {
		nrs = append(nrs, nr{Name: k, Requests: v})
	}
	sort.Slice(nrs, func(i, j int) bool {
		if nrs[i].Requests != nrs[j].Requests {
			return nrs[i].Requests > nrs[j].Requests
		}
		return nrs[i].Name < nrs[j].Name
	})
	selected := min(topN, len(nrs))
	topSet := make(map[string]bool, selected)
	seriesOrder := make([]string, 0, selected+1)
	for _, item := range nrs[:selected] {
		topSet[item.Name] = true
		seriesOrder = append(seriesOrder, item.Name)
	}
	hasOthers := len(nrs) > selected

	bucketSec := int64(3600)
	if r.Gran == GranDay {
		bucketSec = 86400
	}

	type acc struct {
		Num float64
		Den float64
	}
	type slot struct {
		Ts    int64
		Label string
	}
	slotIdx := make(map[slot]int)
	out := make([]MetricStackedBucket, 0)
	bucketAcc := make([]map[string]*acc, 0)

	for _, x := range rows {
		ts, label := bucketTsLabel(x.Date, x.Hour, r.Gran)
		if ts+bucketSec <= r.Start || ts >= r.End {
			continue
		}
		key := slot{Ts: ts, Label: label}
		idx, ok := slotIdx[key]
		if !ok {
			out = append(out, MetricStackedBucket{Ts: ts, Label: label, Series: map[string]float64{}})
			bucketAcc = append(bucketAcc, map[string]*acc{})
			idx = len(out) - 1
			slotIdx[key] = idx
		}
		seriesName := x.Name
		if !topSet[seriesName] {
			seriesName = "others"
		}
		a, ok := bucketAcc[idx][seriesName]
		if !ok {
			a = &acc{}
			bucketAcc[idx][seriesName] = a
		}
		a.Num += x.Num
		if weighted {
			a.Den += x.Den
		} else {
			a.Den = 1
		}
	}
	for i, accs := range bucketAcc {
		for name, a := range accs {
			var v float64
			if a.Den != 0 {
				v = a.Num / a.Den
			}
			out[i].Series[name] = v
		}
	}
	if hasOthers {
		seriesOrder = append(seriesOrder, "others")
	}
	return MetricTrendStacked{Buckets: out, SeriesOrder: seriesOrder}
}

// metricComponentsRow is the raw SQL scan row for metricTrendStackRows: every
// component MetricTrendGrouped might need across all 6 metrics, summed once
// per (bucket,name) group so we only query usage_hourly_buckets a single time
// regardless of which metric was requested.
type metricComponentsRow struct {
	Date                      string
	Hour                      int
	Bucket                    int64
	Identity                  string
	DisplayName               string
	Requests                  int64
	TotalCost                 int64
	PromptTokens              int64
	CompletionTokens          int64
	CacheReadTokens           int64
	CacheWriteTokens          int64
	SumFirstResponseMs        int64
	StreamRequestCount        int64
	SumStreamCompletionTokens int64
	SumGenerationMs           int64
}

// metricTrendStackRows combines aligned full-hour usage buckets with raw log
// rows at partial-hour boundaries, then derives each row's Num/Den per metric
// in Go. Channel series keep channel_id as identity and use channel_name only
// as the display label.
func metricTrendStackRows(db *gorm.DB, rawTable, metric, dim string, r ObsRange, userID uint, modelName string) ([]metricStackRow, error) {
	var identityExpr, displayExpr, groupDimCol string
	switch dim {
	case "channel":
		identityExpr, displayExpr, groupDimCol = channelDimensionSQL()
	case "model":
		identityExpr, displayExpr = "model_name", "model_name"
		groupDimCol = "model_name"
	default:
		return nil, ErrUnsupportedTrendDim
	}

	hourSelect := "hour"
	groupCols := fmt.Sprintf("date, hour, %s", groupDimCol)
	if r.Gran == GranDay {
		hourSelect = "0 AS hour"
		groupCols = fmt.Sprintf("date, %s", groupDimCol)
	}

	selectCols := fmt.Sprintf(`date, %s, %s AS identity, %s AS display_name,
		COALESCE(SUM(request_count), 0) AS requests,
		COALESCE(SUM(total_cost), 0) AS total_cost,
		COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
		COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
		COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
		COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
		COALESCE(SUM(sum_first_response_ms), 0) AS sum_first_response_ms,
		COALESCE(SUM(stream_request_count), 0) AS stream_request_count,
		COALESCE(SUM(sum_stream_completion_tokens), 0) AS sum_stream_completion_tokens,
		COALESCE(SUM(sum_generation_ms), 0) AS sum_generation_ms`, hourSelect, identityExpr, displayExpr)

	window := splitExactBillingWindow(r.Start, r.End)
	var comps []metricComponentsRow
	if userID == 0 && modelName == "" && window.hasFullBuckets() {
		query := alignedHourWindow(db.Model(&models.UsageHourlyBucket{}), window.fullStart, window.fullEnd)
		if modelName != "" {
			query = query.Where("model_name = ?", modelName)
		}
		var rows []metricComponentsRow
		if err := query.Select(selectCols).Group(groupCols).Scan(&rows).Error; err != nil {
			return nil, err
		}
		comps = append(comps, rows...)
	}
	boundaries := window.boundaries
	if userID != 0 || modelName != "" {
		boundaries = []billingBoundary{{start: r.Start, end: r.End}}
	}
	for _, boundary := range boundaries {
		raw := db.Table(rawTable)
		if userID != 0 {
			raw = raw.Where("user_id = ?", userID)
		}
		if modelName != "" {
			raw = raw.Where("model_name = ?", modelName)
		}
		bucketSec := int64(3600)
		if r.Gran == GranDay {
			bucketSec = 86400
		}
		rawSelect := fmt.Sprintf(`(created_at - created_at %% %d) AS bucket, %s AS identity, %s AS display_name,
			COUNT(*) requests, COALESCE(SUM(total_cost),0) total_cost,
			COALESCE(SUM(prompt_tokens),0) prompt_tokens, COALESCE(SUM(completion_tokens),0) completion_tokens,
			COALESCE(SUM(cache_read_tokens),0) cache_read_tokens, COALESCE(SUM(cache_write_tokens),0) cache_write_tokens,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND first_response_ms>0 THEN first_response_ms ELSE 0 END),0) sum_first_response_ms,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND first_response_ms>0 THEN 1 ELSE 0 END),0) stream_request_count,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 AND duration-first_response_ms>0 THEN completion_tokens ELSE 0 END),0) sum_stream_completion_tokens,
			COALESCE(SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 AND duration-first_response_ms>0 THEN duration-first_response_ms ELSE 0 END),0) sum_generation_ms`, bucketSec, identityExpr, displayExpr)
		var rows []metricComponentsRow
		if err := raw.Where("created_at >= ? AND created_at < ?", boundary.start, boundary.end).Select(rawSelect).Group("bucket, " + groupDimCol).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			date, hour := utcDateHour(rows[i].Bucket)
			rows[i].Date, rows[i].Hour = date, hour
		}
		comps = append(comps, rows...)
	}
	labels := stableDimensionLabels(comps, func(row metricComponentsRow) (string, string) { return row.Identity, row.DisplayName })

	rows := make([]metricStackRow, 0, len(comps))
	for _, c := range comps {
		name := labels[c.Identity]
		row := metricStackRow{Date: c.Date, Hour: c.Hour, Identity: c.Identity, Name: name, Requests: float64(c.Requests)}
		switch metric {
		case "cost":
			row.Num, row.Den = float64(c.TotalCost), 1
		case "requests":
			row.Num, row.Den = float64(c.Requests), 1
		case "tokens":
			row.Num, row.Den = float64(c.PromptTokens+c.CompletionTokens+c.CacheReadTokens+c.CacheWriteTokens), 1
		case "ttft":
			row.Num, row.Den = float64(c.SumFirstResponseMs), float64(c.StreamRequestCount)
		case "tps":
			row.Num, row.Den = float64(c.SumStreamCompletionTokens)*1000, float64(c.SumGenerationMs)
		case "cache_hit_rate":
			row.Num = float64(c.CacheReadTokens) * 100
			row.Den = float64(c.PromptTokens + c.CacheReadTokens)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// MetricTrendGrouped returns a per-(model|channel) time series for one of
// cost/requests/tokens/ttft/tps/cache_hit_rate, folded to top-N + "others"
// (see assembleMetricStacked). admin-only (mirrors MarketShareTrend): non-admin
// scope returns an empty result rather than an error, since the handler already
// 403s before reaching here — this is defense-in-depth, not the primary gate.
func (q *adminStatsQuery) MetricTrendGrouped(metric, stat, dim string, r ObsRange, scope Scope, topN int, f ObsFilter) (result MetricTrendStacked, err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if stat == "" {
		stat = canonicalTrendMetricStat(metric)
	}
	if dim != "model" && dim != "channel" {
		return MetricTrendStacked{}, ErrUnsupportedTrendDim
	}
	if !isValidTrendMetric(metric) {
		return MetricTrendStacked{}, ErrUnsupportedTrendMetric
	}
	if !validTrendMetricStat(metric, stat) {
		return MetricTrendStacked{}, ErrUnsupportedTrendStat
	}
	empty := MetricTrendStacked{Buckets: []MetricStackedBucket{}, SeriesOrder: []string{}}
	if topN <= 0 {
		topN = 5
	}
	if r.End <= r.Start {
		return empty, nil
	}
	db, err := q.logDB()
	if err != nil {
		return MetricTrendStacked{}, err
	}
	rawTable := "usage_logs"
	mode, modeErr := q.ctx.DatabaseLayoutMode()
	if modeErr != nil {
		return MetricTrendStacked{}, modeErr
	}
	if mode == app.DatabaseLayoutSplit {
		rawTable = models.RequestLog{}.TableName()
	}
	userID := f.EffectiveUserID(scope)
	rows, err := metricTrendStackRows(db, rawTable, metric, dim, r, userID, f.ModelName)
	if err != nil {
		return MetricTrendStacked{}, err
	}
	if len(rows) == 0 {
		return empty, nil
	}
	if stat == "p95" || stat == "p5" {
		histRows, histErr := metricTrendHistogramRows(db, rawTable, metric, dim, r, userID, f.ModelName)
		if histErr != nil {
			return MetricTrendStacked{}, histErr
		}
		return assembleMetricPercentileStacked(rows, histRows, r, topN, metric), nil
	}
	return assembleMetricStacked(rows, r, topN, trendMetricWeighted(metric)), nil
}

func validTrendMetricStat(metric, stat string) bool {
	if stat == "" {
		stat = canonicalTrendMetricStat(metric)
	}
	switch metric {
	case "ttft":
		return stat == "avg" || stat == "p95"
	case "tps":
		return stat == "avg" || stat == "p5"
	case "cost", "requests", "tokens":
		return stat == "sum"
	case "cache_hit_rate":
		return stat == "ratio"
	default:
		return false
	}
}

func canonicalTrendMetricStat(metric string) string {
	switch metric {
	case "ttft", "tps":
		return "avg"
	case "cache_hit_rate":
		return "ratio"
	default:
		return "sum"
	}
}

type metricHistogramRow struct {
	Date     string
	Hour     int
	Identity string
	Counts   [17]int64
	Max      int64
}

type metricHistogramScanRow struct {
	Date     string
	Hour     int
	Identity string
	Max      int64 `gorm:"column:max_value"`
	H0       int64 `gorm:"column:s0"`
	H1       int64 `gorm:"column:s1"`
	H2       int64 `gorm:"column:s2"`
	H3       int64 `gorm:"column:s3"`
	H4       int64 `gorm:"column:s4"`
	H5       int64 `gorm:"column:s5"`
	H6       int64 `gorm:"column:s6"`
	H7       int64 `gorm:"column:s7"`
	H8       int64 `gorm:"column:s8"`
	H9       int64 `gorm:"column:s9"`
	H10      int64 `gorm:"column:s10"`
	H11      int64 `gorm:"column:s11"`
	H12      int64 `gorm:"column:s12"`
	H13      int64 `gorm:"column:s13"`
	H14      int64 `gorm:"column:s14"`
	H15      int64 `gorm:"column:s15"`
	H16      int64 `gorm:"column:s16"`
}

func (row metricHistogramScanRow) histogramRow() metricHistogramRow {
	return metricHistogramRow{Date: row.Date, Hour: row.Hour, Identity: row.Identity, Max: row.Max,
		Counts: [17]int64{row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8, row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16}}
}

func metricTrendHistogramRows(db *gorm.DB, rawTable, metric, dim string, r ObsRange, userID uint, modelName string) ([]metricHistogramRow, error) {
	if userID != 0 || modelName != "" {
		return metricHistogramBoundaryRows(db, rawTable, metric, dim, r, billingBoundary{start: r.Start, end: r.End}, userID, modelName)
	}
	window := splitExactBillingWindow(r.Start, r.End)
	rows := make([]metricHistogramRow, 0)
	if window.hasFullBuckets() {
		full, err := metricHistogramFullHourRows(db, metric, dim, r, window, userID, modelName)
		if err != nil {
			return nil, err
		}
		rows = append(rows, full...)
	}
	for _, boundary := range window.boundaries {
		partial, err := metricHistogramBoundaryRows(db, rawTable, metric, dim, r, boundary, userID, modelName)
		if err != nil {
			return nil, err
		}
		rows = append(rows, partial...)
	}
	return rows, nil
}

func metricHistogramFullHourRows(db *gorm.DB, metric, dim string, r ObsRange, window exactBillingWindow, userID uint, modelName string) ([]metricHistogramRow, error) {
	if userID != 0 && dim != "model" {
		return nil, ErrUnsupportedTrendDim
	}
	var table any
	maxColumn := "max_first_response_ms"
	if userID != 0 {
		if metric == "ttft" {
			table = &models.UsageUserTTFTHistogram{}
		} else {
			table, maxColumn = &models.UsageUserTPSHistogram{}, "max_tps"
		}
	} else if metric == "ttft" {
		table = &models.UsageTTFTHistogram{}
	} else {
		table, maxColumn = &models.UsageTPSHistogram{}, "max_tps"
	}
	identity := "model_name"
	groupDim := "model_name"
	if dim == "channel" {
		identity = "CASE WHEN private_channel_id > 0 THEN 'private:' || CAST(private_channel_id AS TEXT) ELSE 'admin:' || CAST(channel_id AS TEXT) END"
		groupDim = "private_channel_id, channel_id"
	}
	hourSelect, groupCols := "hour", "date, hour, "+groupDim
	if r.Gran == GranDay {
		hourSelect, groupCols = "0 AS hour", "date, "+groupDim
	}
	query := alignedHourWindow(db.Model(table), window.fullStart, window.fullEnd)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	var scanned []metricHistogramScanRow
	selectCols := fmt.Sprintf("date, %s, %s AS identity, COALESCE(MAX(%s),0) AS max_value, %s", hourSelect, identity, maxColumn, histSumSelectFrag)
	if err := query.Select(selectCols).Group(groupCols).Scan(&scanned).Error; err != nil {
		return nil, err
	}
	rows := make([]metricHistogramRow, 0, len(scanned))
	for _, row := range scanned {
		rows = append(rows, row.histogramRow())
	}
	return rows, nil
}

func metricHistogramBoundaryRows(db *gorm.DB, rawTable, metric, dim string, r ObsRange, boundary billingBoundary, userID uint, modelName string) ([]metricHistogramRow, error) {
	query := db.Table(rawTable).Where("created_at >= ? AND created_at < ?", boundary.start, boundary.end)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	query = query.Where("status = ? AND is_stream = ?", 1, true)
	switch metric {
	case "ttft":
		query = query.Where("first_response_ms > 0")
	case "tps":
		query = query.Where("completion_tokens > 0 AND duration-first_response_ms > 0")
	}
	dbRows, err := query.Select("created_at, user_id, owner_type, channel_id, private_channel_id, model_name, status, is_stream, first_response_ms, duration, completion_tokens").Rows()
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()
	type key struct {
		date, identity string
		hour           int
	}
	merged := make(map[key]*metricHistogramRow)
	for dbRows.Next() {
		var log models.UsageLog
		if err := query.ScanRows(dbRows, &log); err != nil {
			return nil, err
		}
		var value int64
		if metric == "ttft" {
			if log.Status != 1 || !log.IsStream || log.FirstResponseMs <= 0 {
				continue
			}
			value = int64(log.FirstResponseMs)
		} else {
			generation := log.Duration - log.FirstResponseMs
			if log.Status != 1 || !log.IsStream || log.CompletionTokens <= 0 || generation <= 0 {
				continue
			}
			value = tpshist.TokensPerSecond(int64(log.CompletionTokens), int64(generation))
		}
		bucket := log.CreatedAt - log.CreatedAt%3600
		if r.Gran == GranDay {
			bucket = log.CreatedAt - log.CreatedAt%86400
		}
		date, hour := utcDateHour(bucket)
		identity := log.ModelName
		if dim == "channel" {
			identity = fmt.Sprintf("admin:%d", log.ChannelID)
			if log.PrivateChannelID > 0 || log.OwnerType == "private" {
				identity = fmt.Sprintf("private:%d", log.PrivateChannelID)
			}
		}
		item := key{date: date, hour: hour, identity: identity}
		row := merged[item]
		if row == nil {
			row = &metricHistogramRow{Date: date, Hour: hour, Identity: identity}
			merged[item] = row
		}
		if value > row.Max {
			row.Max = value
		}
		if metric == "ttft" {
			row.Counts[ttfthist.SlotIndex(value)]++
		} else {
			row.Counts[tpshist.SlotIndex(value)]++
		}
	}
	if err := dbRows.Err(); err != nil {
		return nil, err
	}
	rows := make([]metricHistogramRow, 0, len(merged))
	for _, row := range merged {
		rows = append(rows, *row)
	}
	return rows, nil
}

func assembleMetricPercentileStacked(rankRows []metricStackRow, histRows []metricHistogramRow, r ObsRange, topN int, metric string) MetricTrendStacked {
	requests := make(map[string]float64)
	labels := make(map[string]string)
	for _, row := range rankRows {
		requests[row.Identity] += row.Requests
		labels[row.Identity] = row.Name
	}
	known := make(map[string]bool, len(requests))
	for identity := range requests {
		known[identity] = true
	}
	type ranked struct {
		identity, name string
		requests       float64
	}
	ranking := make([]ranked, 0, len(requests))
	for identity, count := range requests {
		ranking = append(ranking, ranked{identity: identity, name: labels[identity], requests: count})
	}
	sort.Slice(ranking, func(i, j int) bool {
		if ranking[i].requests != ranking[j].requests {
			return ranking[i].requests > ranking[j].requests
		}
		return ranking[i].name < ranking[j].name
	})
	selected := min(topN, len(ranking))
	top := make(map[string]string, selected)
	order := make([]string, 0, selected+1)
	for _, item := range ranking[:selected] {
		top[item.identity] = item.name
		order = append(order, item.name)
	}
	if len(ranking) > selected {
		order = append(order, "others")
	}
	type bucketKey struct {
		ts    int64
		label string
	}
	type histogramAcc struct {
		rows [][]int64
		max  int64
	}
	buckets := make(map[bucketKey]map[string]*histogramAcc)
	for _, row := range rankRows {
		ts, label := bucketTsLabel(row.Date, row.Hour, r.Gran)
		key := bucketKey{ts: ts, label: label}
		if buckets[key] == nil {
			buckets[key] = make(map[string]*histogramAcc)
		}
		name, ok := top[row.Identity]
		if !ok {
			name = "others"
		}
		if buckets[key][name] == nil {
			buckets[key][name] = &histogramAcc{}
		}
	}
	for _, row := range histRows {
		if !known[row.Identity] {
			continue
		}
		ts, label := bucketTsLabel(row.Date, row.Hour, r.Gran)
		key := bucketKey{ts: ts, label: label}
		if buckets[key] == nil {
			buckets[key] = make(map[string]*histogramAcc)
		}
		name, ok := top[row.Identity]
		if !ok {
			name = "others"
		}
		acc := buckets[key][name]
		if acc == nil {
			acc = &histogramAcc{}
			buckets[key][name] = acc
		}
		counts := make([]int64, len(row.Counts))
		copy(counts, row.Counts[:])
		acc.rows = append(acc.rows, counts)
		if row.Max > acc.max {
			acc.max = row.Max
		}
	}
	keys := make([]bucketKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ts < keys[j].ts })
	out := make([]MetricStackedBucket, 0, len(keys))
	for _, key := range keys {
		bucket := MetricStackedBucket{Ts: key.ts, Label: key.label, Series: map[string]float64{}}
		for name, acc := range buckets[key] {
			merged := histutil.MergeCounts(acc.rows)
			var counts [17]int64
			copy(counts[:], merged)
			if metric == "ttft" {
				bucket.Series[name] = float64(ttfthist.EstimatePercentile(counts, 0.95, acc.max))
			} else {
				bucket.Series[name] = float64(tpshist.EstimateP5(counts, acc.max))
			}
		}
		out = append(out, bucket)
	}
	return MetricTrendStacked{Buckets: out, SeriesOrder: order}
}

// CacheSaving 计算窗口内的缓存命中收益。
//
// 数据源为 log-owned request facts；有效 user_id (admin 锁定某用户或 user scope)
// 作为过滤条件。
// CacheSaving 不跟随 f.ModelName (cache 卡片设计为模型无关)。
// 公式:
//
//	hit_ratio    = sum(cache_read_tokens) / sum(prompt_tokens + cache_read_tokens)
//	saved_tokens = sum(cache_read_tokens)
//	saved_cost   = saved_tokens * (sum(input_cost) / sum(prompt_tokens))
//
// 分母为 0 时各项分别回退 0,避免除零。
func (q *adminStatsQuery) CacheSaving(r ObsRange, scope Scope, f ObsFilter) (result CacheSaving, err error) {
	if r.End <= r.Start {
		return CacheSaving{VsLabel: "vs no-cache"}, nil
	}
	hourlyDB, err := q.logDB()
	if err != nil {
		return CacheSaving{}, err
	}
	requestDB, err := q.requestLogDB()
	if err != nil {
		return CacheSaving{}, err
	}
	result, err = cacheSavingHybrid(hourlyDB, requestDB, r, f.EffectiveUserID(scope), f.TokenID, f.ModelName)
	return result, WrapLogDatabaseError(err)
}

type cacheSavingAgg struct {
	Prompt     int64
	CacheRead  int64
	CacheWrite int64
	InputCost  int64
}

func cacheSavingFromRequestFacts(db *gorm.DB, r ObsRange, userID, tokenID uint, modelName string) (cacheSavingAgg, error) {
	var total cacheSavingAgg
	query := db.Model(&models.UsageLog{}).Where("created_at >= ? AND created_at < ?", r.Start, r.End)
	query = filterBillingStats(query, userID, tokenID, modelName)
	if err := query.Select(`COALESCE(SUM(prompt_tokens), 0) AS prompt,
			COALESCE(SUM(cache_read_tokens), 0) AS cache_read,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write,
			COALESCE(SUM(input_cost), 0) AS input_cost`).
		Scan(&total).Error; err != nil {
		return cacheSavingAgg{}, err
	}
	return total, nil
}

func cacheSavingHybrid(hourlyDB, requestDB *gorm.DB, r ObsRange, userID, tokenID uint, modelName string) (CacheSaving, error) {
	var total cacheSavingAgg
	add := func(row cacheSavingAgg) {
		total.Prompt += row.Prompt
		total.CacheRead += row.CacheRead
		total.CacheWrite += row.CacheWrite
		total.InputCost += row.InputCost
	}
	if userID != 0 || tokenID != 0 || modelName != "" {
		row, err := cacheSavingFromRequestFacts(requestDB, r, userID, tokenID, modelName)
		if err != nil {
			return CacheSaving{}, err
		}
		add(row)
	} else {
		window := splitExactBillingWindow(r.Start, r.End)
		if window.hasFullBuckets() {
			var row cacheSavingAgg
			if err := alignedHourWindow(hourlyDB.Model(&models.UsageHourlyBucket{}), window.fullStart, window.fullEnd).
				Select(`COALESCE(SUM(prompt_tokens),0) prompt, COALESCE(SUM(cache_read_tokens),0) cache_read,
					COALESCE(SUM(cache_write_tokens),0) cache_write, COALESCE(SUM(input_cost),0) input_cost`).Scan(&row).Error; err != nil {
				return CacheSaving{}, err
			}
			add(row)
		}
		for _, boundary := range window.boundaries {
			row, err := cacheSavingFromRequestFacts(requestDB, ObsRange{Start: boundary.start, End: boundary.end, Gran: r.Gran}, 0, 0, "")
			if err != nil {
				return CacheSaving{}, err
			}
			add(row)
		}
	}
	out := CacheSaving{
		SavedTokens: total.CacheRead,
		ReadTokens:  total.CacheRead,
		WriteTokens: total.CacheWrite,
		VsLabel:     "vs no-cache",
	}
	denom := total.Prompt + total.CacheRead
	if denom > 0 {
		out.HitRatio = float64(total.CacheRead) / float64(denom)
	}
	if total.Prompt > 0 {
		out.SavedCost = int64(float64(total.CacheRead) * float64(total.InputCost) / float64(total.Prompt))
	}
	return out, nil
}

// LogsTotals 是 LogsTotals DAO 输出, 给 /v1/logs/insights 用。
// Spark* 长度恒为 24, 槽位对应 r.End 前的最后 24 小时;
// SparkP95 用 MAX(duration) 作 p95 的近似 (per-bucket 真实 p95 要 24 个独立查询, 用 MAX 折中)。
type LogsTotals struct {
	Total       int64   `json:"total"`
	Failed      int64   `json:"failed"`
	P95Ms       int64   `json:"p95_ms"`
	SlowestMs   int64   `json:"slowest_ms"`
	SparkTotal  []int64 `json:"spark_total"`
	SparkFailed []int64 `json:"spark_failed"`
	SparkP95    []int64 `json:"spark_p95"`
}

// LogsTotals:admin 走 rollup(usage_hourly_bucket totals/sparks + usage_duration_histograms
// slowest/p95),窗口内 rollup 无行则回退原表(未回填保护,spec §12);user scope 恒走原表。
func (q *adminStatsQuery) LogsTotals(r ObsRange, scope Scope) (output LogsTotals, err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if r.End <= r.Start {
		return LogsTotals{
			SparkTotal:  make([]int64, 24),
			SparkFailed: make([]int64, 24),
			SparkP95:    make([]int64, 24),
		}, nil
	}
	if scope.IsAdmin {
		res, ok, err := q.logsTotalsFromRollups(r)
		if err == nil && ok {
			return res, nil
		}
		if errors.Is(err, ErrLogDatabaseUnavailable) || errors.Is(err, ErrInvalidDatabaseLayout) || errors.Is(err, ErrCrossDatabaseTransaction) {
			return LogsTotals{}, err
		}
		// err != nil 或 rollup 空窗:静默回退原表(回填期间的正确性优先)
	}
	result, err := q.logsTotalsFromRaw(r, scope)
	return result, WrapLogDatabaseError(err)
}

// logsTotalsFromRaw 聚合 usage_logs 在 r 窗口内的请求总数 / 失败数 / duration p95 / 最慢请求 / 24-slot spark。
// 非 admin scope 自动注入 user_id 过滤。
// p95 计算用 SQLite 友好的 OFFSET 近似 (跟 stageP95 / PercentileTTFT 同思路)。
// 原 LogsTotals 函数体原样搬移,行为不变(既有测试 + user scope 依赖此路径)。
func (q *adminStatsQuery) logsTotalsFromRaw(r ObsRange, scope Scope) (LogsTotals, error) {
	db, err := q.requestLogDB()
	if err != nil {
		return LogsTotals{}, err
	}

	base := func() *gorm.DB {
		q := db.Model(&models.UsageLog{}).
			Where("created_at >= ? AND created_at < ?", r.Start, r.End)
		if !scope.IsAdmin {
			q = q.Where("user_id = ?", scope.UserID)
		}
		return q
	}

	type logsTotalsAgg struct {
		Total   int64 `gorm:"column:total"`
		Failed  int64 `gorm:"column:failed"`
		Success int64 `gorm:"column:success"`
		Slowest int64 `gorm:"column:slowest"`
	}
	var agg logsTotalsAgg
	if err := base().
		Select(`COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status = 0 THEN 1 ELSE 0 END), 0) AS failed,
			COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END), 0) AS success,
			COALESCE(MAX(CASE WHEN status = 1 THEN duration ELSE NULL END), 0) AS slowest`).
		Scan(&agg).Error; err != nil {
		return LogsTotals{}, err
	}

	// p95 over status=1 rows (success) using OFFSET approximation.
	var p95 int64
	if agg.Success > 0 {
		offset := agg.Success * 95 / 100
		if offset >= agg.Success {
			offset = agg.Success - 1
		}
		if err := base().Where("status = 1").
			Select("duration").
			Order("duration ASC").
			Offset(int(offset)).Limit(1).
			Scan(&p95).Error; err != nil {
			return LogsTotals{}, err
		}
	}

	// 24-slot sparks.
	winStart := r.End - 24*3600
	if winStart < r.Start {
		winStart = r.Start
	}
	sparkTotal, err := logsHourlySpark(base(), winStart, r.End, "")
	if err != nil {
		return LogsTotals{}, err
	}
	sparkFailed, err := logsHourlySpark(base(), winStart, r.End, "status = 0")
	if err != nil {
		return LogsTotals{}, err
	}
	sparkP95, err := logsHourlySparkMax(base(), winStart, r.End)
	if err != nil {
		return LogsTotals{}, err
	}
	return LogsTotals{
		Total: agg.Total, Failed: agg.Failed, P95Ms: p95, SlowestMs: agg.Slowest,
		SparkTotal: sparkTotal, SparkFailed: sparkFailed, SparkP95: sparkP95,
	}, nil
}

// utcDateHour 把 unix 秒转成 rollup 维度 (date, hour)。
func utcDateHour(ts int64) (string, int) {
	t := time.Unix(ts, 0).UTC()
	return t.Format("2006-01-02"), t.Hour()
}

// hourWindow selects every rollup bucket intersecting [start,end). It is not
// exact for partial boundary hours; exact queries split those ranges to
// layout-aware request facts with splitExactBillingWindow.
func hourWindow(db *gorm.DB, start, end int64) *gorm.DB {
	sd, sh := utcDateHour(start)
	ed, eh := utcDateHour(end - 1)
	return db.Where(
		"(date > ? OR (date = ? AND hour >= ?)) AND (date < ? OR (date = ? AND hour <= ?))",
		sd, sd, sh, ed, ed, eh,
	)
}

type billingBoundary struct{ start, end int64 }

type exactBillingWindow struct {
	fullStart, fullEnd int64
	boundaries         []billingBoundary
}

func (w exactBillingWindow) hasFullBuckets() bool { return w.fullStart < w.fullEnd }

func splitExactBillingWindow(start, end int64) exactBillingWindow {
	return splitExactRollupWindow(start, end, 3600)
}

func splitExactDailyWindow(start, end int64) exactBillingWindow {
	return splitExactRollupWindow(start, end, 86400)
}

func splitExactRollupWindow(start, end, bucketSeconds int64) exactBillingWindow {
	if end <= start {
		return exactBillingWindow{}
	}
	startFloor := start - start%bucketSeconds
	fullStart := startFloor
	if start != startFloor {
		fullStart += bucketSeconds
	}
	fullEnd := end - end%bucketSeconds
	if fullStart >= fullEnd {
		return exactBillingWindow{boundaries: []billingBoundary{{start: start, end: end}}}
	}
	window := exactBillingWindow{fullStart: fullStart, fullEnd: fullEnd}
	if start < fullStart {
		window.boundaries = append(window.boundaries, billingBoundary{start: start, end: fullStart})
	}
	if fullEnd < end {
		window.boundaries = append(window.boundaries, billingBoundary{start: fullEnd, end: end})
	}
	return window
}

func alignedHourWindow(db *gorm.DB, start, end int64) *gorm.DB {
	startDate, startHour := utcDateHour(start)
	endDate, endHour := utcDateHour(end)
	return db.Where("(date, hour) >= (?, ?) AND (date, hour) < (?, ?)", startDate, startHour, endDate, endHour)
}

// logsTotalsFromRollups 从 usage_hourly_bucket(totals/sparks)+ usage_duration_histograms
// (slowest/p95,17 槽 SUM 后经 durhist.EstimatePercentile 插值)读 admin scope 的 LogsTotals。
// ok=false 表示窗口内 rollup 无行(未回填),调用方应回退 logsTotalsFromRaw;
// 同样地,若小时桶有成功请求但直方图侧表 17 槽全空(部署后/rebuild 前的历史窗口,
// 侧表比 usage_hourly_bucket 新)也视为未回填,回退原表拿真实 p95/slowest(spec §12)。
func (q *adminStatsQuery) logsTotalsFromRollups(r ObsRange) (LogsTotals, bool, error) {
	db, err := q.logDB()
	if err != nil {
		return LogsTotals{}, false, err
	}

	// totals(通用计数走 usage_hourly_bucket)
	type totalsAgg struct {
		Rows   int64 `gorm:"column:bucket_rows"`
		Total  int64 `gorm:"column:total"`
		Failed int64 `gorm:"column:failed"`
	}
	var agg totalsAgg
	if err := hourWindow(db.Model(&models.UsageHourlyBucket{}), r.Start, r.End).
		Select(`COUNT(*) AS bucket_rows,
			COALESCE(SUM(request_count),0) AS total,
			COALESCE(SUM(failed_count),0) AS failed`).
		Scan(&agg).Error; err != nil {
		return LogsTotals{}, false, err
	}
	if agg.Rows == 0 {
		return LogsTotals{}, false, nil // 窗口未回填 → 回退原表
	}

	// slowest + 合并直方图(usage_duration_histograms)
	type histAgg struct {
		Max int64 `gorm:"column:max_ms"`
		H0  int64 `gorm:"column:s0"`
		H1  int64 `gorm:"column:s1"`
		H2  int64 `gorm:"column:s2"`
		H3  int64 `gorm:"column:s3"`
		H4  int64 `gorm:"column:s4"`
		H5  int64 `gorm:"column:s5"`
		H6  int64 `gorm:"column:s6"`
		H7  int64 `gorm:"column:s7"`
		H8  int64 `gorm:"column:s8"`
		H9  int64 `gorm:"column:s9"`
		H10 int64 `gorm:"column:s10"`
		H11 int64 `gorm:"column:s11"`
		H12 int64 `gorm:"column:s12"`
		H13 int64 `gorm:"column:s13"`
		H14 int64 `gorm:"column:s14"`
		H15 int64 `gorm:"column:s15"`
		H16 int64 `gorm:"column:s16"`
	}
	var ha histAgg
	if err := hourWindow(db.Model(&models.UsageDurationHistogram{}), r.Start, r.End).
		Select(`COALESCE(MAX(max_duration_ms),0) AS max_ms,
			COALESCE(SUM(h0),0) AS s0, COALESCE(SUM(h1),0) AS s1, COALESCE(SUM(h2),0) AS s2,
			COALESCE(SUM(h3),0) AS s3, COALESCE(SUM(h4),0) AS s4, COALESCE(SUM(h5),0) AS s5,
			COALESCE(SUM(h6),0) AS s6, COALESCE(SUM(h7),0) AS s7, COALESCE(SUM(h8),0) AS s8,
			COALESCE(SUM(h9),0) AS s9, COALESCE(SUM(h10),0) AS s10, COALESCE(SUM(h11),0) AS s11,
			COALESCE(SUM(h12),0) AS s12, COALESCE(SUM(h13),0) AS s13, COALESCE(SUM(h14),0) AS s14,
			COALESCE(SUM(h15),0) AS s15, COALESCE(SUM(h16),0) AS s16`).
		Scan(&ha).Error; err != nil {
		return LogsTotals{}, false, err
	}
	counts := [durhist.NumSlots]int64{ha.H0, ha.H1, ha.H2, ha.H3, ha.H4, ha.H5, ha.H6, ha.H7, ha.H8, ha.H9, ha.H10, ha.H11, ha.H12, ha.H13, ha.H14, ha.H15, ha.H16}

	var histTotal int64
	for _, c := range counts {
		histTotal += c
	}
	// 小时桶有成功请求但直方图侧表尚未回填(部署后/rebuild 前的历史窗口)→
	// 回退原表拿真实 p95/slowest(spec §12 "侧表无数据则回退原表")。
	// 注意用"存在成功"区分:全失败窗口直方图本就该空,p95=0 与 raw 一致,不回退。
	if histTotal == 0 && agg.Total > agg.Failed {
		return LogsTotals{}, false, nil
	}

	p95 := durhist.EstimatePercentile(counts, 0.95, ha.Max)

	// 24-slot sparks:窗口 [End-24h, End) 按 (date,hour) 分组
	winStart := r.End - 24*3600
	if winStart < r.Start {
		winStart = r.Start
	}
	sparkTotal, sparkFailed, err := q.rollupSparks(winStart, r.End)
	if err != nil {
		return LogsTotals{}, false, err
	}
	sparkP95, err := q.rollupSparkMax(winStart, r.End)
	if err != nil {
		return LogsTotals{}, false, err
	}

	return LogsTotals{
		Total: agg.Total, Failed: agg.Failed, P95Ms: p95, SlowestMs: ha.Max,
		SparkTotal: sparkTotal, SparkFailed: sparkFailed, SparkP95: sparkP95,
	}, true, nil
}

// rollupSparks:usage_hourly_bucket 按 (date,hour) SUM,散点到 24 槽
// (槽下标算法与 logsHourlySpark(上面)一致:(hourUnix-winStart)/3600)。
func (q *adminStatsQuery) rollupSparks(winStart, end int64) (total, failed []int64, err error) {
	db, err := q.logDB()
	if err != nil {
		return nil, nil, err
	}
	type row struct {
		Date   string `gorm:"column:date"`
		Hour   int    `gorm:"column:hour"`
		Total  int64  `gorm:"column:total"`
		Failed int64  `gorm:"column:failed"`
	}
	var rows []row
	if err = hourWindow(db.Model(&models.UsageHourlyBucket{}), winStart, end).
		Select(`date, hour, COALESCE(SUM(request_count),0) AS total, COALESCE(SUM(failed_count),0) AS failed`).
		Group("date, hour").Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	total, failed = make([]int64, 24), make([]int64, 24)
	for _, rr := range rows {
		t, perr := time.Parse("2006-01-02", rr.Date)
		if perr != nil {
			continue
		}
		hourUnix := t.Unix() + int64(rr.Hour)*3600
		idx := int((hourUnix - winStart) / 3600)
		if idx >= 0 && idx < 24 {
			total[idx] += rr.Total
			failed[idx] += rr.Failed
		}
	}
	return total, failed, nil
}

// rollupSparkMax:usage_duration_histograms 每小时 MAX(max_duration_ms)
// (语义对齐原 logsHourlySparkMax:每小时最慢成功请求)。
func (q *adminStatsQuery) rollupSparkMax(winStart, end int64) ([]int64, error) {
	db, err := q.logDB()
	if err != nil {
		return nil, err
	}
	type row struct {
		Date string `gorm:"column:date"`
		Hour int    `gorm:"column:hour"`
		Max  int64  `gorm:"column:max_ms"`
	}
	var rows []row
	if err := hourWindow(db.Model(&models.UsageDurationHistogram{}), winStart, end).
		Select(`date, hour, COALESCE(MAX(max_duration_ms),0) AS max_ms`).
		Group("date, hour").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]int64, 24)
	for _, rr := range rows {
		t, perr := time.Parse("2006-01-02", rr.Date)
		if perr != nil {
			continue
		}
		hourUnix := t.Unix() + int64(rr.Hour)*3600
		idx := int((hourUnix - winStart) / 3600)
		if idx >= 0 && idx < 24 {
			out[idx] = rr.Max
		}
	}
	return out, nil
}

// logsHourlySpark 把 [winStart, end) 切成 24 个 hour-slot, 统计每槽 COUNT(*)。
// extraWhere 为 "" 时不过滤; 否则附加 AND <extraWhere>。
func logsHourlySpark(base *gorm.DB, winStart, end int64, extraWhere string) ([]int64, error) {
	type row struct {
		Bucket int64
		Count  int64
	}
	q := base.Where("created_at >= ? AND created_at < ?", winStart, end).
		Select("(created_at - (created_at % 3600)) AS bucket, COUNT(*) AS count").
		Group("bucket")
	if extraWhere != "" {
		q = q.Where(extraWhere)
	}
	var rows []row
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]int64, 24)
	for _, x := range rows {
		offset := int((x.Bucket - winStart) / 3600)
		if offset < 0 || offset >= 24 {
			continue
		}
		out[offset] += x.Count
	}
	return out, nil
}

// logsHourlySparkMax 是 p95 sparkline 的近似实现: per-hour MAX(duration)。
// 比 24 次独立 p95 查询便宜。语义上是 "最慢请求时长" 序列。
func logsHourlySparkMax(base *gorm.DB, winStart, end int64) ([]int64, error) {
	type row struct {
		Bucket int64
		MaxDur int64
	}
	var rows []row
	if err := base.Where("created_at >= ? AND created_at < ? AND status = 1", winStart, end).
		Select("(created_at - (created_at % 3600)) AS bucket, COALESCE(MAX(duration), 0) AS max_dur").
		Group("bucket").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]int64, 24)
	for _, x := range rows {
		offset := int((x.Bucket - winStart) / 3600)
		if offset < 0 || offset >= 24 {
			continue
		}
		if x.MaxDur > out[offset] {
			out[offset] = x.MaxDur
		}
	}
	return out, nil
}

func leaderboardByUserFromRequestFacts(db *gorm.DB, metric string, limit int, r ObsRange, modelName string) ([]LeaderRow, error) {
	query := db.Model(&models.UsageLog{}).
		Where("created_at >= ? AND created_at < ? AND user_id > 0", r.Start, r.End)
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	var rows []leaderboardScanRow
	err := query.Select(`user_id AS id,
		COALESCE(SUM(total_cost), 0) AS cost,
		COUNT(*) AS requests,
		COALESCE(SUM(prompt_tokens) + SUM(completion_tokens) + SUM(cache_read_tokens) + SUM(cache_write_tokens), 0) AS tokens,
		0 AS tps, 0 AS ttft_ms`).
		Group("user_id").Order(leaderboardOrderClause(metric)).Limit(limit).Scan(&rows).Error
	return rowsToLeaderRows(rows), err
}

func (q *adminStatsQuery) withUserNames(rows []LeaderRow) ([]LeaderRow, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	var users []models.User
	if err := q.ctx.GetCoreDB().Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	names := make(map[uint]string, len(users))
	for _, user := range users {
		names[user.ID] = user.Username
	}
	for i := range rows {
		rows[i].Name = names[rows[i].ID]
	}
	return rows, nil
}
