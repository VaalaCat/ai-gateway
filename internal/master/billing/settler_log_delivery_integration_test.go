package billing

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestCoreFactSettlerLogOutageKeepsCoreBillingAndRecoversWithoutDoubleCounting(t *testing.T) {
	core := newSettlerOutageDB(t, true)
	logs := newSettlerOutageDB(t, false)
	require.NoError(t, core.Create(&models.User{ID: 1, Username: "queue-user", Email: "queue@example.test", Quota: 10_000}).Error)
	require.NoError(t, core.Create(&models.ModelConfig{ModelName: "gpt-test", InputPrice: 1, Status: 1}).Error)

	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	settings := logqueue.DeliverySettings{QueueMaxEntries: 3, QueueMaxBytes: 1 << 20, DeliveryBatchSize: 2, BackoffMaxSeconds: 1}
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: settings.QueueMaxEntries, MaxBytes: settings.QueueMaxBytes}, logqueue.BatchSize, nil)
	var available atomic.Bool
	var current atomic.Pointer[gorm.DB]
	worker := logqueue.NewLogDeliveryWorker(logqueue.WorkerOptions{
		Queue:    queue,
		Writer:   &logqueue.LogBatchWriter{DBFinder: current.Load},
		Settings: settlerOutageSettings{settings: settings},
		Connector: logqueue.DatabaseConnectorFunc(func(context.Context) (*gorm.DB, error) {
			if !available.Load() {
				return nil, errors.New("log database is read-only")
			}
			return logs, nil
		}),
		Handoff: func(db *gorm.DB) *gorm.DB {
			old := current.Swap(db)
			application.SetLogDB(db)
			return old
		},
		SnapshotPath: filepath.Join(t.TempDir(), "log-delivery.snapshot.gz"),
		PollInterval: time.Millisecond, RetryBase: time.Millisecond, RetryMax: 2 * time.Millisecond,
	})
	require.NoError(t, worker.Start(t.Context()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	settler := NewCoreFactSettler(application, eventbus.NewMemoryBus(), zap.NewNop(), noopCoreAggregator{}, worker)

	entries := make([]protocol.UsageLogEntry, 0, 5)
	for index := 1; index <= 5; index++ {
		entry := protocol.UsageLogEntry{
			RequestID: fmt.Sprintf("settled-%d", index), UserID: 1, TokenID: 1, ChannelID: 2,
			OwnerType: "admin", ModelName: "gpt-test", PromptTokens: 1_000, CompletionTokens: 30,
			IsStream: true, Duration: 400, FirstResponseMs: 100, Status: 1,
			Timestamp: 1_784_787_600, TraceData: `{"inbound_path":"/v1/chat/completions"}`,
		}
		entries = append(entries, entry)
		require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entry}))
	}
	require.Eventually(t, func() bool { return worker.Status().LastError != "" }, time.Second, time.Millisecond)
	stats := queue.Stats()
	require.Equal(t, 3, stats.Pending+stats.Retry+stats.Inflight)
	require.EqualValues(t, 2, stats.Dropped)
	items := queue.Items()
	require.Len(t, items, 3)
	require.Equal(t, "settled-3", items[0].Value.Request.RequestID)
	assertSettlerOutageCoreTotals(t, core, 5, 9_500)

	available.Store(true)
	worker.RetryNow()
	require.Eventually(t, func() bool {
		status := worker.Status()
		return status.SchemaReady && status.Queue.Pending+status.Queue.Retry+status.Queue.Inflight == 0
	}, time.Second, time.Millisecond)

	// A duplicate settlement after recovery must not enqueue or apply log deltas twice.
	require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entries[2]}))
	require.Zero(t, queue.Stats().Pending+queue.Stats().Retry+queue.Stats().Inflight)
	var requestCount, traceCount, histogramSamples int64
	require.NoError(t, logs.Model(&models.RequestLog{}).Count(&requestCount).Error)
	require.NoError(t, logs.Model(&models.RequestTrace{}).Count(&traceCount).Error)
	require.NoError(t, logs.Model(&models.UsageDurationHistogram{}).
		Select("COALESCE(SUM(h0+h1+h2+h3+h4+h5+h6+h7+h8+h9+h10+h11+h12+h13+h14+h15+h16), 0)").
		Scan(&histogramSamples).Error)
	require.EqualValues(t, 3, requestCount)
	require.EqualValues(t, 3, traceCount)
	require.EqualValues(t, 3, histogramSamples)
	assertSettlerOutageCoreTotals(t, core, 5, 9_500)
}

type settlerOutageSettings struct{ settings logqueue.DeliverySettings }

func (s settlerOutageSettings) Find(context.Context) logqueue.DeliverySettings { return s.settings }

func newSettlerOutageDB(t *testing.T, core bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if core {
		require.NoError(t, models.MigrateCoreDB(db))
	} else {
		require.NoError(t, models.MigrateLogDB(db))
	}
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func assertSettlerOutageCoreTotals(t *testing.T, core *gorm.DB, wantFacts, wantQuota int64) {
	t.Helper()
	var count int64
	require.NoError(t, core.Model(&models.BillingLog{}).Count(&count).Error)
	require.Equal(t, wantFacts, count)
	var user models.User
	require.NoError(t, core.First(&user, 1).Error)
	require.Equal(t, wantQuota, user.Quota)
	require.Equal(t, int64(10_000)-wantQuota, user.UsedQuota)
}
