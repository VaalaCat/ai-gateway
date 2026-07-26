package billing

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type recordingCoreAggregator struct {
	facts []models.BillingLog
}

func (a *recordingCoreAggregator) SubmitBilling(fact *models.BillingLog) {
	if fact != nil {
		a.facts = append(a.facts, *fact)
	}
}

type recordingLogQueue struct {
	result  deliveryqueue.EnqueueResult
	batches []logqueue.LogBatch
}

func (q *recordingLogQueue) Enqueue(batch logqueue.LogBatch) deliveryqueue.EnqueueResult {
	q.batches = append(q.batches, batch)
	return q.result
}

func TestSettlerCommitsBillingLogAndQuotaInOneCoreTransaction(t *testing.T) {
	t.Run("commits both", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		require.NoError(t, db.Create(&models.User{ID: 1, Username: "atomic", Password: "x", Quota: 1000}).Error)
		require.NoError(t, db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 1, Status: 1}).Error)
		agg := &recordingCoreAggregator{}
		queue := &recordingLogQueue{result: deliveryqueue.EnqueueResult{Accepted: true}}
		settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), agg, queue)

		err := settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{
			RequestID: "atomic-ok", UserID: 1, ModelName: "m", PromptTokens: 1_000,
			Status: 1, Timestamp: time.Now().Unix(),
		}})
		require.NoError(t, err)

		var fact models.BillingLog
		require.NoError(t, db.Where("request_id = ?", "atomic-ok").First(&fact).Error)
		var receipt models.BillingProjectionReceipt
		require.NoError(t, db.Where("request_id = ?", "atomic-ok").First(&receipt).Error)
		require.Equal(t, fact.ID, receipt.BillingLogID)
		require.Equal(t, models.BillingProjectionPending, receipt.State)
		var user models.User
		require.NoError(t, db.First(&user, 1).Error)
		require.Equal(t, int64(900), user.Quota)
		require.Len(t, agg.facts, 1)
		require.Len(t, queue.batches, 1)
		require.Equal(t, "atomic-ok", queue.batches[0].Request.RequestID)
	})

	t.Run("receipt failure rolls back fact and quota", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		require.NoError(t, db.Create(&models.User{ID: 1, Username: "receipt-rollback", Password: "x", Quota: 1000}).Error)
		require.NoError(t, db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 1, Status: 1}).Error)
		require.NoError(t, db.Exec(`CREATE TRIGGER fail_pending_receipt BEFORE INSERT ON billing_projection_receipts BEGIN SELECT RAISE(ABORT, 'forced receipt failure'); END`).Error)
		settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), &recordingCoreAggregator{}, &recordingLogQueue{})

		err := settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{
			RequestID: "receipt-fail", UserID: 1, ModelName: "m", PromptTokens: 1_000, Status: 1,
		}})
		require.ErrorContains(t, err, "forced receipt failure")
		require.Equal(t, int64(0), countRows(t, db, &models.BillingLog{}))
		require.Equal(t, int64(0), countRows(t, db, &models.BillingProjectionReceipt{}))
		var user models.User
		require.NoError(t, db.First(&user, 1).Error)
		require.Equal(t, int64(1000), user.Quota)
	})

	t.Run("rolls back both", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		require.NoError(t, db.Create(&models.User{ID: 1, Username: "rollback", Password: "x", Quota: 1000}).Error)
		require.NoError(t, db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 1, Status: 1}).Error)
		require.NoError(t, db.Exec(`CREATE TRIGGER fail_billing_insert BEFORE INSERT ON billing_logs BEGIN SELECT RAISE(ABORT, 'forced billing failure'); END`).Error)
		agg := &recordingCoreAggregator{}
		queue := &recordingLogQueue{}
		settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), agg, queue)

		err := settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{
			RequestID: "atomic-fail", UserID: 1, ModelName: "m", PromptTokens: 1_000,
			Status: 1, Timestamp: time.Now().Unix(),
		}})
		require.Error(t, err)

		var user models.User
		require.NoError(t, db.First(&user, 1).Error)
		require.Equal(t, int64(1000), user.Quota)
		require.Empty(t, agg.facts)
		require.Empty(t, queue.batches)
	})

	t.Run("quota failure rolls back inserted fact", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		require.NoError(t, db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 1, Status: 1}).Error)
		agg := &recordingCoreAggregator{}
		queue := &recordingLogQueue{}
		settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), agg, queue)

		err := settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{
			RequestID: "quota-fail", UserID: 99, ModelName: "m", PromptTokens: 1_000, Status: 1,
		}})
		require.Error(t, err)
		require.Equal(t, int64(0), countRows(t, db, &models.BillingLog{}))
		require.Empty(t, agg.facts)
		require.Empty(t, queue.batches)
	})
}

func TestSettlerReturnsSuccessWhenLogQueueDropsAfterCommit(t *testing.T) {
	db, appProv := setupTestDB(t)
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "drop", Password: "x", Quota: 1000}).Error)
	agg := &recordingCoreAggregator{}
	queue := &recordingLogQueue{result: deliveryqueue.EnqueueResult{Dropped: true, Error: "full"}}
	settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), agg, queue)

	err := settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{RequestID: "drop-after-commit", UserID: 1, Status: 0}})
	require.NoError(t, err)
	require.Equal(t, int64(1), countRows(t, db, &models.BillingLog{}))
	require.Len(t, agg.facts, 1)
	require.Len(t, queue.batches, 1)
}

func TestSettlerUsesCommittedBillingTimestampForLogBatch(t *testing.T) {
	_, appProv := setupTestDB(t)
	queue := &recordingLogQueue{result: deliveryqueue.EnqueueResult{Accepted: true}}
	settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), &recordingCoreAggregator{}, queue)

	require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{RequestID: "zero-time", Status: 1}}))
	require.Len(t, queue.batches, 1)
	require.Greater(t, queue.batches[0].Request.CreatedAt, int64(0))
	require.NotEqual(t, "1970-01-01", queue.batches[0].Hourly[0].Date)
}

func TestSettlerDuplicateRequestDoesNotDeductOrDeliverTwice(t *testing.T) {
	db, appProv := setupTestDB(t)
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "duplicate", Password: "x", Quota: 1000}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 1, Status: 1}).Error)
	agg := &recordingCoreAggregator{}
	queue := &recordingLogQueue{result: deliveryqueue.EnqueueResult{Accepted: true}}
	settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), agg, queue)
	entry := protocol.UsageLogEntry{RequestID: "same", UserID: 1, ModelName: "m", PromptTokens: 1_000, Status: 1}

	require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entry}))
	require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entry}))

	var user models.User
	require.NoError(t, db.First(&user, 1).Error)
	require.Equal(t, int64(900), user.Quota)
	require.Equal(t, int64(1), countRows(t, db, &models.BillingLog{}))
	require.Len(t, agg.facts, 1)
	require.Len(t, queue.batches, 1)
}

func TestSettlerPreservesRawCostForFreeAndBYOKBillingFacts(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry protocol.UsageLogEntry
	}{
		{name: "free channel", entry: protocol.UsageLogEntry{RequestID: "free-raw", UserID: 1, ModelName: "m", PromptTokens: 1_000, Free: true, Status: 1}},
		{name: "BYOK free mode", entry: protocol.UsageLogEntry{RequestID: "byok-raw", UserID: 1, ModelName: "m", PromptTokens: 1_000, OwnerType: "private", PrivateChannelID: 9, Status: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, appProv := setupTestDB(t)
			require.NoError(t, db.Create(&models.User{ID: 1, Username: test.name, Password: "x", Quota: 1000}).Error)
			require.NoError(t, db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 1, Status: 1}).Error)
			settler := NewCoreFactSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop(), &recordingCoreAggregator{}, &recordingLogQueue{result: deliveryqueue.EnqueueResult{Accepted: true}})

			require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{test.entry}))
			var fact models.BillingLog
			require.NoError(t, db.Where("request_id = ?", test.entry.RequestID).First(&fact).Error)
			require.Zero(t, fact.TotalCost)
			require.Equal(t, int64(100), fact.RawTotal())
			require.NotNil(t, fact.BillingFactor)
			require.Zero(t, *fact.BillingFactor)
		})
	}
}

func countRows(t *testing.T, db interface{ Model(any) *gorm.DB }, model any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(model).Count(&count).Error)
	return count
}
