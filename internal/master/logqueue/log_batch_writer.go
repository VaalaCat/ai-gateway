package logqueue

import (
	"context"
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidLogBatch      = errors.New("invalid log batch")
	ErrLogPersistenceFailed = errors.New("log persistence failed")
)

type LogBatchWriter struct {
	DBFinder func() *gorm.DB
}

func (w *LogBatchWriter) Write(ctx context.Context, batches []LogBatch) error {
	if len(batches) == 0 {
		return nil
	}
	for i := range batches {
		if err := validateBatch(&batches[i]); err != nil {
			return fmt.Errorf("%w: batch %d: %w", ErrInvalidLogBatch, i, err)
		}
	}
	if w == nil || w.DBFinder == nil {
		return fmt.Errorf("%w: log batch writer has no database finder", ErrLogPersistenceFailed)
	}
	db := w.DBFinder()
	if db == nil {
		return fmt.Errorf("%w: log database is unavailable", ErrLogPersistenceFailed)
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range batches {
			if err := writeBatch(tx, &batches[i]); err != nil {
				return fmt.Errorf("write log batch %d: %w", i, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLogPersistenceFailed, err)
	}
	return nil
}

func validateBatch(batch *LogBatch) error {
	if batch.Request.RequestID == "" {
		return fmt.Errorf("request_id is empty")
	}
	for _, trace := range batch.Traces {
		if trace.RequestID != batch.Request.RequestID {
			return fmt.Errorf("trace request_id %q does not match request %q", trace.RequestID, batch.Request.RequestID)
		}
	}
	return nil
}

func writeBatch(tx *gorm.DB, batch *LogBatch) error {
	inserted, err := insertRequest(tx, batch.Request)
	if err != nil || !inserted {
		return err
	}
	for i := range batch.Traces {
		batch.Traces[i].ID = 0
	}
	if len(batch.Traces) > 0 {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "request_id"}, {Name: "attempt_index"}}, DoNothing: true,
		}).Create(&batch.Traces).Error; err != nil {
			return fmt.Errorf("insert request traces: %w", err)
		}
	}
	return applyLogDeltas(tx, batch)
}

func insertRequest(tx *gorm.DB, request models.RequestLog) (bool, error) {
	request.ID = 0
	result := tx.Clauses(
		clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true},
		clause.Returning{Columns: []clause.Column{{Name: "id"}}},
	).Create(&request)
	if result.Error != nil {
		return false, fmt.Errorf("insert request log: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

type deltaWriter struct {
	name string
	run  func(*gorm.DB) error
}

func applyLogDeltas(tx *gorm.DB, batch *LogBatch) error {
	writers := []deltaWriter{
		{name: "hourly buckets", run: func(db *gorm.DB) error { return upsertHourly(db, batch.Hourly) }},
		{name: "duration histograms", run: func(db *gorm.DB) error { return upsertDuration(db, batch.Duration) }},
		{name: "TTFT histograms", run: func(db *gorm.DB) error { return upsertTTFT(db, batch.TTFT) }},
		{name: "TPS histograms", run: func(db *gorm.DB) error { return upsertTPS(db, batch.TPS) }},
		{name: "user TTFT histograms", run: func(db *gorm.DB) error { return upsertUserTTFT(db, batch.UserTTFT) }},
		{name: "user TPS histograms", run: func(db *gorm.DB) error { return upsertUserTPS(db, batch.UserTPS) }},
	}
	for _, write := range writers {
		if err := write.run(tx); err != nil {
			return fmt.Errorf("upsert %s: %w", write.name, err)
		}
	}
	return nil
}

var logDimensions = []clause.Column{
	{Name: "date"}, {Name: "hour"}, {Name: "channel_id"},
	{Name: "private_channel_id"}, {Name: "model_name"}, {Name: "agent_id"},
}

var userDimensions = []clause.Column{
	{Name: "date"}, {Name: "hour"}, {Name: "user_id"}, {Name: "model_name"},
}

var histogramColumns = []string{
	"h0", "h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8",
	"h9", "h10", "h11", "h12", "h13", "h14", "h15", "h16",
}

func accumulatingAssignments(values map[string]any, columns []string) map[string]any {
	assignments := make(map[string]any, len(columns))
	for _, column := range columns {
		assignments[column] = gorm.Expr(column+" + ?", values[column])
	}
	return assignments
}

func upsertRows[T any](tx *gorm.DB, rows []T, dimensions []clause.Column, assignments func(T) map[string]any) error {
	for i := range rows {
		if err := tx.Clauses(clause.OnConflict{
			Columns: dimensions, DoUpdates: clause.Assignments(assignments(rows[i])),
		}).Create(&rows[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func histogramValues(values [17]int64) map[string]any {
	out := make(map[string]any, len(histogramColumns))
	for i, column := range histogramColumns {
		out[column] = values[i]
	}
	return out
}

func histogramAssignments(values [17]int64, maxColumn string, maxValue, updatedAt int64) map[string]any {
	assignments := accumulatingAssignments(histogramValues(values), histogramColumns)
	assignments[maxColumn] = gorm.Expr("CASE WHEN "+maxColumn+" >= ? THEN "+maxColumn+" ELSE ? END", maxValue, maxValue)
	assignments["updated_at"] = updatedAt
	return assignments
}

var hourlyCounterColumns = []string{
	"request_count", "success_count", "failed_count", "prompt_tokens", "completion_tokens",
	"cache_read_tokens", "cache_write_tokens", "input_cost", "output_cost", "total_cost", "raw_cost",
	"stream_request_count", "sum_first_response_ms", "sum_generation_ms", "sum_stream_completion_tokens",
	"sum_inbound_decode_ms", "sum_upstream_dispatch_ms", "sum_upstream_decode_ms",
	"sum_outbound_encode_ms", "sum_client_encode_ms",
}

func upsertHourly(tx *gorm.DB, rows []models.UsageHourlyBucket) error {
	return upsertRows(tx, rows, logDimensions, func(row models.UsageHourlyBucket) map[string]any {
		values := map[string]any{
			"request_count": row.RequestCount, "success_count": row.SuccessCount, "failed_count": row.FailedCount,
			"prompt_tokens": row.PromptTokens, "completion_tokens": row.CompletionTokens,
			"cache_read_tokens": row.CacheReadTokens, "cache_write_tokens": row.CacheWriteTokens,
			"input_cost": row.InputCost, "output_cost": row.OutputCost, "total_cost": row.TotalCost, "raw_cost": row.RawCost,
			"stream_request_count": row.StreamRequestCount, "sum_first_response_ms": row.SumFirstResponseMs,
			"sum_generation_ms": row.SumGenerationMs, "sum_stream_completion_tokens": row.SumStreamCompletionTokens,
			"sum_inbound_decode_ms": row.SumInboundDecodeMs, "sum_upstream_dispatch_ms": row.SumUpstreamDispatchMs,
			"sum_upstream_decode_ms": row.SumUpstreamDecodeMs, "sum_outbound_encode_ms": row.SumOutboundEncodeMs,
			"sum_client_encode_ms": row.SumClientEncodeMs,
		}
		assignments := accumulatingAssignments(values, hourlyCounterColumns)
		assignments["owner_type"] = row.OwnerType
		assignments["channel_name"] = row.ChannelName
		assignments["channel_type"] = row.ChannelType
		assignments["last_used_at"] = gorm.Expr("CASE WHEN last_used_at >= ? THEN last_used_at ELSE ? END", row.LastUsedAt, row.LastUsedAt)
		assignments["updated_at"] = row.UpdatedAt
		return assignments
	})
}

func upsertDuration(tx *gorm.DB, rows []models.UsageDurationHistogram) error {
	return upsertRows(tx, rows, logDimensions, func(row models.UsageDurationHistogram) map[string]any {
		return histogramAssignments(histogramArray(row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8, row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16), "max_duration_ms", row.MaxDurationMs, row.UpdatedAt)
	})
}

func upsertTTFT(tx *gorm.DB, rows []models.UsageTTFTHistogram) error {
	return upsertRows(tx, rows, logDimensions, func(row models.UsageTTFTHistogram) map[string]any {
		return histogramAssignments(histogramArray(row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8, row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16), "max_first_response_ms", row.MaxFirstResponseMs, row.UpdatedAt)
	})
}

func upsertTPS(tx *gorm.DB, rows []models.UsageTPSHistogram) error {
	return upsertRows(tx, rows, logDimensions, func(row models.UsageTPSHistogram) map[string]any {
		return histogramAssignments(histogramArray(row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8, row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16), "max_tps", row.MaxTps, row.UpdatedAt)
	})
}

func upsertUserTTFT(tx *gorm.DB, rows []models.UsageUserTTFTHistogram) error {
	return upsertRows(tx, rows, userDimensions, func(row models.UsageUserTTFTHistogram) map[string]any {
		return histogramAssignments(histogramArray(row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8, row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16), "max_first_response_ms", row.MaxFirstResponseMs, row.UpdatedAt)
	})
}

func upsertUserTPS(tx *gorm.DB, rows []models.UsageUserTPSHistogram) error {
	return upsertRows(tx, rows, userDimensions, func(row models.UsageUserTPSHistogram) map[string]any {
		return histogramAssignments(histogramArray(row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8, row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16), "max_tps", row.MaxTps, row.UpdatedAt)
	})
}

func histogramArray(values ...int64) [17]int64 {
	var out [17]int64
	copy(out[:], values)
	return out
}
