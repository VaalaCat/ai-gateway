package dao

import (
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type AdminStatsQuery interface {
	GetOverview() (*OverviewStats, error)
	GetTableCount(table KnownTable) (int64, error)
	GetTotalCost(filter UsageLogListFilter) (int64, error)
	GetTrend(days int, userID *uint) ([]TrendItem, error)
	HourlyTrend(r ObsRange, scope Scope) ([]TimeBucket, error)
	Distribution(by string, r ObsRange, scope Scope) ([]Bucket, error)
	Leaderboard(by, metric string, limit int, r ObsRange, scope Scope) ([]LeaderRow, error)
	SpeedCompare(dimension string, r ObsRange, scope Scope) ([]SpeedRow, error)
	ChannelMetrics(r ObsRange) ([]ChannelMetric, error)
	AgentMetrics(r ObsRange) ([]AgentMetric, error)
	ErrorDistribution(by string, r ObsRange, scope Scope) ([]ErrBucket, error)
	StageLatencyP95(filter UsageLogListFilter, r ObsRange) (StageLatency, error)
	DashboardKpis(r ObsRange, scope Scope) (KpiBundle, error)
	CostTrendStackedByModel(r ObsRange, scope Scope, topN int) (CostTrendStacked, error)
	CacheSaving(r ObsRange, scope Scope) (CacheSaving, error)
	LogsTotals(r ObsRange, scope Scope) (LogsTotals, error)
}

// SpeedRow 是 SpeedCompare 输出的一行 (维度: model | channel)。
// ID 仅在 dimension=channel 时填充; model 维度无数字主键, ID=0。
type SpeedRow struct {
	ID     uint    `json:"id,omitempty"`
	Name   string  `json:"name"`
	TTFTMs int64   `json:"ttft_ms"`
	TPS    float64 `json:"tps"`
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

func (q *adminStatsQuery) GetOverview() (*OverviewStats, error) {
	db := q.ctx.GetDB()
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
	if err := db.Model(&models.UsageLog{}).Count(&s.UsageLogCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.UsageLog{}).Select("COALESCE(SUM(total_cost), 0)").Scan(&s.TotalCost).Error; err != nil {
		return nil, err
	}
	return s, nil
}

func (q *adminStatsQuery) GetTableCount(table KnownTable) (int64, error) {
	var count int64
	err := q.ctx.GetDB().Table(string(table)).Count(&count).Error
	return count, err
}

func (q *adminStatsQuery) GetTotalCost(filter UsageLogListFilter) (int64, error) {
	db := applyUsageLogFilter(q.ctx.GetDB().Model(&models.UsageLog{}), filter)
	var cost int64
	err := db.Select("COALESCE(SUM(total_cost), 0)").Scan(&cost).Error
	return cost, err
}

func (q *adminStatsQuery) GetTrend(days int, userID *uint) ([]TrendItem, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	db := q.ctx.GetDB().Model(&models.UsageLog{}).Where("created_at >= ?", cutoff)
	if userID != nil {
		db = db.Where("user_id = ?", *userID)
	}

	var items []TrendItem
	err := db.Select(
		"DATE(created_at, 'unixepoch') as date, " +
			"COUNT(*) as requests, " +
			"COALESCE(SUM(prompt_tokens), 0) as prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) as completion_tokens, " +
			"COALESCE(SUM(total_cost), 0) as cost",
	).Group("date").Order("date ASC").Find(&items).Error
	return items, err
}

func (q *adminStatsQuery) HourlyTrend(r ObsRange, scope Scope) ([]TimeBucket, error) {
	if r.End <= r.Start {
		return nil, nil
	}
	db := q.ctx.GetDB()
	if scope.IsAdmin {
		return hourlyTrendFromBuckets(db, r)
	}
	return hourlyTrendFromUsageLog(db, r, scope.UserID)
}

func hourlyTrendFromBuckets(db *gorm.DB, r ObsRange) ([]TimeBucket, error) {
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
	if r.Gran == GranDay {
		groupCols = "date"
	}

	var rows []row
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select(groupCols + `,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,
			COALESCE(SUM(total_cost), 0) AS cost`).
		Group(groupCols).
		Order(groupCols).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	bucketSec := int64(3600)
	if r.Gran == GranDay {
		bucketSec = 86400
	}

	out := make([]TimeBucket, 0, len(rows))
	for _, x := range rows {
		ts, label := bucketTsLabel(x.Date, x.Hour, r.Gran)
		// 区间重叠: bucket [ts, ts+bucketSec) 与 [r.Start, r.End) 有交集
		if ts+bucketSec <= r.Start || ts >= r.End {
			continue
		}
		out = append(out, TimeBucket{
			Ts: ts, Label: label,
			Cost: x.Cost, Requests: x.Requests, Tokens: x.Tokens,
		})
	}
	return out, nil
}

func hourlyTrendFromUsageLog(db *gorm.DB, r ObsRange, userID uint) ([]TimeBucket, error) {
	bucketSec := int64(3600)
	if r.Gran == GranDay {
		bucketSec = 86400
	}

	type row struct {
		Bucket   int64
		Requests int64
		Tokens   int64
		Cost     int64
	}
	var rows []row
	err := db.Model(&models.UsageLog{}).
		Where("created_at >= ? AND created_at < ? AND user_id = ?", r.Start, r.End, userID).
		Select(fmt.Sprintf(`(created_at - (created_at %% %d)) AS bucket,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,
			COALESCE(SUM(total_cost), 0) AS cost`, bucketSec)).
		Group("bucket").
		Order("bucket").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]TimeBucket, 0, len(rows))
	for _, x := range rows {
		out = append(out, TimeBucket{
			Ts: x.Bucket, Label: formatBucketLabel(x.Bucket, r.Gran),
			Cost: x.Cost, Requests: x.Requests, Tokens: x.Tokens,
		})
	}
	return out, nil
}

func (q *adminStatsQuery) Distribution(by string, r ObsRange, scope Scope) ([]Bucket, error) {
	if by != "model" {
		return nil, fmt.Errorf("distribution: unsupported dimension %q", by)
	}
	db := q.ctx.GetDB()
	if !scope.IsAdmin {
		return distributionByModelFromUsageLog(db, r, scope.UserID)
	}
	return distributionByModelFromBuckets(db, r)
}

func distributionByModelFromBuckets(db *gorm.DB, r ObsRange) ([]Bucket, error) {
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	var rows []distributionScanRow
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select("model_name AS name, COALESCE(SUM(request_count), 0) AS value").
		Group("model_name").
		Order("value DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return normalizeBuckets(rows), nil
}

func distributionByModelFromUsageLog(db *gorm.DB, r ObsRange, userID uint) ([]Bucket, error) {
	var rows []distributionScanRow
	err := db.Model(&models.UsageLog{}).
		Where("created_at >= ? AND created_at < ? AND user_id = ?", r.Start, r.End, userID).
		Select("model_name AS name, COUNT(*) AS value").
		Group("model_name").
		Order("value DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
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

func (q *adminStatsQuery) Leaderboard(by, metric string, limit int, r ObsRange, scope Scope) ([]LeaderRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	metric = normalizeLeaderboardMetric(metric)
	db := q.ctx.GetDB()
	switch by {
	case "user":
		if !scope.IsAdmin {
			// 单用户 scope 下 user 维度无意义
			return nil, nil
		}
		return leaderboardByUser(db, metric, limit, r)
	case "model":
		if !scope.IsAdmin {
			return leaderboardByModelUser(db, metric, limit, r, scope.UserID)
		}
		return leaderboardByModel(db, metric, limit, r)
	case "channel":
		if !scope.IsAdmin {
			return leaderboardByChannelUser(db, metric, limit, r, scope.UserID)
		}
		return leaderboardByChannel(db, metric, limit, r)
	default:
		return nil, fmt.Errorf("leaderboard: unsupported by %q", by)
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

func leaderboardByModel(db *gorm.DB, metric string, limit int, r ObsRange) ([]LeaderRow, error) {
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	q := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select(`
			0 AS id,
			model_name AS name,
			COALESCE(SUM(total_cost), 0) AS cost,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,` + hourlyBucketStreamSelect).
		Group("model_name")
	if leaderboardNeedsStream(metric) {
		q = q.Having("SUM(stream_request_count) > 0")
	}
	var rows []leaderboardScanRow
	if err := q.Order(leaderboardOrderClause(metric)).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rowsToLeaderRows(rows), nil
}

func leaderboardByModelUser(db *gorm.DB, metric string, limit int, r ObsRange, userID uint) ([]LeaderRow, error) {
	q := db.Model(&models.UsageLog{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, r.Start, r.End).
		Select(`
			0 AS id,
			model_name AS name,
			COALESCE(SUM(total_cost), 0) AS cost,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,` + usageLogStreamSelect).
		Group("model_name")
	if leaderboardNeedsStream(metric) {
		q = q.Having("SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN 1 ELSE 0 END) > 0")
	}
	var rows []leaderboardScanRow
	if err := q.Order(leaderboardOrderClause(metric)).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rowsToLeaderRows(rows), nil
}

func leaderboardByChannel(db *gorm.DB, metric string, limit int, r ObsRange) ([]LeaderRow, error) {
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	q := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ? AND channel_id > 0", startDate, endDate).
		Select(`
			channel_id AS id,
			COALESCE(MIN(NULLIF(channel_name, '')), '') AS name,
			COALESCE(SUM(total_cost), 0) AS cost,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,` + hourlyBucketStreamSelect).
		Group("channel_id")
	if leaderboardNeedsStream(metric) {
		q = q.Having("SUM(stream_request_count) > 0")
	}
	var rows []leaderboardScanRow
	if err := q.Order(leaderboardOrderClause(metric)).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rowsToLeaderRows(rows), nil
}

func leaderboardByChannelUser(db *gorm.DB, metric string, limit int, r ObsRange, userID uint) ([]LeaderRow, error) {
	q := db.Model(&models.UsageLog{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ? AND channel_id > 0", userID, r.Start, r.End).
		Select(`
			channel_id AS id,
			COALESCE(MIN(NULLIF(channel_name, '')), '') AS name,
			COALESCE(SUM(total_cost), 0) AS cost,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens) + SUM(completion_tokens), 0) AS tokens,` + usageLogStreamSelect).
		Group("channel_id")
	if leaderboardNeedsStream(metric) {
		q = q.Having("SUM(CASE WHEN is_stream=1 AND status=1 AND completion_tokens>0 THEN 1 ELSE 0 END) > 0")
	}
	var rows []leaderboardScanRow
	if err := q.Order(leaderboardOrderClause(metric)).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rowsToLeaderRows(rows), nil
}

func (q *adminStatsQuery) SpeedCompare(dimension string, r ObsRange, scope Scope) ([]SpeedRow, error) {
	if !scope.IsAdmin {
		return nil, nil
	}
	switch dimension {
	case "model":
		return speedCompareByModel(q.ctx.GetDB(), r)
	case "channel":
		return speedCompareByChannel(q.ctx.GetDB(), r)
	default:
		return nil, fmt.Errorf("speed_compare: unsupported dimension %q", dimension)
	}
}

func speedCompareByModel(db *gorm.DB, r ObsRange) ([]SpeedRow, error) {
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	type row struct {
		Name   string
		TTFTMs int64
		TPS    float64
	}
	var rows []row
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select(`model_name AS name,
			SUM(sum_first_response_ms) / SUM(stream_request_count) AS ttft_ms,
			(SUM(sum_stream_completion_tokens) * 1000.0) / SUM(sum_generation_ms) AS tps`).
		Group("model_name").
		Having("SUM(stream_request_count) > 0 AND SUM(sum_generation_ms) > 0").
		Order("ttft_ms ASC").
		Limit(10).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]SpeedRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, SpeedRow{Name: x.Name, TTFTMs: x.TTFTMs, TPS: x.TPS})
	}
	return out, nil
}

func speedCompareByChannel(db *gorm.DB, r ObsRange) ([]SpeedRow, error) {
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	type row struct {
		ID     uint
		Name   string
		TTFTMs int64
		TPS    float64
	}
	var rows []row
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select(`channel_id AS id,
			COALESCE(MIN(NULLIF(channel_name, '')), '') AS name,
			SUM(sum_first_response_ms) / SUM(stream_request_count) AS ttft_ms,
			(SUM(sum_stream_completion_tokens) * 1000.0) / SUM(sum_generation_ms) AS tps`).
		Group("channel_id").
		Having("SUM(stream_request_count) > 0 AND SUM(sum_generation_ms) > 0").
		Order("ttft_ms ASC").
		Limit(10).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]SpeedRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, SpeedRow{ID: x.ID, Name: x.Name, TTFTMs: x.TTFTMs, TPS: x.TPS})
	}
	return out, nil
}

// ChannelMetric 是 Monitoring 页面 channel 维度的一行,聚合 24h 内 channel 用量。
// TTFTP95Ms / LatencyP95Ms 目前固定为 0; Task 2.8 会接入 PercentileTTFT helper 填充 p95 数据。
type ChannelMetric struct {
	ID           uint    `json:"id"`
	Name         string  `json:"name"`
	Requests     int64   `json:"requests"`
	ErrorRatio   float64 `json:"error_ratio"`
	TTFTP95Ms    int64   `json:"ttft_p95_ms"`
	TPSAvg       float64 `json:"tps_avg"`
	LatencyP95Ms int64   `json:"latency_p95_ms"`
	Spark24h     []int64 `json:"spark_24h"`
}

// AgentMetric 是 Monitoring 页面 agent 维度的一行,聚合 24h 内 agent 用量,
// 并 JOIN models.Agent 拿到 Name/Status/LastSeen。
// TTFTP95Ms / LatencyP95Ms 目前固定为 0; Task 2.8 会接入 p95 helper 填充。
type AgentMetric struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Online       bool    `json:"online"`
	LastSeen     int64   `json:"last_seen"`
	Requests     int64   `json:"requests"`
	TTFTP95Ms    int64   `json:"ttft_p95_ms"`
	TPSAvg       float64 `json:"tps_avg"`
	LatencyP95Ms int64   `json:"latency_p95_ms"`
	Spark24h     []int64 `json:"spark_24h"`
}

// channelMetricAggRow 是 ChannelMetrics 聚合扫描的中间行。
type channelMetricAggRow struct {
	ID          uint
	Name        string
	Requests    int64
	FailedCount int64
	SumComp     int64
	SumGenMs    int64
}

// agentMetricAggRow 是 AgentMetrics 聚合扫描的中间行。
type agentMetricAggRow struct {
	ID          string
	Requests    int64
	FailedCount int64
	SumComp     int64
	SumGenMs    int64
}

// ChannelMetrics 返回 Monitoring 页面 channel 维度的指标行;
// 过滤 channel_id > 0 → 排除 BYOK 行 (Monitoring 页只看 admin channel)。
// TODO(Task 2.8): 接入 PercentileTTFT helper 后填充 TTFTP95Ms / LatencyP95Ms。
func (q *adminStatsQuery) ChannelMetrics(r ObsRange) ([]ChannelMetric, error) {
	db := q.ctx.GetDB()
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	var aggs []channelMetricAggRow
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ? AND channel_id > 0", startDate, endDate).
		Select(`channel_id AS id,
			COALESCE(MIN(NULLIF(channel_name, '')), '') AS name,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(failed_count), 0) AS failed_count,
			COALESCE(SUM(sum_stream_completion_tokens), 0) AS sum_comp,
			COALESCE(SUM(sum_generation_ms), 0) AS sum_gen_ms`).
		Group("channel_id").
		Order("requests DESC").
		Scan(&aggs).Error
	if err != nil {
		return nil, err
	}

	sparks, err := channelSpark24h(db, r)
	if err != nil {
		return nil, err
	}

	out := make([]ChannelMetric, 0, len(aggs))
	for _, a := range aggs {
		var errorRatio float64
		if a.Requests > 0 {
			errorRatio = float64(a.FailedCount) / float64(a.Requests)
		}
		var tps float64
		if a.SumGenMs > 0 {
			tps = float64(a.SumComp) * 1000.0 / float64(a.SumGenMs)
		}
		out = append(out, ChannelMetric{
			ID:           a.ID,
			Name:         a.Name,
			Requests:     a.Requests,
			ErrorRatio:   errorRatio,
			TPSAvg:       tps,
			TTFTP95Ms:    0, // TODO(Task 2.8): p95 from usage_logs short-window
			LatencyP95Ms: 0, // TODO(Task 2.8): p95 from usage_logs short-window
			Spark24h:     sparks[a.ID],
		})
	}
	return out, nil
}

// AgentMetrics 返回 Monitoring 页面 agent 维度的指标行;
// 过滤 agent_id <> '' → 排除未归属 agent 的旧行。JOIN agents 表拿 Name/Status/LastSeen。
// TODO(Task 2.8): 接入 PercentileTTFT helper 后填充 TTFTP95Ms / LatencyP95Ms。
func (q *adminStatsQuery) AgentMetrics(r ObsRange) ([]AgentMetric, error) {
	db := q.ctx.GetDB()
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	var aggs []agentMetricAggRow
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ? AND agent_id <> ''", startDate, endDate).
		Select(`agent_id AS id,
			COALESCE(SUM(request_count), 0) AS requests,
			COALESCE(SUM(failed_count), 0) AS failed_count,
			COALESCE(SUM(sum_stream_completion_tokens), 0) AS sum_comp,
			COALESCE(SUM(sum_generation_ms), 0) AS sum_gen_ms`).
		Group("agent_id").
		Order("requests DESC").
		Scan(&aggs).Error
	if err != nil {
		return nil, err
	}

	var agents []models.Agent
	if err := db.Find(&agents).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]*models.Agent, len(agents))
	for i := range agents {
		byID[agents[i].AgentID] = &agents[i]
	}

	sparks, err := agentSpark24h(db, r)
	if err != nil {
		return nil, err
	}

	out := make([]AgentMetric, 0, len(aggs))
	for _, a := range aggs {
		am := AgentMetric{
			ID:           a.ID,
			Requests:     a.Requests,
			TTFTP95Ms:    0, // TODO(Task 2.8): p95 from usage_logs short-window
			LatencyP95Ms: 0, // TODO(Task 2.8): p95 from usage_logs short-window
			Spark24h:     sparks[a.ID],
		}
		if a.SumGenMs > 0 {
			am.TPSAvg = float64(a.SumComp) * 1000.0 / float64(a.SumGenMs)
		}
		if agent, ok := byID[a.ID]; ok {
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
func channelSpark24h(db *gorm.DB, r ObsRange) (map[uint][]int64, error) {
	winStart := r.End - 24*3600
	if winStart < r.Start {
		winStart = r.Start
	}
	startDate := time.Unix(winStart, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	type row struct {
		ID       uint
		Date     string
		Hour     int
		Requests int64
	}
	var rows []row
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ? AND channel_id > 0", startDate, endDate).
		Select("channel_id AS id, date, hour, COALESCE(SUM(request_count), 0) AS requests").
		Group("channel_id, date, hour").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint][]int64)
	for _, row := range rows {
		ts, _ := bucketTsLabel(row.Date, row.Hour, GranHour)
		if ts < winStart || ts >= r.End {
			continue
		}
		offset := int((ts - winStart) / 3600)
		if offset < 0 || offset >= 24 {
			continue
		}
		if out[row.ID] == nil {
			out[row.ID] = make([]int64, 24)
		}
		out[row.ID][offset] += row.Requests
	}
	return out, nil
}

// agentSpark24h 与 channelSpark24h 同语义,但维度为 agent_id (string)。
func agentSpark24h(db *gorm.DB, r ObsRange) (map[string][]int64, error) {
	winStart := r.End - 24*3600
	if winStart < r.Start {
		winStart = r.Start
	}
	startDate := time.Unix(winStart, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")
	type row struct {
		ID       string
		Date     string
		Hour     int
		Requests int64
	}
	var rows []row
	err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ? AND agent_id <> ''", startDate, endDate).
		Select("agent_id AS id, date, hour, COALESCE(SUM(request_count), 0) AS requests").
		Group("agent_id, date, hour").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string][]int64)
	for _, row := range rows {
		ts, _ := bucketTsLabel(row.Date, row.Hour, GranHour)
		if ts < winStart || ts >= r.End {
			continue
		}
		offset := int((ts - winStart) / 3600)
		if offset < 0 || offset >= 24 {
			continue
		}
		if out[row.ID] == nil {
			out[row.ID] = make([]int64, 24)
		}
		out[row.ID][offset] += row.Requests
	}
	return out, nil
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
// scope 非 admin 时返回 nil,nil; by=channel 用 LEFT JOIN channels 保留 BYOK 行 (channel_id=0 或外键失效) 的空 name。
func (q *adminStatsQuery) ErrorDistribution(by string, r ObsRange, scope Scope) ([]ErrBucket, error) {
	if !scope.IsAdmin {
		return nil, nil
	}
	switch by {
	case "stage":
		return errorDistributionByStage(q.ctx.GetDB(), r)
	case "channel":
		return errorDistributionByChannel(q.ctx.GetDB(), r)
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
	err := db.Table("usage_logs").
		Where("status = 0 AND created_at >= ? AND created_at < ?", r.Start, r.End).
		Select("error_stage AS stage, COUNT(*) AS count").
		Group("error_stage").
		Order("count DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
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

func errorDistributionByChannel(db *gorm.DB, r ObsRange) ([]ErrBucket, error) {
	type row struct {
		ID    uint
		Name  string
		Count int64
	}
	var rows []row
	err := db.Table("usage_logs ul").
		Joins("LEFT JOIN channels c ON c.id = ul.channel_id").
		Where("ul.status = 0 AND ul.created_at >= ? AND ul.created_at < ?", r.Start, r.End).
		Select("ul.channel_id AS id, COALESCE(c.name, '') AS name, COUNT(*) AS count").
		Group("ul.channel_id, c.name").
		Order("count DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
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
		out = append(out, ErrBucket{ID: x.ID, Name: x.Name, Count: x.Count, Ratio: ratio})
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
func (q *adminStatsQuery) StageLatencyP95(filter UsageLogListFilter, r ObsRange) (StageLatency, error) {
	db := q.ctx.GetDB()
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

// DashboardKpis 组合 HourlyTrend + 周期对比 + admin/user 专属字段, 输出 Dashboard 顶部卡片所需的 KpiBundle。
// Spark 固定走 hour 粒度 (r.Gran=GranDay 时内部强制为 GranHour); admin scope 额外输出 SuccessRate / Users,
// user scope 额外输出 Quota。previous 周期为紧邻 r.Start 之前等长度窗口,用于计算 Delta。
func (q *adminStatsQuery) DashboardKpis(r ObsRange, scope Scope) (KpiBundle, error) {
	hourR := r
	hourR.Gran = GranHour

	currentBuckets, err := q.HourlyTrend(hourR, scope)
	if err != nil {
		return KpiBundle{}, err
	}

	duration := r.End - r.Start
	prevR := ObsRange{Start: r.Start - duration, End: r.Start, Gran: GranHour}
	prevBuckets, err := q.HourlyTrend(prevR, scope)
	if err != nil {
		return KpiBundle{}, err
	}

	bundle := KpiBundle{
		Requests: kpiMetric(currentBuckets, prevBuckets, func(b TimeBucket) int64 { return b.Requests }),
		Cost:     kpiMetric(currentBuckets, prevBuckets, func(b TimeBucket) int64 { return b.Cost }),
		Tokens:   kpiMetric(currentBuckets, prevBuckets, func(b TimeBucket) int64 { return b.Tokens }),
	}

	if scope.IsAdmin {
		successRate, err := kpiSuccessRate(q.ctx.GetDB(), r, hourR)
		if err != nil {
			return KpiBundle{}, err
		}
		bundle.SuccessRate = &successRate

		users, err := kpiUsers(q.ctx.GetDB(), r)
		if err != nil {
			return KpiBundle{}, err
		}
		bundle.Users = &users
		return bundle, nil
	}

	quota, err := kpiQuota(q.ctx.GetDB(), scope.UserID)
	if err != nil {
		return KpiBundle{}, err
	}
	bundle.Quota = &quota
	return bundle, nil
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
// Spark 同样为逐小时 success 计数。Delta 暂固定为 0。
//
// 过滤策略: SQL 仅按 date 粗筛 (避免按 hour 算 ts 后跨日 join 复杂度),
// 然后在 Go 里按 hourR.Start/End 二次过滤,保证起点当天 hourR.Start 之前的
// hour 不被计入 total。
func kpiSuccessRate(db *gorm.DB, r, hourR ObsRange) (KpiMetric, error) {
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	type sparkRow struct {
		Date    string
		Hour    int
		Success int64
	}
	var rows []sparkRow
	if err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select("date, hour, COALESCE(SUM(success_count), 0) AS success").
		Group("date, hour").
		Order("date, hour").
		Scan(&rows).Error; err != nil {
		return KpiMetric{}, err
	}

	var success int64
	spark := make([]int64, 0, len(rows))
	for _, x := range rows {
		ts, _ := bucketTsLabel(x.Date, x.Hour, GranHour)
		if ts < hourR.Start || ts >= hourR.End {
			continue
		}
		success += x.Success
		spark = append(spark, x.Success)
	}
	return KpiMetric{Value: success, Spark: spark, Delta: 0}, nil
}

// kpiUsers 统计 admin scope 的用户 KPI:
// Value=总用户数 (全表 count), Active=range 内有 usage_log 的 distinct user_id 数,
// New=range 内 created_at 落在窗口内的 users 数。
func kpiUsers(db *gorm.DB, r ObsRange) (KpiUsers, error) {
	var total int64
	if err := db.Model(&models.User{}).Count(&total).Error; err != nil {
		return KpiUsers{}, err
	}
	var newCount int64
	if err := db.Model(&models.User{}).
		Where("created_at >= ? AND created_at < ?", r.Start, r.End).
		Count(&newCount).Error; err != nil {
		return KpiUsers{}, err
	}
	var active int64
	if err := db.Raw(`
		SELECT COUNT(DISTINCT user_id) FROM usage_logs
		WHERE created_at >= ? AND created_at < ?`, r.Start, r.End).Scan(&active).Error; err != nil {
		return KpiUsers{}, err
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

// CostTrendStackedByModel 按 (time-bucket × model_name) 聚合 total_cost,
// 时间槽由 r.Gran 决定: hour → (date, hour) 桶; day → date 桶。
// 仅返回 series 总成本 top-N 的 model, 其余合并为 "others"。
//
// Phase 1: admin-only。非 admin scope 返回空 CostTrendStacked (无 error),
// 因为 usage_hourly_buckets 不带 user_id 维度;
// 用户侧 trend 已由 /v1/stats/dashboard 的 HourlyTrend 提供。
// TODO: 后续如果需要 user-scope stack-by-model, 改走 UsageLog 聚合。
func (q *adminStatsQuery) CostTrendStackedByModel(r ObsRange, scope Scope, topN int) (CostTrendStacked, error) {
	if !scope.IsAdmin {
		return CostTrendStacked{Buckets: []StackedBucket{}, SeriesOrder: []string{}}, nil
	}
	if topN <= 0 {
		topN = 5
	}
	if r.End <= r.Start {
		return CostTrendStacked{Buckets: []StackedBucket{}, SeriesOrder: []string{}}, nil
	}

	db := q.ctx.GetDB()
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	type row struct {
		Date      string
		Hour      int
		ModelName string
		Cost      int64
	}
	groupCols := "date, hour, model_name"
	selectCols := "date, hour, model_name, COALESCE(SUM(total_cost), 0) AS cost"
	if r.Gran == GranDay {
		groupCols = "date, model_name"
		// day 粒度: hour 仍 SELECT 0,以便 row 结构统一
		selectCols = "date, 0 AS hour, model_name, COALESCE(SUM(total_cost), 0) AS cost"
	}
	var rows2 []row
	if err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select(selectCols).
		Group(groupCols).
		Scan(&rows2).Error; err != nil {
		return CostTrendStacked{}, err
	}

	// 第一遍: 按 model 累计总成本,选 top-N。
	modelTotals := make(map[string]int64)
	for _, x := range rows2 {
		modelTotals[x.ModelName] += x.Cost
	}
	type mt struct {
		Name string
		Cost int64
	}
	mts := make([]mt, 0, len(modelTotals))
	for k, v := range modelTotals {
		mts = append(mts, mt{Name: k, Cost: v})
	}
	// 简单选 top-N: 反复挑最大,N 通常很小所以 O(N*K) 可接受。
	topSet := make(map[string]bool)
	seriesOrder := make([]string, 0, topN+1)
	for i := 0; i < topN && len(mts) > 0; i++ {
		idx := 0
		for j := 1; j < len(mts); j++ {
			if mts[j].Cost > mts[idx].Cost {
				idx = j
			}
		}
		topSet[mts[idx].Name] = true
		seriesOrder = append(seriesOrder, mts[idx].Name)
		mts = append(mts[:idx], mts[idx+1:]...)
	}
	hasOthers := len(mts) > 0

	// 第二遍: 按时间槽聚合 series。
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
	for _, x := range rows2 {
		ts, label := bucketTsLabel(x.Date, x.Hour, r.Gran)
		// 区间重叠: bucket [ts, ts+bucketSec) 与 [r.Start, r.End) 有交集
		// 与 HourlyTrend (stats.go:188) 保持一致, 修复 day 粒度下窗口不对齐 00:00 时
		// 左端 bucket 被砍 (例: r.Start=昨天12:00 时, 昨日 ts=昨天00:00 < r.Start → 丢)
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
		seriesName := x.ModelName
		if !topSet[seriesName] {
			seriesName = "others"
		}
		out[idx].Series[seriesName] += x.Cost
	}
	if hasOthers {
		seriesOrder = append(seriesOrder, "others")
	}
	return CostTrendStacked{Buckets: out, SeriesOrder: seriesOrder}, nil
}

// CacheSaving 计算窗口内的缓存命中收益。
//
// Phase 1: admin-only。非 admin scope 返回零值 + 固定 vs_label。
// 公式:
//   hit_ratio    = sum(cache_read_tokens) / sum(prompt_tokens + cache_read_tokens)
//   saved_tokens = sum(cache_read_tokens)
//   saved_cost   = saved_tokens * (sum(input_cost) / sum(prompt_tokens))
// 分母为 0 时各项分别回退 0,避免除零。saved_cost 公式是粗略估算
// (用 prompt 平均单价乘以 cache_read 数量),够给前端展示一个数量级。
func (q *adminStatsQuery) CacheSaving(r ObsRange, scope Scope) (CacheSaving, error) {
	if !scope.IsAdmin {
		return CacheSaving{VsLabel: "vs no-cache"}, nil
	}
	if r.End <= r.Start {
		return CacheSaving{VsLabel: "vs no-cache"}, nil
	}
	db := q.ctx.GetDB()
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	type agg struct {
		Prompt     int64
		CacheRead  int64
		CacheWrite int64
		InputCost  int64
	}
	var a agg
	if err := db.Model(&models.UsageHourlyBucket{}).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Select(`COALESCE(SUM(prompt_tokens), 0) AS prompt,
			COALESCE(SUM(cache_read_tokens), 0) AS cache_read,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write,
			COALESCE(SUM(input_cost), 0) AS input_cost`).
		Scan(&a).Error; err != nil {
		return CacheSaving{}, err
	}

	out := CacheSaving{
		SavedTokens: a.CacheRead,
		ReadTokens:  a.CacheRead,
		WriteTokens: a.CacheWrite,
		VsLabel:     "vs no-cache",
	}
	denom := a.Prompt + a.CacheRead
	if denom > 0 {
		out.HitRatio = float64(a.CacheRead) / float64(denom)
	}
	if a.Prompt > 0 {
		out.SavedCost = int64(float64(a.CacheRead) * float64(a.InputCost) / float64(a.Prompt))
	}
	return out, nil
}

// LogsTotals 是 LogsTotals DAO 输出, 给 /v1/logs/insights 用。
// Spark* 长度恒为 24, 槽位对应 r.End 前的最后 24 小时;
// SparkP95 用 MAX(duration) 作 p95 的近似 (per-bucket 真实 p95 要 24 个独立查询, 用 MAX 折中)。
type LogsTotals struct {
	Total      int64   `json:"total"`
	Failed     int64   `json:"failed"`
	P95Ms      int64   `json:"p95_ms"`
	SlowestMs  int64   `json:"slowest_ms"`
	SparkTotal []int64 `json:"spark_total"`
	SparkFailed []int64 `json:"spark_failed"`
	SparkP95   []int64 `json:"spark_p95"`
}

// LogsTotals 聚合 usage_logs 在 r 窗口内的请求总数 / 失败数 / duration p95 / 最慢请求 / 24-slot spark。
// 非 admin scope 自动注入 user_id 过滤。
// p95 计算用 SQLite 友好的 OFFSET 近似 (跟 stageP95 / PercentileTTFT 同思路)。
func (q *adminStatsQuery) LogsTotals(r ObsRange, scope Scope) (LogsTotals, error) {
	db := q.ctx.GetDB()
	if r.End <= r.Start {
		return LogsTotals{
			SparkTotal:  make([]int64, 24),
			SparkFailed: make([]int64, 24),
			SparkP95:    make([]int64, 24),
		}, nil
	}

	base := func() *gorm.DB {
		q := db.Model(&models.UsageLog{}).
			Where("created_at >= ? AND created_at < ?", r.Start, r.End)
		if !scope.IsAdmin {
			q = q.Where("user_id = ?", scope.UserID)
		}
		return q
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return LogsTotals{}, err
	}
	var failed int64
	if err := base().Where("status = 0").Count(&failed).Error; err != nil {
		return LogsTotals{}, err
	}

	// p95 over status=1 rows (success) using OFFSET approximation.
	successCnt := int64(0)
	if err := base().Where("status = 1").Count(&successCnt).Error; err != nil {
		return LogsTotals{}, err
	}
	var p95 int64
	var slowest int64
	if successCnt > 0 {
		offset := successCnt * 95 / 100
		if offset >= successCnt {
			offset = successCnt - 1
		}
		if err := base().Where("status = 1").
			Select("duration").
			Order("duration ASC").
			Offset(int(offset)).Limit(1).
			Scan(&p95).Error; err != nil {
			return LogsTotals{}, err
		}
		if err := base().Where("status = 1").
			Select("COALESCE(MAX(duration), 0)").
			Scan(&slowest).Error; err != nil {
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
		Total: total, Failed: failed, P95Ms: p95, SlowestMs: slowest,
		SparkTotal: sparkTotal, SparkFailed: sparkFailed, SparkP95: sparkP95,
	}, nil
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

// leaderboardByUser 仅 admin 调用; token_daily_billings 不带 stream 累计字段,
// 故 user 维度 leaderboard 上 tps/ttft 始终为 0 (metric=tps/ttft 时该维度退化为按 0 排序)。
func leaderboardByUser(db *gorm.DB, metric string, limit int, r ObsRange) ([]LeaderRow, error) {
	startDate := time.Unix(r.Start, 0).UTC().Format("2006-01-02")
	endDate := time.Unix(r.End, 0).UTC().Format("2006-01-02")

	q := db.Table("token_daily_billings AS tdb").
		Joins("LEFT JOIN users u ON u.id = tdb.user_id").
		Where("tdb.date >= ? AND tdb.date <= ?", startDate, endDate).
		Select(`
			tdb.user_id AS id,
			COALESCE(u.username, '') AS name,
			COALESCE(SUM(tdb.total_cost), 0) AS cost,
			COALESCE(SUM(tdb.request_count), 0) AS requests,
			COALESCE(SUM(tdb.prompt_tokens) + SUM(tdb.completion_tokens), 0) AS tokens,
			0 AS tps,
			0 AS ttft_ms`).
		Group("tdb.user_id, u.username")
	var rows []leaderboardScanRow
	if err := q.Order(leaderboardOrderClause(metric)).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rowsToLeaderRows(rows), nil
}
