package apiusage

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	"github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type apiServiceFinderStub struct {
	service *models.APIService
}

type apiUsageTestApp struct{ db *gorm.DB }

func (a apiUsageTestApp) GetCoreDB() *gorm.DB { return a.db }
func (a apiUsageTestApp) GetLogDB() *gorm.DB  { return a.db }
func (apiUsageTestApp) GetDatabaseLayoutMode() app.DatabaseLayoutMode {
	return app.DatabaseLayoutLegacySingle
}

type recordingAPILogQueue struct{ batches []logqueue.LogBatch }

func (q *recordingAPILogQueue) Enqueue(batch logqueue.LogBatch) deliveryqueue.EnqueueResult {
	q.batches = append(q.batches, batch)
	return deliveryqueue.EnqueueResult{Accepted: true}
}

func newAPIUsageSettlerFixture(t *testing.T) (apiUsageTestApp, *eventbus.MemoryBus, *recordingAPILogQueue) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))
	require.NoError(t, db.AutoMigrate(&models.APIService{}))
	bus := eventbus.NewMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })
	return apiUsageTestApp{db: db}, bus, &recordingAPILogQueue{}
}

func (f apiServiceFinderStub) FindByID(context.Context, uint) (*models.APIService, error) {
	return f.service, nil
}

// Production break caught: the current service price must be charged only
// after a provider dispatch is both known and true; unknown and false reports
// must never consume quota even when their service has a non-zero price.
func TestAPIUsageSettlerChargesCurrentPriceOnlyWhenDispatchKnownAndTrue(t *testing.T) {
	settler := NewAPIUsageSettler(apiServiceFinderStub{service: &models.APIService{
		ID:           42,
		PricePerCall: 12345,
	}})

	for _, tt := range []struct {
		name                  string
		providerDispatchKnown bool
		providerDispatched    bool
		wantQuota             int64
	}{
		{name: "dispatch unknown", providerDispatched: true, wantQuota: 0},
		{name: "dispatch known but not sent", providerDispatchKnown: true, wantQuota: 0},
		{name: "dispatch known and sent", providerDispatchKnown: true, providerDispatched: true, wantQuota: 12345},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usage, err := settler.Settle(context.Background(), protocol.APIUsageEntry{
				RequestID:             "request-" + tt.name,
				APIServiceID:          42,
				SourceAgentID:         "source-agent",
				ProviderDispatchKnown: tt.providerDispatchKnown,
				ProviderDispatched:    tt.providerDispatched,
			})

			require.NoError(t, err)
			require.Equal(t, tt.wantQuota, usage.Quota)
			require.Equal(t, "source-agent", usage.SourceAgentID)
		})
	}
}

// Production break caught: a free API service must produce the normal typed
// audit log while leaving the user's quota unchanged.
func TestAPIUsageSettlerFreeServiceWritesLogWithoutQuotaMutation(t *testing.T) {
	app, bus, logs := newAPIUsageSettlerFixture(t)
	user := models.User{Username: "free-user", Password: "x", Status: consts.StatusEnabled, Quota: 100}
	require.NoError(t, app.db.Create(&user).Error)
	service := models.APIService{Slug: "free-api", Name: "Free API", PricePerCall: 0, Status: consts.StatusEnabled}
	require.NoError(t, app.db.Create(&service).Error)
	settler := NewMasterAPIUsageSettler(app, bus, zap.NewNop(), logs)
	_, err := settler.Settle(t.Context(), protocol.APIUsageEntry{RequestID: "free-request", UserID: user.ID, APIServiceID: service.ID, SourceAgentID: "source", ProviderDispatchKnown: true, ProviderDispatched: true})
	require.NoError(t, err)
	var got models.User
	require.NoError(t, app.db.First(&got, user.ID).Error)
	require.Equal(t, int64(100), got.Quota)
	require.Len(t, logs.batches, 1)
	require.Zero(t, logs.batches[0].APIRequest.TotalCost)
}

// Production break caught: a deleted service must never guess an old price;
// its audit row is zero-priced, marks independent settlement absence, and
// retains the execution ErrorStage reported by the Agent.
func TestAPIUsageSettlerMissingServiceLogsZeroPriceSnapshot(t *testing.T) {
	app, bus, logs := newAPIUsageSettlerFixture(t)
	settler := NewMasterAPIUsageSettler(app, bus, zap.NewNop(), logs)
	_, err := settler.Settle(t.Context(), protocol.APIUsageEntry{RequestID: "missing-request", APIServiceID: 999, SourceAgentID: "source", ErrorStage: "upstream_timeout", ProviderDispatchKnown: true, ProviderDispatched: true})
	require.NoError(t, err)
	require.Len(t, logs.batches, 1)
	row := logs.batches[0].APIRequest
	require.Zero(t, row.UnitPrice)
	require.True(t, row.ServiceMissingAtSettlement)
	require.Equal(t, "upstream_timeout", row.ErrorStage)
}

// Production break caught: a charged API call must publish the new quota to
// its source Agent and use the same depleted event path that disables tokens.
func TestAPIUsageSettlerPublishesQuotaSyncedAndDepletedLikeLLM(t *testing.T) {
	app, bus, logs := newAPIUsageSettlerFixture(t)
	user := models.User{Username: "paid-user", Password: "x", Status: consts.StatusEnabled, Quota: 5}
	require.NoError(t, app.db.Create(&user).Error)
	token := models.Token{UserID: user.ID, Key: "sk-api-paid", Name: "paid", Status: consts.StatusEnabled}
	require.NoError(t, app.db.Create(&token).Error)
	service := models.APIService{Slug: "paid-api", Name: "Paid API", PricePerCall: 10, Status: consts.StatusEnabled}
	require.NoError(t, app.db.Create(&service).Error)
	checker := billing.NewQuotaChecker(app, bus, zap.NewNop())
	checker.Start()
	syncs := make(chan protocol.UserQuotaSync, 1)
	_, err := events.SubscribeUserQuotaSync(bus, func(_ context.Context, value protocol.UserQuotaSync) error { syncs <- value; return nil })
	require.NoError(t, err)
	settler := NewMasterAPIUsageSettler(app, bus, zap.NewNop(), logs)
	_, err = settler.Settle(t.Context(), protocol.APIUsageEntry{RequestID: "paid-request", UserID: user.ID, TokenID: token.ID, APIServiceID: service.ID, SourceAgentID: "source-agent", ProviderDispatchKnown: true, ProviderDispatched: true})
	require.NoError(t, err)
	require.Equal(t, "source-agent", (<-syncs).AgentID)
	require.Eventually(t, func() bool { var got models.User; return app.db.First(&got, user.ID).Error == nil && got.Quota == -5 }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		var got models.Token
		return app.db.First(&got, token.ID).Error == nil && got.Status == consts.StatusDisabled
	}, time.Second, time.Millisecond)
}

// Production break caught: a Log DB reconnect must replay the typed API batch
// only; it must not call Settle again or repeat the already-committed quota
// deduction while the log database is unavailable.
func TestAPILogDeliveryRetriesSplitDBFailureWithoutBlockingSettler(t *testing.T) {
	app, bus, _ := newAPIUsageSettlerFixture(t)
	user := models.User{Username: "reconnect-user", Password: "x", Status: consts.StatusEnabled, Quota: 100}
	require.NoError(t, app.db.Create(&user).Error)
	service := models.APIService{Slug: "reconnect-api", Name: "Reconnect API", PricePerCall: 10, Status: consts.StatusEnabled}
	require.NoError(t, app.db.Create(&service).Error)
	logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := logDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, models.MigrateLogDB(logDB))
	var current atomic.Pointer[gorm.DB]
	var opens atomic.Int32
	worker := logqueue.NewLogDeliveryWorker(logqueue.WorkerOptions{
		Writer: &logqueue.LogBatchWriter{DBFinder: current.Load},
		Connector: logqueue.DatabaseConnectorFunc(func(context.Context) (*gorm.DB, error) {
			if opens.Add(1) == 1 {
				return nil, errors.New("log db offline")
			}
			return logDB, nil
		}),
		Handoff: current.Swap, SnapshotPath: filepath.Join(t.TempDir(), "api-log.snapshot.gz"), PollInterval: time.Millisecond, RetryBase: time.Millisecond, RetryMax: 2 * time.Millisecond,
	})
	require.NoError(t, worker.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, worker.Stop(context.Background())) })
	settler := NewMasterAPIUsageSettler(app, bus, zap.NewNop(), worker)
	_, err = settler.Settle(t.Context(), protocol.APIUsageEntry{RequestID: "reconnect-request", UserID: user.ID, APIServiceID: service.ID, SourceAgentID: "source", ProviderDispatchKnown: true, ProviderDispatched: true, Trace: &apiattempt.APIExecutionTrace{RequestBody: &apiattempt.APIBodyCapture{Captured: true, Data: "body"}}})
	require.NoError(t, err)
	var charged models.User
	require.NoError(t, app.db.First(&charged, user.ID).Error)
	require.Equal(t, int64(90), charged.Quota)
	require.Eventually(t, func() bool {
		var row models.APIRequestLog
		return logDB.Where("request_id = ?", "reconnect-request").First(&row).Error == nil
	}, time.Second, time.Millisecond)
	require.NoError(t, app.db.First(&charged, user.ID).Error)
	require.Equal(t, int64(90), charged.Quota)
	var traces int64
	require.NoError(t, logDB.Model(&models.APIRequestTrace{}).Where("request_id = ?", "reconnect-request").Count(&traces).Error)
	require.Equal(t, int64(1), traces)
}
