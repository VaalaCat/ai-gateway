package historybackfill

import (
	"context"
	"fmt"
	"reflect"
	"time"

	masterbilling "github.com/VaalaCat/ai-gateway/internal/master/billing"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BillingBatchWriter struct {
	DBFinder func() *gorm.DB
}

func (w *BillingBatchWriter) WriteHistory(ctx context.Context, cursorKey string, lastSourceID uint, rows []models.BillingLog) error {
	return w.writeHistoryWithHook(ctx, cursorKey, lastSourceID, rows, nil)
}

func (w *BillingBatchWriter) writeHistoryWithHook(
	ctx context.Context,
	cursorKey string,
	lastSourceID uint,
	rows []models.BillingLog,
	beforeCursor func() error,
) error {
	if ctx == nil {
		return fmt.Errorf("historical billing context is nil")
	}
	if cursorKey == "" {
		return fmt.Errorf("historical billing cursor key is empty")
	}
	if w == nil || w.DBFinder == nil {
		return fmt.Errorf("historical billing database is unavailable")
	}
	db := w.DBFinder()
	if db == nil {
		return fmt.Errorf("historical billing database is unavailable")
	}
	clean := append([]models.BillingLog(nil), rows...)
	unique := make([]models.BillingLog, 0, len(clean))
	seen := make(map[string]models.BillingLog, len(clean))
	for i := range clean {
		if clean[i].RequestID == "" {
			return fmt.Errorf("historical billing row %d has empty request_id", i)
		}
		clean[i].ID = 0
		if previous, exists := seen[clean[i].RequestID]; exists {
			if !reflect.DeepEqual(previous, clean[i]) {
				return fmt.Errorf("historical billing row %d has conflicting duplicate request_id %q", i, clean[i].RequestID)
			}
			continue
		}
		seen[clean[i].RequestID] = clean[i]
		unique = append(unique, clean[i])
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		novel := unique
		if len(unique) > 0 {
			requestIDs := make([]string, len(unique))
			for i := range unique {
				requestIDs[i] = unique[i].RequestID
			}
			var existing []string
			if err := tx.Model(&models.BillingLog{}).Where("request_id IN ?", requestIDs).Pluck("request_id", &existing).Error; err != nil {
				return fmt.Errorf("find existing historical billing: %w", err)
			}
			var receipts []string
			if err := tx.Model(&models.BillingProjectionReceipt{}).Where("request_id IN ?", requestIDs).Pluck("request_id", &receipts).Error; err != nil {
				return fmt.Errorf("find existing billing projection receipts: %w", err)
			}
			existingSet := make(map[string]struct{}, len(existing))
			for _, requestID := range existing {
				existingSet[requestID] = struct{}{}
			}
			for _, requestID := range receipts {
				if _, exists := existingSet[requestID]; !exists {
					return fmt.Errorf("billing projection receipt %q has no committed fact", requestID)
				}
			}
			if len(existing) > 0 {
				novel = make([]models.BillingLog, 0, len(unique)-len(existingSet))
				for i := range unique {
					if _, exists := existingSet[unique[i].RequestID]; !exists {
						novel = append(novel, unique[i])
					}
				}
			}
		}
		if len(novel) > 0 {
			inserted, err := masterdatabase.CreateInSafeBatchesCount(ctx, tx, novel, clause.OnConflict{
				Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true,
			})
			if err != nil {
				return fmt.Errorf("insert historical billing: %w", err)
			}
			if inserted != int64(len(novel)) {
				return fmt.Errorf("insert historical billing: inserted %d of %d new facts", inserted, len(novel))
			}
			for i := range novel {
				if err := masterbilling.RegisterPendingBillingProjectionInTx(ctx, tx, &novel[i]); err != nil {
					return fmt.Errorf("register historical billing projection: %w", err)
				}
			}
		}
		if err := masterbilling.ProjectCommittedBillingFactsInTx(ctx, tx, unique); err != nil {
			return fmt.Errorf("project historical billing: %w", err)
		}
		if beforeCursor != nil {
			if err := beforeCursor(); err != nil {
				return err
			}
		}
		return advanceHistoryCursor(tx, cursorKey, lastSourceID, int64(len(clean)))
	})
}

func advanceHistoryCursor(tx *gorm.DB, key string, lastSourceID uint, processed int64) error {
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
		Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{
			"last_source_id", "processed_rows", "skipped", "updated_at_unix",
		}),
	}).Create(&cursor).Error; err != nil {
		return fmt.Errorf("advance history cursor %q: %w", key, err)
	}
	return nil
}

func (w *BillingBatchWriter) WriteBilling(ctx context.Context, source []models.UsageLog) error {
	if len(source) == 0 {
		return nil
	}
	if w == nil || w.DBFinder == nil {
		return fmt.Errorf("historical billing database is unavailable")
	}
	db := w.DBFinder()
	if db == nil {
		return fmt.Errorf("historical billing database is unavailable")
	}
	rows := make([]models.BillingLog, len(source))
	ids := make([]uint, len(source))
	requestIDs := make([]string, len(source))
	for i := range source {
		if source[i].ID == 0 || source[i].RequestID == "" {
			return fmt.Errorf("historical billing row %d has invalid identity", i)
		}
		rows[i] = billingLogFromUsage(source[i])
		ids[i], requestIDs[i] = rows[i].ID, rows[i].RequestID
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := masterdatabase.CreateInSafeBatches(ctx, tx, rows, clause.OnConflict{DoNothing: true}); err != nil {
			return fmt.Errorf("insert historical billing: %w", err)
		}
		var stored []models.BillingLog
		if err := tx.Where("id IN ? OR request_id IN ?", ids, requestIDs).Find(&stored).Error; err != nil {
			return fmt.Errorf("verify historical billing: %w", err)
		}
		byID := make(map[uint]models.BillingLog, len(stored))
		byRequestID := make(map[string]models.BillingLog, len(stored))
		for _, row := range stored {
			byID[row.ID], byRequestID[row.RequestID] = row, row
		}
		for _, expected := range rows {
			byPrimary, idOK := byID[expected.ID]
			byRequest, requestOK := byRequestID[expected.RequestID]
			if !idOK || !requestOK || byPrimary.ID != byRequest.ID || !reflect.DeepEqual(expected, byPrimary) {
				return fmt.Errorf("historical billing conflict: id=%d request_id=%q", expected.ID, expected.RequestID)
			}
		}
		return nil
	})
}

func billingLogFromUsage(row models.UsageLog) models.BillingLog {
	return models.BillingLog{
		ID: row.ID, RequestID: row.RequestID, UserID: row.UserID, TokenID: row.TokenID,
		TokenName: row.TokenName, ChannelID: row.ChannelID, PrivateChannelID: row.PrivateChannelID,
		OwnerType: row.OwnerType, ChannelName: row.ChannelName, ChannelType: row.ChannelType,
		ModelName: row.ModelName, PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
		CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		InputCost: row.InputCost, OutputCost: row.OutputCost, CacheReadCost: row.CacheReadCost,
		CacheWriteCost: row.CacheWriteCost, TotalCost: row.TotalCost, RawInputCost: row.RawInputCost,
		RawOutputCost: row.RawOutputCost, RawCacheReadCost: row.RawCacheReadCost,
		RawCacheWriteCost: row.RawCacheWriteCost, BillingFactor: row.BillingFactor,
		PriceRatio: row.PriceRatio, Free: row.Free, Status: row.Status, CreatedAt: row.CreatedAt,
	}
}
