package billing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestProjectCommittedBillingFactsRollsBackProjectionWhenReceiptFails(t *testing.T) {
	db, _ := setupTestDB(t)
	fact := models.BillingLog{RequestID: "atomic", TotalCost: 17, Status: 1, CreatedAt: time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC).Unix()}
	require.NoError(t, db.Create(&fact).Error)
	require.NoError(t, db.Create(&models.BillingProjectionReceipt{RequestID: fact.RequestID, BillingLogID: fact.ID, State: models.BillingProjectionPending}).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_projection_receipt BEFORE UPDATE ON billing_projection_receipts BEGIN SELECT RAISE(FAIL, 'receipt failure'); END`).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		return ProjectCommittedBillingFactsInTx(t.Context(), tx, []models.BillingLog{fact})
	})
	require.ErrorContains(t, err, "receipt failure")
	requireCoreProjectionCount(t, db, 0)
	var pending models.BillingProjectionReceipt
	require.NoError(t, db.First(&pending, "request_id = ?", fact.RequestID).Error)
	require.Equal(t, models.BillingProjectionPending, pending.State)

	require.NoError(t, db.Exec(`DROP TRIGGER fail_projection_receipt`).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ProjectCommittedBillingFactsInTx(t.Context(), tx, []models.BillingLog{fact})
	}))
	requireCoreProjectionCount(t, db, 1)
}

func TestProjectCommittedBillingFactsAppliesPendingOnce(t *testing.T) {
	db, _ := setupTestDB(t)
	fact := models.BillingLog{RequestID: "pending-once", PromptTokens: 7, Status: 1, CreatedAt: time.Now().Unix()}
	require.NoError(t, db.Create(&fact).Error)
	require.NoError(t, db.Create(&models.BillingProjectionReceipt{RequestID: fact.RequestID, BillingLogID: fact.ID, State: models.BillingProjectionPending}).Error)

	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return ProjectCommittedBillingFactsInTx(t.Context(), tx, []models.BillingLog{fact})
		}))
	}
	var receipt models.BillingProjectionReceipt
	require.NoError(t, db.First(&receipt, "request_id = ?", fact.RequestID).Error)
	require.Equal(t, models.BillingProjectionApplied, receipt.State)
	require.NotZero(t, receipt.ProjectedAtUnix)
	requireCoreProjectionCount(t, db, 1)
}

func TestLoadPendingBillingFactsIsBoundedOrderedAndRejectsOrphan(t *testing.T) {
	db, _ := setupTestDB(t)
	for _, requestID := range []string{"one", "two"} {
		fact := models.BillingLog{RequestID: requestID, CreatedAt: time.Now().Unix()}
		require.NoError(t, db.Create(&fact).Error)
		require.NoError(t, db.Create(&models.BillingProjectionReceipt{RequestID: requestID, BillingLogID: fact.ID, State: models.BillingProjectionPending}).Error)
	}
	facts, err := LoadPendingBillingFacts(t.Context(), db, 1)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, "one", facts[0].RequestID)

	require.NoError(t, db.Create(&models.BillingProjectionReceipt{RequestID: "orphan", BillingLogID: 99, State: models.BillingProjectionPending}).Error)
	_, err = LoadPendingBillingFacts(t.Context(), db, 10)
	require.ErrorContains(t, err, "has no committed fact")
}

func TestProjectCommittedBillingFactsTreatsBaselineFactsAsAlreadyApplied(t *testing.T) {
	db, _ := setupTestDB(t)
	fact := models.BillingLog{RequestID: "pre-upgrade", PromptTokens: 11, CreatedAt: time.Now().Unix()}
	require.NoError(t, db.Create(&fact).Error)
	require.NoError(t, db.Model(&models.BillingProjectionBaseline{}).Where("id = ?", models.BillingProjectionBaselineID).
		Update("billing_log_high_watermark", fact.ID).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ProjectCommittedBillingFactsInTx(t.Context(), tx, []models.BillingLog{fact})
	}))
	requireCoreProjectionCount(t, db, 0)
}

func newPendingRecoveryAggregator(t *testing.T, fail *bool) (*Aggregator, *gorm.DB) {
	t.Helper()
	db, application := setupTestDB(t)
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{FlushEvery: 10 * time.Millisecond})
	agg.SetProjectionFlushContextFn(func(ctx context.Context, facts []models.BillingLog) error {
		if fail != nil && *fail {
			return errors.New("forced projection failure")
		}
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return ProjectCommittedBillingFactsInTx(ctx, tx, facts)
		})
	})
	return agg, db
}

func insertPendingFact(t *testing.T, db *gorm.DB, requestID string, promptTokens int) models.BillingLog {
	t.Helper()
	fact := models.BillingLog{RequestID: requestID, PromptTokens: promptTokens, Status: 1, CreatedAt: time.Now().Unix()}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&fact).Error; err != nil {
			return err
		}
		return RegisterPendingBillingProjectionInTx(t.Context(), tx, &fact)
	}))
	return fact
}

func insertPendingFacts(t *testing.T, db *gorm.DB, prefix string, count int) {
	t.Helper()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for i := range count {
			fact := models.BillingLog{
				RequestID: fmt.Sprintf("%s-%03d", prefix, i), PromptTokens: 1,
				Status: 1, CreatedAt: time.Now().Unix(),
			}
			if err := tx.Create(&fact).Error; err != nil {
				return err
			}
			if err := RegisterPendingBillingProjectionInTx(t.Context(), tx, &fact); err != nil {
				return err
			}
		}
		return nil
	}))
}

func countProjectionReceiptsByState(t *testing.T, db *gorm.DB, state models.BillingProjectionState) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&models.BillingProjectionReceipt{}).Where("state = ?", state).Count(&count).Error)
	return count
}

func TestAggregatorFlushRecoversPendingReceiptAfterRestart(t *testing.T) {
	agg, db := newPendingRecoveryAggregator(t, nil)
	insertPendingFact(t, db, "restart-pending", 13)

	require.NoError(t, agg.FlushContext(t.Context()))
	requireCoreProjectionCount(t, db, 1)
	var receipt models.BillingProjectionReceipt
	require.NoError(t, db.First(&receipt, "request_id = ?", "restart-pending").Error)
	require.Equal(t, models.BillingProjectionApplied, receipt.State)

	require.NoError(t, agg.FlushContext(t.Context()))
	var promptTokens int64
	require.NoError(t, db.Model(&models.TokenDailyBilling{}).Select("COALESCE(SUM(prompt_tokens), 0)").Scan(&promptTokens).Error)
	require.Equal(t, int64(13), promptTokens)
}

func TestAggregatorPeriodicWorkerRecoversAnotherProcessPendingReceipt(t *testing.T) {
	agg, db := newPendingRecoveryAggregator(t, nil)
	insertPendingFact(t, db, "other-process", 17)
	agg.Start(t.Context())
	t.Cleanup(func() { require.NoError(t, agg.Close(context.Background())) })

	require.Eventually(t, func() bool {
		var receipt models.BillingProjectionReceipt
		return db.First(&receipt, "request_id = ?", "other-process").Error == nil && receipt.State == models.BillingProjectionApplied
	}, time.Second, 10*time.Millisecond)
	requireCoreProjectionCount(t, db, 1)
}

func TestAggregatorStartRecoversPendingWithoutTicker(t *testing.T) {
	db, application := setupTestDB(t)
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetProjectionFlushContextFn(func(ctx context.Context, facts []models.BillingLog) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return ProjectCommittedBillingFactsInTx(ctx, tx, facts)
		})
	})
	insertPendingFact(t, db, "startup-pending", 18)
	agg.Start(t.Context())
	t.Cleanup(func() { require.NoError(t, agg.Close(context.Background())) })

	require.Eventually(t, func() bool {
		var receipt models.BillingProjectionReceipt
		return db.First(&receipt, "request_id = ?", "startup-pending").Error == nil && receipt.State == models.BillingProjectionApplied
	}, time.Second, 10*time.Millisecond)
}

func TestAggregatorStartDrainsMoreThanOnePendingBatchWithoutTicker(t *testing.T) {
	agg, db := newPendingRecoveryAggregator(t, nil)
	agg.flushEvery = 0
	insertPendingFacts(t, db, "startup-batch", pendingProjectionBatchSize+1)
	agg.Start(t.Context())
	t.Cleanup(func() { require.NoError(t, agg.Close(context.Background())) })

	require.Eventually(t, func() bool {
		return countProjectionReceiptsByState(t, db, models.BillingProjectionPending) == 0
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, int64(pendingProjectionBatchSize+1), countProjectionReceiptsByState(t, db, models.BillingProjectionApplied))
}

func TestAggregatorPendingRecoveryRetriesProjectionFailure(t *testing.T) {
	fail := true
	agg, db := newPendingRecoveryAggregator(t, &fail)
	insertPendingFact(t, db, "retry-pending", 19)

	require.ErrorContains(t, agg.FlushContext(t.Context()), "forced projection failure")
	var receipt models.BillingProjectionReceipt
	require.NoError(t, db.First(&receipt, "request_id = ?", "retry-pending").Error)
	require.Equal(t, models.BillingProjectionPending, receipt.State)
	requireCoreProjectionCount(t, db, 0)

	fail = false
	require.NoError(t, agg.FlushContext(t.Context()))
	requireCoreProjectionCount(t, db, 1)
}

func TestAggregatorPendingRecoveryRejectsReceiptWithoutFact(t *testing.T) {
	agg, db := newPendingRecoveryAggregator(t, nil)
	require.NoError(t, db.Create(&models.BillingProjectionReceipt{
		RequestID: "orphan-recovery", BillingLogID: 404, State: models.BillingProjectionPending,
	}).Error)
	require.ErrorContains(t, agg.FlushContext(t.Context()), "has no committed fact")
}

func TestAggregatorCloseRecoversPendingReceipt(t *testing.T) {
	agg, db := newPendingRecoveryAggregator(t, nil)
	insertPendingFact(t, db, "shutdown-pending", 31)

	require.NoError(t, agg.Close(t.Context()))
	requireCoreProjectionCount(t, db, 1)
	var receipt models.BillingProjectionReceipt
	require.NoError(t, db.First(&receipt, "request_id = ?", "shutdown-pending").Error)
	require.Equal(t, models.BillingProjectionApplied, receipt.State)
}

func TestAggregatorCloseDrainsMultiplePendingBatchesWithoutTicker(t *testing.T) {
	agg, db := newPendingRecoveryAggregator(t, nil)
	agg.flushEvery = 0
	insertPendingFacts(t, db, "close-batch", pendingProjectionBatchSize*2+1)

	require.NoError(t, agg.Close(t.Context()))
	require.Zero(t, countProjectionReceiptsByState(t, db, models.BillingProjectionPending))
	require.Equal(t, int64(pendingProjectionBatchSize*2+1), countProjectionReceiptsByState(t, db, models.BillingProjectionApplied))
}

func TestAggregatorCloseStopsWhenSecondPendingBatchProjectionFails(t *testing.T) {
	db, application := setupTestDB(t)
	calls := 0
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetProjectionFlushContextFn(func(ctx context.Context, facts []models.BillingLog) error {
		calls++
		if calls == 2 {
			return errors.New("forced second batch failure")
		}
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return ProjectCommittedBillingFactsInTx(ctx, tx, facts)
		})
	})
	insertPendingFacts(t, db, "close-failure", pendingProjectionBatchSize+1)

	err := agg.Close(t.Context())
	require.ErrorContains(t, err, "forced second batch failure")
	require.Equal(t, 2, calls)
	require.Equal(t, int64(1), countProjectionReceiptsByState(t, db, models.BillingProjectionPending))
	require.Equal(t, int64(pendingProjectionBatchSize), countProjectionReceiptsByState(t, db, models.BillingProjectionApplied))
}

func TestAggregatorCloseStopsWhenProjectionMakesNoProgress(t *testing.T) {
	db, application := setupTestDB(t)
	calls := 0
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetProjectionFlushContextFn(func(context.Context, []models.BillingLog) error {
		calls++
		return nil
	})
	insertPendingFacts(t, db, "no-progress", pendingProjectionBatchSize+1)

	err := agg.Close(t.Context())
	require.ErrorContains(t, err, "no progress")
	require.Equal(t, 1, calls)
	require.Equal(t, int64(pendingProjectionBatchSize+1), countProjectionReceiptsByState(t, db, models.BillingProjectionPending))
}

func requireCoreProjectionCount(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	for _, model := range []any{
		&models.TokenDailyBilling{}, &models.ChannelDailyBilling{}, &models.BillingHourlyBucket{},
	} {
		var got int64
		require.NoError(t, db.Model(model).Count(&got).Error)
		require.Equal(t, want, got)
	}
}
