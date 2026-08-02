package dao

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"gorm.io/gorm"
)

type exactUsageGroup struct {
	identitySQL string
	idSQL       string
	nameSQL     string
	groupBy     string
	identity    func(models.UsageLog) string
}

var exactUsageGroups = map[string]exactUsageGroup{
	"model": {
		identitySQL: "model_name", idSQL: "0", nameSQL: "model_name", groupBy: "model_name",
		identity: func(log models.UsageLog) string { return log.ModelName },
	},
	"channel": {
		identitySQL: "CAST(channel_id AS TEXT)", idSQL: "channel_id",
		nameSQL: "COALESCE(MIN(NULLIF(channel_name, '')), '')", groupBy: "channel_id",
		identity: func(log models.UsageLog) string { return strconv.FormatUint(uint64(log.ChannelID), 10) },
	},
	"agent": {
		identitySQL: "agent_id", idSQL: "0", nameSQL: "''", groupBy: "agent_id",
		identity: func(log models.UsageLog) string { return log.AgentID },
	},
}

type exactUsageRow struct {
	Identity           string
	ID                 uint
	Name               string
	Requests           int64
	FailedCount        int64
	PromptTokens       int64
	CompletionTokens   int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	TotalCost          int64
	RawCost            int64
	StreamRequests     int64
	SumFirstResponseMs int64
	SumGenerationMs    int64
	SumStreamTokens    int64
}

const exactUsageFullSums = "COALESCE(SUM(request_count), 0) AS requests, " +
	"COALESCE(SUM(failed_count), 0) AS failed_count, " +
	"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
	"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
	"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, " +
	"COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens, " +
	"COALESCE(SUM(total_cost), 0) AS total_cost, " +
	"COALESCE(SUM(raw_cost), 0) AS raw_cost, " +
	"COALESCE(SUM(stream_request_count), 0) AS stream_requests, " +
	"COALESCE(SUM(sum_first_response_ms), 0) AS sum_first_response_ms, " +
	"COALESCE(SUM(sum_generation_ms), 0) AS sum_generation_ms, " +
	"COALESCE(SUM(sum_stream_completion_tokens), 0) AS sum_stream_tokens"

const exactUsageBoundarySums = "COUNT(*) AS requests, " +
	"COALESCE(SUM(CASE WHEN status = 0 THEN 1 ELSE 0 END), 0) AS failed_count, " +
	"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
	"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
	"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, " +
	"COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens, " +
	"COALESCE(SUM(total_cost), 0) AS total_cost, " +
	"COALESCE(SUM(CASE WHEN raw_input_cost IS NULL AND raw_output_cost IS NULL " +
	"AND raw_cache_read_cost IS NULL AND raw_cache_write_cost IS NULL THEN total_cost " +
	"ELSE COALESCE(raw_input_cost, 0) + COALESCE(raw_output_cost, 0) " +
	"+ COALESCE(raw_cache_read_cost, 0) + COALESCE(raw_cache_write_cost, 0) END), 0) AS raw_cost, " +
	"COALESCE(SUM(CASE WHEN is_stream = 1 AND status = 1 AND first_response_ms > 0 THEN 1 ELSE 0 END), 0) AS stream_requests, " +
	"COALESCE(SUM(CASE WHEN is_stream = 1 AND status = 1 AND first_response_ms > 0 THEN first_response_ms ELSE 0 END), 0) AS sum_first_response_ms, " +
	"COALESCE(SUM(CASE WHEN is_stream = 1 AND status = 1 AND completion_tokens > 0 " +
	"AND duration - first_response_ms > 0 THEN duration - first_response_ms ELSE 0 END), 0) AS sum_generation_ms, " +
	"COALESCE(SUM(CASE WHEN is_stream = 1 AND status = 1 AND completion_tokens > 0 " +
	"AND duration - first_response_ms > 0 THEN completion_tokens ELSE 0 END), 0) AS sum_stream_tokens"

func findExactUsageRows(
	hourlyDB, requestDB *gorm.DB,
	r ObsRange,
	groupName, modelName, predicate string,
	args ...any,
) ([]exactUsageRow, error) {
	group, ok := exactUsageGroups[groupName]
	if !ok {
		return nil, fmt.Errorf("exact usage: unsupported group %q", groupName)
	}
	window := splitExactBillingWindow(r.Start, r.End)
	rows := make([]exactUsageRow, 0)
	if window.hasFullBuckets() {
		full, err := exactUsageRowsFromFullHours(hourlyDB, window, group, modelName, predicate, args...)
		if err != nil {
			return nil, err
		}
		rows = append(rows, full...)
	}
	for _, boundary := range window.boundaries {
		partial, err := exactUsageRowsFromBoundary(requestDB, boundary, group, modelName, predicate, args...)
		if err != nil {
			return nil, err
		}
		rows = append(rows, partial...)
	}
	return mergeExactUsageRows(rows), nil
}

func exactUsageRowsFromFullHours(
	db *gorm.DB,
	window exactBillingWindow,
	group exactUsageGroup,
	modelName, predicate string,
	args ...any,
) ([]exactUsageRow, error) {
	query := alignedHourWindow(db.Model(&models.UsageHourlyBucket{}), window.fullStart, window.fullEnd)
	query = applyExactUsageFilters(query, modelName, predicate, args...)
	var rows []exactUsageRow
	selectSQL := fmt.Sprintf("%s AS identity, %s AS id, %s AS name, %s",
		group.identitySQL, group.idSQL, group.nameSQL, exactUsageFullSums)
	err := query.Select(selectSQL).Group(group.groupBy).Scan(&rows).Error
	return rows, err
}

func exactUsageRowsFromBoundary(
	db *gorm.DB,
	boundary billingBoundary,
	group exactUsageGroup,
	modelName, predicate string,
	args ...any,
) ([]exactUsageRow, error) {
	query := db.Model(&models.UsageLog{}).
		Where("created_at >= ? AND created_at < ?", boundary.start, boundary.end)
	query = applyExactUsageFilters(query, modelName, predicate, args...)
	var rows []exactUsageRow
	selectSQL := fmt.Sprintf("%s AS identity, %s AS id, %s AS name, %s",
		group.identitySQL, group.idSQL, group.nameSQL, exactUsageBoundarySums)
	err := query.Select(selectSQL).Group(group.groupBy).Scan(&rows).Error
	return rows, err
}

func applyExactUsageFilters(query *gorm.DB, modelName, predicate string, args ...any) *gorm.DB {
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	if predicate != "" {
		query = query.Where(predicate, args...)
	}
	return query
}

func mergeExactUsageRows(rows []exactUsageRow) []exactUsageRow {
	merged := make(map[string]*exactUsageRow)
	for _, row := range rows {
		current := merged[row.Identity]
		if current == nil {
			copy := row
			merged[row.Identity] = &copy
			continue
		}
		current.Name = earlierNonEmptyName(current.Name, row.Name)
		current.Requests += row.Requests
		current.FailedCount += row.FailedCount
		current.PromptTokens += row.PromptTokens
		current.CompletionTokens += row.CompletionTokens
		current.CacheReadTokens += row.CacheReadTokens
		current.CacheWriteTokens += row.CacheWriteTokens
		current.TotalCost += row.TotalCost
		current.RawCost += row.RawCost
		current.StreamRequests += row.StreamRequests
		current.SumFirstResponseMs += row.SumFirstResponseMs
		current.SumGenerationMs += row.SumGenerationMs
		current.SumStreamTokens += row.SumStreamTokens
	}
	out := make([]exactUsageRow, 0, len(merged))
	for _, row := range merged {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity < out[j].Identity })
	return out
}

func earlierNonEmptyName(left, right string) string {
	if left == "" || (right != "" && right < left) {
		return right
	}
	return left
}

type exactHistogramMetric struct {
	table       any
	maxColumn   string
	eligibility string
	value       func(models.UsageLog) int64
	slot        func(int64) int
	estimate    func([17]int64, int64) int64
}

var exactHistogramMetrics = map[string]exactHistogramMetric{
	"ttft": {
		table: &models.UsageTTFTHistogram{}, maxColumn: "max_first_response_ms",
		eligibility: "status = 1 AND is_stream = 1 AND first_response_ms > 0",
		value:       func(log models.UsageLog) int64 { return int64(log.FirstResponseMs) },
		slot:        ttfthist.SlotIndex,
		estimate:    func(counts [17]int64, max int64) int64 { return ttfthist.EstimatePercentile(counts, 0.95, max) },
	},
	"tps": {
		table: &models.UsageTPSHistogram{}, maxColumn: "max_tps",
		eligibility: "status = 1 AND is_stream = 1 AND completion_tokens > 0 AND duration - first_response_ms > 0",
		value: func(log models.UsageLog) int64 {
			return tpshist.TokensPerSecond(int64(log.CompletionTokens), int64(log.Duration-log.FirstResponseMs))
		},
		slot:     tpshist.SlotIndex,
		estimate: func(counts [17]int64, max int64) int64 { return tpshist.EstimateP5(counts, max) },
	},
	"duration": {
		table: &models.UsageDurationHistogram{}, maxColumn: "max_duration_ms",
		eligibility: "status = 1",
		value:       func(log models.UsageLog) int64 { return int64(log.Duration) },
		slot:        durhist.SlotIndex,
		estimate:    func(counts [17]int64, max int64) int64 { return durhist.EstimatePercentile(counts, 0.95, max) },
	},
}

type exactHistogramAccumulator struct {
	counts [17]int64
	max    int64
}

func findExactPercentiles(
	hourlyDB, requestDB *gorm.DB,
	r ObsRange,
	groupName, metricName, modelName, predicate string,
	args ...any,
) (map[string]int64, error) {
	group, groupOK := exactUsageGroups[groupName]
	metric, metricOK := exactHistogramMetrics[metricName]
	if !groupOK || !metricOK {
		return nil, fmt.Errorf("exact histogram: unsupported group %q or metric %q", groupName, metricName)
	}
	window := splitExactBillingWindow(r.Start, r.End)
	merged := make(map[string]*exactHistogramAccumulator)
	if window.hasFullBuckets() {
		if err := mergeFullHourHistograms(merged, hourlyDB, window, group, metric, modelName, predicate, args...); err != nil {
			return nil, err
		}
	}
	for _, boundary := range window.boundaries {
		if err := mergeBoundaryHistogramSamples(merged, requestDB, boundary, group, metric, modelName, predicate, args...); err != nil {
			return nil, err
		}
	}
	out := make(map[string]int64, len(merged))
	for identity, histogram := range merged {
		out[identity] = metric.estimate(histogram.counts, histogram.max)
	}
	return out, nil
}

func requestFactsDB(hourlyDB *gorm.DB, candidates []*gorm.DB) *gorm.DB {
	if len(candidates) > 0 && candidates[0] != nil {
		return candidates[0]
	}
	return hourlyDB
}

func exactUintPercentiles(values map[string]int64) (map[uint]int64, error) {
	out := make(map[uint]int64, len(values))
	for identity, value := range values {
		parsed, err := strconv.ParseUint(identity, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse exact channel identity %q: %w", identity, err)
		}
		out[uint(parsed)] = value
	}
	return out, nil
}

func mergeFullHourHistograms(
	merged map[string]*exactHistogramAccumulator,
	db *gorm.DB,
	window exactBillingWindow,
	group exactUsageGroup,
	metric exactHistogramMetric,
	modelName, predicate string,
	args ...any,
) error {
	query := alignedHourWindow(db.Model(metric.table), window.fullStart, window.fullEnd)
	query = applyExactUsageFilters(query, modelName, predicate, args...)
	var rows []histGroupRowStr
	selectSQL := fmt.Sprintf("%s AS grp_key, COALESCE(MAX(%s), 0) AS max_ms, %s",
		group.identitySQL, metric.maxColumn, histSumSelectFrag)
	if err := query.Select(selectSQL).Group(group.groupBy).Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		mergeExactHistogram(merged, row.Key, row.counts17(), row.Max)
	}
	return nil
}

func mergeBoundaryHistogramSamples(
	merged map[string]*exactHistogramAccumulator,
	db *gorm.DB,
	boundary billingBoundary,
	group exactUsageGroup,
	metric exactHistogramMetric,
	modelName, predicate string,
	args ...any,
) error {
	query := db.Model(&models.UsageLog{}).
		Where("created_at >= ? AND created_at < ?", boundary.start, boundary.end).
		Where(metric.eligibility)
	query = applyExactUsageFilters(query, modelName, predicate, args...)
	var logs []models.UsageLog
	if err := query.Select("channel_id, model_name, agent_id, first_response_ms, duration, completion_tokens").Find(&logs).Error; err != nil {
		return err
	}
	for _, log := range logs {
		value := metric.value(log)
		counts := [17]int64{}
		counts[metric.slot(value)] = 1
		mergeExactHistogram(merged, group.identity(log), counts, value)
	}
	return nil
}

func mergeExactHistogram(merged map[string]*exactHistogramAccumulator, identity string, counts [17]int64, max int64) {
	histogram := merged[identity]
	if histogram == nil {
		histogram = &exactHistogramAccumulator{}
		merged[identity] = histogram
	}
	for index, count := range counts {
		histogram.counts[index] += count
	}
	if max > histogram.max {
		histogram.max = max
	}
}

type exactSparkKey struct {
	identity string
	bucket   int64
}

type exactSparkFullRow struct {
	Identity string
	Date     string
	Hour     int
	Requests int64
}

func findExactSparkSlots(
	hourlyDB, requestDB *gorm.DB,
	r ObsRange,
	groupName, predicate string,
	args ...any,
) (map[string][]int64, error) {
	group, ok := exactUsageGroups[groupName]
	if !ok {
		return nil, fmt.Errorf("exact spark: unsupported group %q", groupName)
	}
	winStart := r.End - 24*3600
	if winStart < r.Start {
		winStart = r.Start
	}
	window := splitExactBillingWindow(winStart, r.End)
	counts := make(map[exactSparkKey]int64)
	if window.hasFullBuckets() {
		query := alignedHourWindow(hourlyDB.Model(&models.UsageHourlyBucket{}), window.fullStart, window.fullEnd)
		query = applyExactUsageFilters(query, "", predicate, args...)
		var rows []exactSparkFullRow
		selectSQL := fmt.Sprintf("%s AS identity, date, hour, COALESCE(SUM(request_count), 0) AS requests", group.identitySQL)
		if err := query.Select(selectSQL).Group(group.groupBy + ", date, hour").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			bucket, _ := bucketTsLabel(row.Date, row.Hour, GranHour)
			counts[exactSparkKey{identity: row.Identity, bucket: bucket}] += row.Requests
		}
	}
	for _, boundary := range window.boundaries {
		query := requestDB.Model(&models.UsageLog{}).
			Where("created_at >= ? AND created_at < ?", boundary.start, boundary.end)
		query = applyExactUsageFilters(query, "", predicate, args...)
		var logs []models.UsageLog
		if err := query.Select("channel_id, agent_id, created_at").Find(&logs).Error; err != nil {
			return nil, err
		}
		for _, log := range logs {
			bucket := log.CreatedAt - log.CreatedAt%3600
			counts[exactSparkKey{identity: group.identity(log), bucket: bucket}]++
		}
	}
	return assembleExactSparkSlots(counts, winStart), nil
}

func assembleExactSparkSlots(counts map[exactSparkKey]int64, winStart int64) map[string][]int64 {
	out := make(map[string][]int64)
	for key, count := range counts {
		bucket := key.bucket
		if bucket < winStart {
			bucket = winStart
		}
		offset := int((bucket - winStart) / 3600)
		if offset < 0 || offset >= 24 {
			continue
		}
		if out[key.identity] == nil {
			out[key.identity] = make([]int64, 24)
		}
		out[key.identity][offset] += count
	}
	return out
}

func exactUintSparkSlots(values map[string][]int64) (map[uint][]int64, error) {
	out := make(map[uint][]int64, len(values))
	for identity, slots := range values {
		parsed, err := strconv.ParseUint(identity, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse exact spark channel identity %q: %w", identity, err)
		}
		out[uint(parsed)] = slots
	}
	return out, nil
}
