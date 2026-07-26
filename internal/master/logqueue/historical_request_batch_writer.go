package logqueue

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type HistoricalRequestBatchWriter struct {
	DBFinder func() *gorm.DB
}

func (w *HistoricalRequestBatchWriter) WriteRequests(
	ctx context.Context, cursorKey string, lastSourceID uint, rows []models.RequestLog,
) error {
	return w.writeRequestsWithHook(ctx, cursorKey, lastSourceID, rows, nil)
}

func (w *HistoricalRequestBatchWriter) writeRequestsWithHook(
	ctx context.Context,
	cursorKey string,
	lastSourceID uint,
	rows []models.RequestLog,
	beforeCursor func() error,
) error {
	db, err := w.historyDB(ctx, cursorKey)
	if err != nil {
		return err
	}
	clean := append([]models.RequestLog(nil), rows...)
	for i := range clean {
		if clean[i].RequestID == "" {
			return fmt.Errorf("historical request %d has empty request_id", i)
		}
		clean[i].ID = 0
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range clean {
			inserted, err := insertRequest(tx, clean[i])
			if err != nil {
				return fmt.Errorf("insert historical request %d: %w", i, err)
			}
			if inserted {
				batch := BuildRequestAggregateBatch(models.UsageLog(clean[i]))
				if err := applyLogDeltas(tx, &batch); err != nil {
					return fmt.Errorf("rebuild historical request aggregates: %w", err)
				}
			}
		}
		if beforeCursor != nil {
			if err := beforeCursor(); err != nil {
				return err
			}
		}
		return advanceHistoricalCursor(tx, cursorKey, lastSourceID, int64(len(clean)))
	})
}

func (w *HistoricalRequestBatchWriter) WriteTraces(
	ctx context.Context, cursorKey string, lastSourceID uint, rows []models.RequestTrace,
) error {
	return w.writeTracesWithHook(ctx, cursorKey, lastSourceID, rows, nil)
}

func (w *HistoricalRequestBatchWriter) writeTracesWithHook(
	ctx context.Context,
	cursorKey string,
	lastSourceID uint,
	rows []models.RequestTrace,
	beforeCursor func() error,
) error {
	db, err := w.historyDB(ctx, cursorKey)
	if err != nil {
		return err
	}
	clean := append([]models.RequestTrace(nil), rows...)
	for i := range clean {
		if clean[i].RequestID == "" {
			return fmt.Errorf("historical trace %d has empty request_id", i)
		}
		clean[i].ID = 0
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(clean) > 0 {
			if err := masterdatabase.CreateInSafeBatches(ctx, tx, clean, clause.OnConflict{
				Columns: []clause.Column{{Name: "request_id"}, {Name: "attempt_index"}}, DoNothing: true,
			}); err != nil {
				return fmt.Errorf("insert historical traces: %w", err)
			}
		}
		if beforeCursor != nil {
			if err := beforeCursor(); err != nil {
				return err
			}
		}
		return advanceHistoricalCursor(tx, cursorKey, lastSourceID, int64(len(clean)))
	})
}

func (w *HistoricalRequestBatchWriter) historyDB(ctx context.Context, cursorKey string) (*gorm.DB, error) {
	if ctx == nil {
		return nil, fmt.Errorf("historical request context is nil")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if cursorKey == "" {
		return nil, fmt.Errorf("historical request cursor key is empty")
	}
	if w == nil || w.DBFinder == nil {
		return nil, fmt.Errorf("historical request database is unavailable")
	}
	db := w.DBFinder()
	if db == nil {
		return nil, fmt.Errorf("historical request database is unavailable")
	}
	return db, nil
}

func advanceHistoricalCursor(tx *gorm.DB, key string, lastSourceID uint, processed int64) error {
	var previous models.HistoryCursor
	err := tx.Where("key = ?", key).First(&previous).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("read history cursor %q: %w", key, err)
	}
	if previous.LastSourceID > lastSourceID {
		lastSourceID = previous.LastSourceID
	}
	cursor := models.HistoryCursor{
		Key: key, LastSourceID: lastSourceID, ProcessedRows: previous.ProcessedRows + processed,
		Skipped: previous.Skipped, UpdatedAtUnix: time.Now().Unix(),
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_source_id", "processed_rows", "skipped", "updated_at_unix"}),
	}).Create(&cursor).Error; err != nil {
		return fmt.Errorf("advance history cursor %q: %w", key, err)
	}
	return nil
}

type HistoricalAggregateBatch struct {
	Hourly   []models.UsageHourlyBucket
	Duration []models.UsageDurationHistogram
	TTFT     []models.UsageTTFTHistogram
	TPS      []models.UsageTPSHistogram
	UserTTFT []models.UsageUserTTFTHistogram
	UserTPS  []models.UsageUserTPSHistogram
}

func (w *HistoricalRequestBatchWriter) WriteHistory(ctx context.Context, requests []models.RequestLog, traces []models.RequestTrace) error {
	if len(requests) == 0 && len(traces) == 0 {
		return nil
	}
	if w == nil || w.DBFinder == nil {
		return fmt.Errorf("historical request database is unavailable")
	}
	db := w.DBFinder()
	if db == nil {
		return fmt.Errorf("historical request database is unavailable")
	}
	for i := range requests {
		if requests[i].ID == 0 || requests[i].RequestID == "" {
			return fmt.Errorf("historical request %d has invalid identity", i)
		}
	}
	for i := range traces {
		if traces[i].ID == 0 || traces[i].RequestID == "" {
			return fmt.Errorf("historical trace %d has invalid identity", i)
		}
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(requests) > 0 {
			if err := masterdatabase.CreateInSafeBatches(ctx, tx, requests, clause.OnConflict{DoNothing: true}); err != nil {
				return fmt.Errorf("insert historical requests: %w", err)
			}
			if err := verifyHistoricalRequests(tx, requests); err != nil {
				return err
			}
		}
		if len(traces) > 0 {
			if err := masterdatabase.CreateInSafeBatches(ctx, tx, traces, clause.OnConflict{DoNothing: true}); err != nil {
				return fmt.Errorf("insert historical traces: %w", err)
			}
			if err := verifyHistoricalTraces(tx, traces); err != nil {
				return err
			}
		}
		return nil
	})
}

const historicalVerificationBatchSize = 400

type historicalRequestIdentity struct {
	ID        uint
	RequestID string
}

type historicalTraceIdentity struct {
	ID           uint
	RequestID    string
	AttemptIndex int
}

func verifyHistoricalRequests(tx *gorm.DB, expected []models.RequestLog) error {
	ids, requestIDs := historicalIdentities(expected, func(row models.RequestLog) (uint, string) { return row.ID, row.RequestID })
	stored, err := findHistoricalRequestIdentities(tx, ids, requestIDs)
	if err != nil {
		return fmt.Errorf("verify historical requests: %w", err)
	}
	byID := make(map[uint]string, len(stored))
	byRequestID := make(map[string]uint, len(stored))
	for _, row := range stored {
		byID[row.ID], byRequestID[row.RequestID] = row.RequestID, row.ID
	}
	for _, row := range expected {
		requestID, idOK := byID[row.ID]
		id, requestOK := byRequestID[row.RequestID]
		if !idOK || !requestOK || requestID != row.RequestID || id != row.ID {
			return fmt.Errorf("historical request conflict: id=%d request_id=%q", row.ID, row.RequestID)
		}
	}
	return nil
}

func verifyHistoricalTraces(tx *gorm.DB, expected []models.RequestTrace) error {
	ids, requestIDs := historicalIdentities(expected, func(row models.RequestTrace) (uint, string) { return row.ID, row.RequestID })
	stored, err := findHistoricalTraceIdentities(tx, ids, requestIDs)
	if err != nil {
		return fmt.Errorf("verify historical traces: %w", err)
	}
	byID := make(map[uint]string, len(stored))
	byBusiness := make(map[string]uint, len(stored))
	for _, row := range stored {
		businessKey := traceBusinessKey(row.RequestID, row.AttemptIndex)
		byID[row.ID], byBusiness[businessKey] = businessKey, row.ID
	}
	for _, row := range expected {
		businessKey := traceBusinessKey(row.RequestID, row.AttemptIndex)
		storedKey, idOK := byID[row.ID]
		storedID, keyOK := byBusiness[businessKey]
		if !idOK || !keyOK || storedKey != businessKey || storedID != row.ID {
			return fmt.Errorf("historical trace conflict: id=%d request_id=%q attempt_index=%d", row.ID, row.RequestID, row.AttemptIndex)
		}
	}
	return nil
}

func historicalIdentities[T any](rows []T, identity func(T) (uint, string)) ([]uint, []string) {
	ids := make([]uint, len(rows))
	requestIDs := make([]string, len(rows))
	for i, row := range rows {
		ids[i], requestIDs[i] = identity(row)
	}
	return ids, requestIDs
}

func findHistoricalRequestIdentities(tx *gorm.DB, ids []uint, requestIDs []string) ([]historicalRequestIdentity, error) {
	stored := make([]historicalRequestIdentity, 0, len(ids))
	for start := 0; start < len(ids); start += historicalVerificationBatchSize {
		end := min(start+historicalVerificationBatchSize, len(ids))
		var rows []historicalRequestIdentity
		if err := tx.Model(&models.RequestLog{}).Select("id", "request_id").
			Where("id IN ? OR request_id IN ?", ids[start:end], requestIDs[start:end]).
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		stored = append(stored, rows...)
	}
	return stored, nil
}

func findHistoricalTraceIdentities(tx *gorm.DB, ids []uint, requestIDs []string) ([]historicalTraceIdentity, error) {
	stored := make([]historicalTraceIdentity, 0, len(ids))
	for start := 0; start < len(ids); start += historicalVerificationBatchSize {
		end := min(start+historicalVerificationBatchSize, len(ids))
		var rows []historicalTraceIdentity
		if err := tx.Model(&models.RequestTrace{}).Select("id", "request_id", "attempt_index").
			Where("id IN ? OR request_id IN ?", ids[start:end], requestIDs[start:end]).
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		stored = append(stored, rows...)
	}
	return stored, nil
}

func traceBusinessKey(requestID string, attemptIndex int) string {
	return requestID + "\x00" + strconv.Itoa(attemptIndex)
}

func (w *HistoricalRequestBatchWriter) ReplaceAggregates(ctx context.Context, batch HistoricalAggregateBatch) error {
	if len(batch.Hourly)+len(batch.Duration)+len(batch.TTFT)+len(batch.TPS)+len(batch.UserTTFT)+len(batch.UserTPS) == 0 {
		return nil
	}
	if w == nil || w.DBFinder == nil {
		return fmt.Errorf("historical aggregate database is unavailable")
	}
	db := w.DBFinder()
	if db == nil {
		return fmt.Errorf("historical aggregate database is unavailable")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range batch.Hourly {
			batch.Hourly[i].ID = 0
		}
		for i := range batch.Duration {
			batch.Duration[i].ID = 0
		}
		for i := range batch.TTFT {
			batch.TTFT[i].ID = 0
		}
		for i := range batch.TPS {
			batch.TPS[i].ID = 0
		}
		for i := range batch.UserTTFT {
			batch.UserTTFT[i].ID = 0
		}
		for i := range batch.UserTPS {
			batch.UserTPS[i].ID = 0
		}
		for _, write := range []func() error{
			func() error { return replaceHistoricalAggregates(tx, batch.Hourly, logDimensions) },
			func() error { return replaceHistoricalAggregates(tx, batch.Duration, logDimensions) },
			func() error { return replaceHistoricalAggregates(tx, batch.TTFT, logDimensions) },
			func() error { return replaceHistoricalAggregates(tx, batch.TPS, logDimensions) },
			func() error { return replaceHistoricalAggregates(tx, batch.UserTTFT, userDimensions) },
			func() error { return replaceHistoricalAggregates(tx, batch.UserTPS, userDimensions) },
		} {
			if err := write(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *HistoricalRequestBatchWriter) MergeAggregates(ctx context.Context, batch HistoricalAggregateBatch) error {
	if len(batch.Hourly)+len(batch.Duration)+len(batch.TTFT)+len(batch.TPS)+len(batch.UserTTFT)+len(batch.UserTPS) == 0 {
		return nil
	}
	if w == nil || w.DBFinder == nil || w.DBFinder() == nil {
		return fmt.Errorf("historical aggregate database is unavailable")
	}
	db := w.DBFinder()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range batch.Hourly {
			key := aggregateSourceKey(batch.Hourly[i].ID, batch.Hourly[i].Date, batch.Hourly[i].Hour, batch.Hourly[i].ChannelID, batch.Hourly[i].PrivateChannelID, batch.Hourly[i].ModelName, batch.Hourly[i].AgentID)
			batch.Hourly[i].ID = 0
			if err := mergeHistoricalAggregate(tx, "usage_hourly_buckets", key, &batch.Hourly[i], logDimensions, hourlyAdditiveColumns, nil); err != nil {
				return err
			}
		}
		for i := range batch.Duration {
			key := aggregateSourceKey(batch.Duration[i].ID, batch.Duration[i].Date, batch.Duration[i].Hour, batch.Duration[i].ChannelID, batch.Duration[i].PrivateChannelID, batch.Duration[i].ModelName, batch.Duration[i].AgentID)
			batch.Duration[i].ID = 0
			if err := mergeHistoricalAggregate(tx, "usage_duration_histograms", key, &batch.Duration[i], logDimensions, historicalHistogramColumns, []string{"max_duration_ms"}); err != nil {
				return err
			}
		}
		for i := range batch.TTFT {
			key := aggregateSourceKey(batch.TTFT[i].ID, batch.TTFT[i].Date, batch.TTFT[i].Hour, batch.TTFT[i].ChannelID, batch.TTFT[i].PrivateChannelID, batch.TTFT[i].ModelName, batch.TTFT[i].AgentID)
			batch.TTFT[i].ID = 0
			if err := mergeHistoricalAggregate(tx, "usage_ttft_histograms", key, &batch.TTFT[i], logDimensions, historicalHistogramColumns, []string{"max_first_response_ms"}); err != nil {
				return err
			}
		}
		for i := range batch.TPS {
			key := aggregateSourceKey(batch.TPS[i].ID, batch.TPS[i].Date, batch.TPS[i].Hour, batch.TPS[i].ChannelID, batch.TPS[i].PrivateChannelID, batch.TPS[i].ModelName, batch.TPS[i].AgentID)
			batch.TPS[i].ID = 0
			if err := mergeHistoricalAggregate(tx, "usage_tps_histograms", key, &batch.TPS[i], logDimensions, historicalHistogramColumns, []string{"max_tps"}); err != nil {
				return err
			}
		}
		for i := range batch.UserTTFT {
			key := aggregateSourceKey(batch.UserTTFT[i].ID, batch.UserTTFT[i].Date, batch.UserTTFT[i].Hour, batch.UserTTFT[i].UserID, batch.UserTTFT[i].ModelName)
			batch.UserTTFT[i].ID = 0
			if err := mergeHistoricalAggregate(tx, "usage_user_ttft_histograms", key, &batch.UserTTFT[i], userDimensions, historicalHistogramColumns, []string{"max_first_response_ms"}); err != nil {
				return err
			}
		}
		for i := range batch.UserTPS {
			key := aggregateSourceKey(batch.UserTPS[i].ID, batch.UserTPS[i].Date, batch.UserTPS[i].Hour, batch.UserTPS[i].UserID, batch.UserTPS[i].ModelName)
			batch.UserTPS[i].ID = 0
			if err := mergeHistoricalAggregate(tx, "usage_user_tps_histograms", key, &batch.UserTPS[i], userDimensions, historicalHistogramColumns, []string{"max_tps"}); err != nil {
				return err
			}
		}
		return nil
	})
}

var hourlyAdditiveColumns = []string{
	"request_count", "success_count", "failed_count", "prompt_tokens", "completion_tokens", "cache_read_tokens", "cache_write_tokens",
	"input_cost", "output_cost", "total_cost", "raw_cost", "stream_request_count", "sum_first_response_ms", "sum_generation_ms",
	"sum_stream_completion_tokens", "sum_inbound_decode_ms", "sum_upstream_dispatch_ms", "sum_upstream_decode_ms", "sum_outbound_encode_ms", "sum_client_encode_ms",
}

var historicalHistogramColumns = []string{"h0", "h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8", "h9", "h10", "h11", "h12", "h13", "h14", "h15", "h16"}

func mergeHistoricalAggregate[T any](tx *gorm.DB, sourceTable, sourceKey string, row *T, dimensions []clause.Column, additive, maxima []string) error {
	marker := models.LogHistoryAggregateMerge{SourceTable: sourceTable, SourceKey: sourceKey}
	result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_table"}, {Name: "source_key"}}, DoNothing: true}).Create(&marker)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	assignments := map[string]any{}
	for _, column := range additive {
		assignments[column] = gorm.Expr(column + " + excluded." + column)
	}
	for _, column := range maxima {
		assignments[column] = gorm.Expr("MAX(" + column + ", excluded." + column + ")")
	}
	return tx.Clauses(clause.OnConflict{Columns: dimensions, DoUpdates: clause.Assignments(assignments)}).Create(row).Error
}

func aggregateSourceKey(id uint, values ...any) string {
	parts := make([]string, 0, len(values)+1)
	if id != 0 {
		parts = append(parts, strconv.FormatUint(uint64(id), 10))
	}
	for _, value := range values {
		parts = append(parts, fmt.Sprint(value))
	}
	return strings.Join(parts, "\x1f")
}

func replaceHistoricalAggregates[T any](tx *gorm.DB, rows []T, dimensions []clause.Column) error {
	if len(rows) == 0 {
		return nil
	}
	return masterdatabase.CreateInSafeBatches(tx.Statement.Context, tx, rows, clause.OnConflict{Columns: dimensions, UpdateAll: true})
}
