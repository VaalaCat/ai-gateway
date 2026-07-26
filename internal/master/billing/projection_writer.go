package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

const pendingProjectionBatchSize = 256

func RegisterPendingBillingProjectionInTx(ctx context.Context, tx *gorm.DB, fact *models.BillingLog) error {
	if ctx == nil || tx == nil || fact == nil {
		return fmt.Errorf("register pending billing projection: invalid input")
	}
	if fact.ID == 0 || fact.RequestID == "" {
		return fmt.Errorf("register pending billing projection: invalid fact identity")
	}
	receipt := models.BillingProjectionReceipt{
		RequestID: fact.RequestID, BillingLogID: fact.ID, State: models.BillingProjectionPending,
	}
	if err := tx.WithContext(ctx).Create(&receipt).Error; err != nil {
		return fmt.Errorf("register pending billing projection: %w", err)
	}
	return nil
}

func LoadPendingBillingFacts(ctx context.Context, db *gorm.DB, limit int) ([]models.BillingLog, error) {
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("load pending billing facts: invalid input")
	}
	if limit <= 0 {
		limit = pendingProjectionBatchSize
	}
	var receipts []models.BillingProjectionReceipt
	if err := db.WithContext(ctx).Where("state = ?", models.BillingProjectionPending).
		Order("billing_log_id ASC").Limit(limit).Find(&receipts).Error; err != nil {
		return nil, fmt.Errorf("load pending billing receipts: %w", err)
	}
	if len(receipts) == 0 {
		return nil, nil
	}
	ids := make([]uint, len(receipts))
	for i := range receipts {
		ids[i] = receipts[i].BillingLogID
	}
	var facts []models.BillingLog
	if err := db.WithContext(ctx).Where("id IN ?", ids).Find(&facts).Error; err != nil {
		return nil, fmt.Errorf("load pending committed facts: %w", err)
	}
	byID := make(map[uint]models.BillingLog, len(facts))
	for _, fact := range facts {
		byID[fact.ID] = fact
	}
	ordered := make([]models.BillingLog, 0, len(receipts))
	for _, receipt := range receipts {
		fact, ok := byID[receipt.BillingLogID]
		if !ok || fact.RequestID != receipt.RequestID {
			return nil, fmt.Errorf("billing projection receipt %q has no committed fact", receipt.RequestID)
		}
		ordered = append(ordered, fact)
	}
	return ordered, nil
}

// ProjectCommittedBillingFactsInTx applies each committed fact without a
// durable receipt exactly once. The stored BillingLog is authoritative when a
// legacy row races with a live settlement using the same request ID.
func ProjectCommittedBillingFactsInTx(ctx context.Context, tx *gorm.DB, candidates []models.BillingLog) error {
	if ctx == nil {
		return fmt.Errorf("project committed billing facts: nil context")
	}
	if tx == nil {
		return fmt.Errorf("project committed billing facts: nil database")
	}
	if len(candidates) == 0 {
		return nil
	}
	requestIDs := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for i := range candidates {
		requestID := candidates[i].RequestID
		if requestID == "" {
			return fmt.Errorf("project committed billing fact %d: empty request_id", i)
		}
		if _, exists := seen[requestID]; exists {
			continue
		}
		seen[requestID] = struct{}{}
		requestIDs = append(requestIDs, requestID)
	}

	var receipts []models.BillingProjectionReceipt
	if err := tx.WithContext(ctx).Where("request_id IN ?", requestIDs).Find(&receipts).Error; err != nil {
		return fmt.Errorf("read billing projection receipts: %w", err)
	}
	receipted := make(map[string]models.BillingProjectionReceipt, len(receipts))
	for _, receipt := range receipts {
		receipted[receipt.RequestID] = receipt
	}
	var stored []models.BillingLog
	if err := tx.WithContext(ctx).Where("request_id IN ?", requestIDs).Find(&stored).Error; err != nil {
		return fmt.Errorf("read committed billing facts: %w", err)
	}
	storedByRequestID := make(map[string]models.BillingLog, len(stored))
	for _, fact := range stored {
		storedByRequestID[fact.RequestID] = fact
	}

	var baseline models.BillingProjectionBaseline
	if err := tx.WithContext(ctx).First(&baseline, models.BillingProjectionBaselineID).Error; err != nil {
		return fmt.Errorf("read billing projection baseline: %w", err)
	}
	eligible := make([]models.BillingLog, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		fact, exists := storedByRequestID[requestID]
		if !exists {
			if _, hasReceipt := receipted[requestID]; hasReceipt {
				return fmt.Errorf("billing projection receipt %q has no committed fact", requestID)
			}
			return fmt.Errorf("billing projection candidate %q has no committed fact", requestID)
		}
		receipt, hasReceipt := receipted[requestID]
		if !hasReceipt {
			if fact.ID <= baseline.BillingLogHighWatermark {
				continue
			}
			return fmt.Errorf("billing projection candidate %q has no durable receipt", requestID)
		}
		if receipt.BillingLogID != fact.ID {
			return fmt.Errorf("billing projection receipt %q references fact %d, want %d", requestID, receipt.BillingLogID, fact.ID)
		}
		switch receipt.State {
		case models.BillingProjectionApplied:
			continue
		case models.BillingProjectionPending:
			eligible = append(eligible, fact)
		default:
			return fmt.Errorf("billing projection receipt %q has invalid state %q", requestID, receipt.State)
		}
	}
	if len(eligible) == 0 {
		return nil
	}

	batch := BuildBillingAggregateBatch(eligible)
	if err := dao.UpsertCoreBillingRowsInTx(ctx, tx, batch.Tokens, batch.Channels, batch.Hourly); err != nil {
		return fmt.Errorf("upsert billing projections: %w", err)
	}
	projectedAt := time.Now().Unix()
	eligibleIDs := make([]string, len(eligible))
	for i := range eligible {
		eligibleIDs[i] = eligible[i].RequestID
	}
	result := tx.WithContext(ctx).Model(&models.BillingProjectionReceipt{}).
		Where("request_id IN ? AND state = ?", eligibleIDs, models.BillingProjectionPending).
		Updates(map[string]any{"state": models.BillingProjectionApplied, "projected_at_unix": projectedAt})
	if result.Error != nil {
		return fmt.Errorf("apply billing projection receipts: %w", result.Error)
	}
	if result.RowsAffected != int64(len(eligibleIDs)) {
		return fmt.Errorf("apply billing projection receipts: updated %d of %d", result.RowsAffected, len(eligibleIDs))
	}
	return nil
}
