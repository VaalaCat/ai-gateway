package billing

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

type recordingAutoDisabler struct {
	calls [][]attemptproxy.ChannelAutoDisableTrigger
	errs  []error
}

func (d *recordingAutoDisabler) DisableFromTriggers(_ context.Context, triggers []attemptproxy.ChannelAutoDisableTrigger) error {
	d.calls = append(d.calls, append([]attemptproxy.ChannelAutoDisableTrigger(nil), triggers...))
	if len(d.errs) == 0 {
		return nil
	}
	err := d.errs[0]
	d.errs = d.errs[1:]
	return err
}

func autoDisableUsageEntry(requestID string, channelID uint) protocol.UsageLogEntry {
	return protocol.UsageLogEntry{
		RequestID: requestID,
		Status:    1,
		Timestamp: time.Now().Unix(),
		AutoDisableTriggers: []attemptproxy.ChannelAutoDisableTrigger{{
			Source: attemptproxy.SourceAdmin, ChannelID: channelID, Revision: 2,
			Reason: attemptproxy.ChannelAutoDisableReasonConsecutiveErrors,
		}},
	}
}

func TestSettlerProcessesAutoDisableTriggersAfterBillingCommit(t *testing.T) {
	t.Run("billing and trigger both succeed", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		disabler := &recordingAutoDisabler{}
		settler := newTestSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop())
		settler.AutoDisabler = disabler

		require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{autoDisableUsageEntry("trigger-ok", 3)}))
		require.Len(t, disabler.calls, 1)
		require.Equal(t, uint(3), disabler.calls[0][0].ChannelID)
		var count int64
		require.NoError(t, db.Model(&models.BillingLog{}).Where("request_id = ?", "trigger-ok").Count(&count).Error)
		require.Equal(t, int64(1), count)
	})

	t.Run("no triggers skips disabler", func(t *testing.T) {
		_, appProv := setupTestDB(t)
		disabler := &recordingAutoDisabler{}
		settler := newTestSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop())
		settler.AutoDisabler = disabler

		require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{RequestID: "no-trigger", Status: 1}}))
		require.Empty(t, disabler.calls)
	})
}

func TestSettlerRetriesAutoDisableAfterBillingDedup(t *testing.T) {
	db, appProv := setupTestDB(t)
	disabler := &recordingAutoDisabler{errs: []error{errors.New("database unavailable"), nil}}
	settler := newTestSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop())
	settler.AutoDisabler = disabler
	entry := autoDisableUsageEntry("trigger-retry", 4)

	require.Error(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entry}))
	require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entry}))
	require.Len(t, disabler.calls, 2)
	var count int64
	require.NoError(t, db.Model(&models.BillingLog{}).Where("request_id = ?", entry.RequestID).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestSettlerAutoDisableFailureDoesNotSuppressCommittedQuotaEvent(t *testing.T) {
	db, appProv := setupTestDB(t)
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "depleted-trigger", Password: "x", Status: 1, Quota: 1}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "priced", InputPrice: 1, Status: 1}).Error)
	bus := eventbus.NewMemoryBus()
	depletedEvents := 0
	_, err := events.SubscribeUserQuotaDepleted(bus, func(context.Context, models.User) error {
		depletedEvents++
		return nil
	})
	require.NoError(t, err)
	disabler := &recordingAutoDisabler{errs: []error{errors.New("database unavailable")}}
	settler := newTestSettler(appProv, bus, zap.NewNop())
	settler.AutoDisabler = disabler
	entry := autoDisableUsageEntry("trigger-depleted", 4)
	entry.UserID = 1
	entry.ModelName = "priced"
	entry.PromptTokens = 1_000_000

	require.Error(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entry}))
	require.Equal(t, 1, depletedEvents)
}

func TestSettlerAggregatesAutoDisableErrorsAndSettlesOtherEntries(t *testing.T) {
	db, appProv := setupTestDB(t)
	disabler := &recordingAutoDisabler{errs: []error{errors.New("first failed"), nil}}
	settler := newTestSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop())
	settler.AutoDisabler = disabler

	err := settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{
		autoDisableUsageEntry("trigger-first", 5),
		autoDisableUsageEntry("trigger-second", 6),
	})
	require.Error(t, err)
	require.Len(t, disabler.calls, 2)
	var count int64
	require.NoError(t, db.Model(&models.BillingLog{}).Where("request_id IN ?", []string{"trigger-first", "trigger-second"}).Count(&count).Error)
	require.Equal(t, int64(2), count)
}

// testAppProvider wraps *gorm.DB to satisfy dao.AppProvider.
type testAppProvider struct{ db *gorm.DB }

func (p *testAppProvider) GetCoreDB() *gorm.DB { return p.db }
func (p *testAppProvider) GetLogDB() *gorm.DB  { return p.db }
func (p *testAppProvider) GetDatabaseLayoutMode() app.DatabaseLayoutMode {
	return app.DatabaseLayoutLegacySingle
}

type legacyTestLogQueue struct{ db *gorm.DB }

func (q legacyTestLogQueue) Enqueue(batch logqueue.LogBatch) deliveryqueue.EnqueueResult {
	err := q.db.Transaction(func(tx *gorm.DB) error {
		request := models.UsageLog(batch.Request)
		request.ID = 0
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		for _, source := range batch.Traces {
			trace := models.UsageLogTrace(source)
			trace.ID = 0
			if err := tx.Create(&trace).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return deliveryqueue.EnqueueResult{Dropped: true, Error: err.Error()}
	}
	return deliveryqueue.EnqueueResult{Accepted: true}
}

func newTestSettler(application dao.AppProvider, bus app.EventBus, logger *zap.Logger) *Settler {
	return NewCoreFactSettler(application, bus, logger, legacyTestLogQueue{db: application.GetCoreDB()})
}

func setupTestDB(t *testing.T) (*gorm.DB, *testAppProvider) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.BillingLog{}); err != nil {
		t.Fatal(err)
	}
	return db, &testAppProvider{db: db}
}

func TestSetupTestDBOwnsSingleSQLiteMemoryConnection(t *testing.T) {
	db, _ := setupTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1 for connection-local SQLite memory schema", got)
	}
}

func TestSettleUsage(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	// Setup: user with quota 10000, model pricing
	db.Create(&models.User{Username: "test", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)

	// Settle usage
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-1",
			UserID:           1,
			TokenID:          1,
			ChannelID:        1,
			ModelName:        "gpt-4o",
			PromptTokens:     1000,
			CompletionTokens: 500,
			Timestamp:        time.Now().Unix(),
		},
	})

	// Check usage log created
	var logCount int64
	db.Model(&models.UsageLog{}).Count(&logCount)
	if logCount != 1 {
		t.Errorf("usage logs = %d, want 1", logCount)
	}

	// Check user quota decreased
	var user models.User
	db.First(&user, 1)
	if user.Quota >= 10000 {
		t.Errorf("quota should have decreased, got %d", user.Quota)
	}
	if user.UsedQuota <= 0 {
		t.Errorf("used_quota should be > 0, got %d", user.UsedQuota)
	}

	// Test deduplication
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{RequestID: "req-1", UserID: 1, ModelName: "gpt-4o", PromptTokens: 1000, CompletionTokens: 500},
	})
	db.Model(&models.UsageLog{}).Count(&logCount)
	if logCount != 1 {
		t.Errorf("duplicate should be ignored, got %d logs", logCount)
	}
}

func TestSettlePersistsAgentRouteScalarsVerbatim(t *testing.T) {
	db, appProv := setupTestDB(t)
	db.Create(&models.User{Username: "route-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	settler := newTestSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop())

	entries := []protocol.UsageLogEntry{
		{
			RequestID: "req-route-direct", UserID: 1, Status: 1, Timestamp: time.Now().Unix(),
			RouteSourceAgentID: "source-a", AgentRouteID: 42, AgentRoutePath: "direct",
		},
		{
			RequestID: "req-route-local", UserID: 1, Status: 1, Timestamp: time.Now().Unix(),
			RouteSourceAgentID: "source-local", AgentRouteID: 43, AgentRoutePath: "local",
		},
		{
			RequestID: "req-route-old", UserID: 1, Status: 1, Timestamp: time.Now().Unix(),
		},
	}
	settler.Settle(context.Background(), "executing-agent", entries)

	var got []models.UsageLog
	require.NoError(t, db.Where("request_id LIKE ?", "req-route-%").Order("request_id").Find(&got).Error)
	require.Len(t, got, 3)
	require.Equal(t, "source-a", got[0].RouteSourceAgentID)
	require.Equal(t, uint(42), got[0].AgentRouteID)
	require.Equal(t, "direct", got[0].AgentRoutePath)
	require.Equal(t, "source-local", got[1].RouteSourceAgentID)
	require.Equal(t, uint(43), got[1].AgentRouteID)
	require.Equal(t, "local", got[1].AgentRoutePath)
	require.Empty(t, got[2].RouteSourceAgentID)
	require.Zero(t, got[2].AgentRouteID)
	require.Empty(t, got[2].AgentRoutePath)
}

// behavior change: pointer presence, rather than string emptiness, selects the
// execution agent persisted on UsageLog.
func TestSettlerExecutionAgentIDPointerPresence(t *testing.T) {
	db, appProv := setupTestDB(t)
	db.Create(&models.User{Username: "execution-agent-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	settler := newTestSettler(appProv, eventbus.NewMemoryBus(), zap.NewNop())
	target, empty := "target-a", ""

	settler.Settle(context.Background(), "authenticated-source", []protocol.UsageLogEntry{
		{RequestID: "req-execution-1-old", UserID: 1, Status: 1, Timestamp: time.Now().Unix()},
		{RequestID: "req-execution-2-target", UserID: 1, Status: 1, Timestamp: time.Now().Unix(), ExecutionAgentID: &target},
		{RequestID: "req-execution-3-empty", UserID: 1, Status: 1, Timestamp: time.Now().Unix(), ExecutionAgentID: &empty},
	})

	var got []models.UsageLog
	require.NoError(t, db.Where("request_id LIKE ?", "req-execution-%").Order("request_id").Find(&got).Error)
	require.Len(t, got, 3)
	require.Equal(t, "authenticated-source", got[0].AgentID)
	require.Equal(t, "target-a", got[1].AgentID)
	require.Empty(t, got[2].AgentID)
}

// TestSettle_PublishesUserQuotaSync 验证结算后把受影响 user 的最新 Quota 定向回送
// 给来源 agent。覆盖三类场景：success(有 owner → 发回送)、boundary(全 owner-less →
// 不发)、failure(user 不存在 → 跳过该 id)。
func TestSettle_PublishesUserQuotaSync(t *testing.T) {
	t.Run("success_publishes_for_seeded_user", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		bus := eventbus.NewMemoryBus()
		logger := zap.NewNop()

		db.Create(&models.User{ID: 9, Username: "u9", Password: "x", Role: 1, Status: 1, Quota: 10000, GroupID: 2})
		db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

		got := make(chan protocol.UserQuotaSync, 1)
		_, err := events.SubscribeUserQuotaSync(bus, func(_ context.Context, m protocol.UserQuotaSync) error {
			got <- m
			return nil
		})
		require.NoError(t, err)

		settler := newTestSettler(appProv, bus, logger)
		settler.Settle(context.Background(), "agentX", []protocol.UsageLogEntry{
			{
				RequestID:        "req-sync-1",
				UserID:           9,
				TokenID:          1,
				ChannelID:        1,
				ModelName:        "m",
				PromptTokens:     100,
				CompletionTokens: 50,
				Status:           1,
				Timestamp:        time.Now().Unix(),
			},
		})

		select {
		case m := <-got:
			require.Equal(t, "agentX", m.AgentID)
			require.Len(t, m.Users, 1)
			require.Equal(t, uint(9), m.Users[0].ID)
			require.Equal(t, uint(2), m.Users[0].GroupID)
			// Quota 应为结算扣减后的余额(< 初始 10000)
			require.Less(t, m.Users[0].Quota, int64(10000))
		case <-time.After(time.Second):
			t.Fatal("timeout waiting user.quota_synced")
		}
	})

	t.Run("boundary_no_owner_publishes_nothing", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		bus := eventbus.NewMemoryBus()
		logger := zap.NewNop()

		db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

		var count atomic.Int64
		_, err := events.SubscribeUserQuotaSync(bus, func(_ context.Context, _ protocol.UserQuotaSync) error {
			count.Add(1)
			return nil
		})
		require.NoError(t, err)

		settler := newTestSettler(appProv, bus, logger)
		settler.Settle(context.Background(), "agentX", []protocol.UsageLogEntry{
			{
				RequestID: "req-sync-anon",
				UserID:    0,
				TokenName: "anon",
				ModelName: "m",
				Status:    1,
				Timestamp: time.Now().Unix(),
			},
		})

		time.Sleep(100 * time.Millisecond)
		require.Equal(t, int64(0), count.Load(), "owner-less batch must not publish UserQuotaSync")
	})

	t.Run("failure_missing_user_skipped", func(t *testing.T) {
		_, appProv := setupTestDB(t)
		bus := eventbus.NewMemoryBus()
		logger := zap.NewNop()

		var count atomic.Int64
		_, err := events.SubscribeUserQuotaSync(bus, func(_ context.Context, _ protocol.UserQuotaSync) error {
			count.Add(1)
			return nil
		})
		require.NoError(t, err)

		settler := newTestSettler(appProv, bus, logger)
		// user 777 不存在 → GetByID 报错 → 跳过 → users 为空 → 不发
		settler.Settle(context.Background(), "agentX", []protocol.UsageLogEntry{
			{
				RequestID: "req-sync-missing",
				UserID:    777,
				ModelName: "m",
				Status:    1,
				Timestamp: time.Now().Unix(),
			},
		})

		time.Sleep(100 * time.Millisecond)
		require.Equal(t, int64(0), count.Load(), "missing user must be skipped → no publish")
	})
}

func TestQuotaDepletion(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	// User with very small quota
	db.Create(&models.User{Username: "poor", Password: "x", Role: 1, Status: 1, Quota: 1})
	db.Create(&models.Token{UserID: 1, Key: "sk-poor", Name: "t1", Status: 1, ExpiredAt: -1})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)
	checker := NewQuotaChecker(appProv, bus, logger)
	checker.Start()

	// Settle large usage
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-deplete",
			UserID:           1,
			TokenID:          1,
			ChannelID:        1,
			ModelName:        "gpt-4o",
			PromptTokens:     10000,
			CompletionTokens: 5000,
			Timestamp:        time.Now().Unix(),
		},
	})

	// Wait for async event processing
	time.Sleep(100 * time.Millisecond)

	// Token should be disabled
	var token models.Token
	db.First(&token, 1)
	if token.Status != 0 {
		t.Errorf("token status = %d, want 0 (disabled)", token.Status)
	}
}

func TestSettleUsage_SystemTestOwnerlessPersistsUsageLogWithoutQuotaDeduction(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	sentinelUser := models.User{Username: "system-ownerless-sentinel", Password: "x", Role: 1, Status: 1, Quota: 10000}
	db.Create(&sentinelUser)
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-system-ownerless-1",
			UserID:           0,
			TokenID:          1,
			ChannelID:        1,
			TokenName:        "__system_test__",
			ModelName:        "gpt-4o",
			PromptTokens:     1000,
			CompletionTokens: 500,
			Status:           1,
			Timestamp:        time.Now().Unix(),
		},
	})

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-system-ownerless-1").First(&log).Error; err != nil {
		t.Errorf("query usage log failed: %v", err)
	} else {
		if log.UserID != 0 {
			t.Fatalf("user_id = %d, want 0", log.UserID)
		}
		if log.TotalCost <= 0 {
			t.Fatalf("total_cost = %d, want > 0", log.TotalCost)
		}
	}

	var user models.User
	if err := db.First(&user, sentinelUser.ID).Error; err != nil {
		t.Fatalf("query sentinel user failed: %v", err)
	}
	if user.Quota != 10000 {
		t.Fatalf("quota = %d, want 10000", user.Quota)
	}
	if user.UsedQuota != 0 {
		t.Fatalf("used_quota = %d, want 0", user.UsedQuota)
	}
}

func TestSettleUsage_NonSystemOwnerlessPersistsUsageLogWithoutUserDeduction(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	sentinelUser := models.User{Username: "ownerless-sentinel", Password: "x", Role: 1, Status: 1, Quota: 10000}
	db.Create(&sentinelUser)
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-ownerless-1",
			UserID:           0,
			TokenID:          1,
			ChannelID:        1,
			TokenName:        "ownerless-token",
			ModelName:        "gpt-4o",
			PromptTokens:     1000,
			CompletionTokens: 500,
			Status:           1,
			Timestamp:        time.Now().Unix(),
		},
	})

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-ownerless-1").First(&log).Error; err != nil {
		t.Errorf("query usage log failed: %v", err)
	} else {
		if log.UserID != 0 {
			t.Fatalf("user_id = %d, want 0", log.UserID)
		}
		if log.TotalCost <= 0 {
			t.Fatalf("total_cost = %d, want > 0", log.TotalCost)
		}
	}

	var user models.User
	if err := db.First(&user, sentinelUser.ID).Error; err != nil {
		t.Fatalf("query sentinel user failed: %v", err)
	}
	if user.Quota != 10000 {
		t.Fatalf("quota = %d, want 10000", user.Quota)
	}
	if user.UsedQuota != 0 {
		t.Fatalf("used_quota = %d, want 0", user.UsedQuota)
	}
}

func TestSettleUsagePersistsFailedStatus(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "failed-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-failed-1",
			UserID:           1,
			TokenID:          1,
			ChannelID:        1,
			ModelName:        "gpt-4o",
			PromptTokens:     0,
			CompletionTokens: 0,
			Status:           0,
			ErrorMessage:     "upstream returned 503",
			Timestamp:        time.Now().Unix(),
		},
	})

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-failed-1").First(&log).Error; err != nil {
		t.Fatalf("query usage log failed: %v", err)
	}
	if log.Status != 0 {
		t.Fatalf("status = %d, want 0", log.Status)
	}
	if log.ErrorMessage != "upstream returned 503" {
		t.Fatalf("error_message = %q, want %q", log.ErrorMessage, "upstream returned 503")
	}
}

func TestSettleUsage_EmptyModelDoesNotWarnAndUsesZeroCost(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	core, observed := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	db.Create(&models.User{Username: "empty-model-user", Password: "x", Role: 1, Status: 1, Quota: 10000})

	settler := newTestSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-empty-model",
			UserID:           1,
			TokenID:          1,
			ChannelID:        1,
			ModelName:        "",
			Status:           0,
			ErrorMessage:     "model is required",
			Timestamp:        time.Now().Unix(),
			PromptTokens:     0,
			CompletionTokens: 0,
		},
	})

	if observed.FilterMessage("no pricing for model").Len() != 0 {
		t.Fatalf("expected no pricing warning for empty model, got logs: %+v", observed.All())
	}

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-empty-model").First(&log).Error; err != nil {
		t.Fatalf("query usage log failed: %v", err)
	}
	if log.TotalCost != 0 {
		t.Fatalf("total_cost = %d, want 0", log.TotalCost)
	}
	if log.ModelName != "" {
		t.Fatalf("model_name = %q, want empty", log.ModelName)
	}
	if log.Status != 0 {
		t.Fatalf("status = %d, want 0", log.Status)
	}
}

func TestSettleOne_HasTrace(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "trace-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-trace-1",
			UserID:           1,
			TokenID:          1,
			ChannelID:        1,
			ModelName:        "gpt-4o",
			PromptTokens:     100,
			CompletionTokens: 50,
			Status:           1,
			TraceData:        `{"inbound_path":"/v1/chat/completions","outbound_path":"https://api.openai.com/v1/chat/completions"}`,
			Timestamp:        time.Now().Unix(),
		},
	})

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-trace-1").First(&log).Error; err != nil {
		t.Fatalf("query usage log failed: %v", err)
	}
	if !log.HasTrace {
		t.Fatalf("has_trace = false, want true")
	}

	var trace models.UsageLogTrace
	if err := db.Where("request_id = ?", "req-trace-1").First(&trace).Error; err != nil {
		t.Fatalf("query usage log trace failed: %v", err)
	}
	if trace.InboundPath != "/v1/chat/completions" {
		t.Fatalf("inbound_path = %q, want %q", trace.InboundPath, "/v1/chat/completions")
	}
	if trace.OutboundPath != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("outbound_path = %q, want %q", trace.OutboundPath, "https://api.openai.com/v1/chat/completions")
	}
}

func TestSettleOne_OtherFieldPersisted(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "other-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)
	otherJSON := `{"relay_mode":"native","channel_type":1,"channel_name":"test-ch","passthrough_enabled":false}`
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-other-1",
			UserID:           1,
			TokenID:          1,
			ChannelID:        1,
			ModelName:        "gpt-4o",
			PromptTokens:     100,
			CompletionTokens: 50,
			Status:           1,
			Other:            otherJSON,
			Timestamp:        time.Now().Unix(),
		},
	})

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-other-1").First(&log).Error; err != nil {
		t.Fatalf("query usage log failed: %v", err)
	}
	if log.Other != otherJSON {
		t.Fatalf("other = %q, want %q", log.Other, otherJSON)
	}
}

func TestSettleOne_NoTrace(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "notrace-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-notrace-1",
			UserID:           1,
			TokenID:          1,
			ChannelID:        1,
			ModelName:        "gpt-4o",
			PromptTokens:     100,
			CompletionTokens: 50,
			Status:           1,
			TraceData:        "",
			Timestamp:        time.Now().Unix(),
		},
	})

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-notrace-1").First(&log).Error; err != nil {
		t.Fatalf("query usage log failed: %v", err)
	}
	if log.HasTrace {
		t.Fatalf("has_trace = true, want false")
	}
}

func TestSettler_PersistsErrorStageAndTimings(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "trace-fields-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "test-model", InputPrice: 0, OutputPrice: 0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)

	entry := protocol.UsageLogEntry{
		RequestID:          "req-trace-fields",
		UserID:             1,
		TokenID:            1,
		ModelName:          "test-model",
		IsStream:           false,
		Timestamp:          time.Now().Unix(),
		Status:             0,
		ErrorMessage:       "boom",
		ErrorStage:         "outbound_encode",
		InboundDecodeMs:    1,
		OutboundEncodeMs:   2,
		UpstreamDispatchMs: 100,
		UpstreamDecodeMs:   5,
		ClientEncodeMs:     3,
	}

	settler.Settle(context.Background(), "agent-test", []protocol.UsageLogEntry{entry})

	var got models.UsageLog
	if err := db.First(&got, "request_id = ?", "req-trace-fields").Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ErrorStage != "outbound_encode" {
		t.Errorf("ErrorStage = %q, want outbound_encode", got.ErrorStage)
	}
	if got.InboundDecodeMs != 1 || got.OutboundEncodeMs != 2 ||
		got.UpstreamDispatchMs != 100 || got.UpstreamDecodeMs != 5 ||
		got.ClientEncodeMs != 3 {
		t.Errorf("timings mismatch: got %+v", got)
	}
}

// TestSettler_TraceDataEmpty_NoTraceRow 验证 trace=off+success 场景：
//
//	entry 含 5 个 _ms / error_stage，但 TraceData 空 → 不写 UsageLogTrace 行、has_trace=false。
func TestSettler_TraceDataEmpty_NoTraceRow(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "trace-empty-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "test-model", InputPrice: 0, OutputPrice: 0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)

	entry := protocol.UsageLogEntry{
		RequestID:          "req-trace-empty",
		UserID:             1,
		TokenID:            1,
		ModelName:          "test-model",
		IsStream:           false,
		Timestamp:          time.Now().Unix(),
		Status:             1, // success
		ErrorStage:         "",
		InboundDecodeMs:    1,
		UpstreamDispatchMs: 100,
		// TraceData 故意留空
	}

	settler.Settle(context.Background(), "agent-test", []protocol.UsageLogEntry{entry})

	var got models.UsageLog
	if err := db.First(&got, "request_id = ?", "req-trace-empty").Error; err != nil {
		t.Fatalf("read back UsageLog: %v", err)
	}
	if got.UpstreamDispatchMs != 100 {
		t.Errorf("UpstreamDispatchMs = %d, want 100 (timing must always be saved)", got.UpstreamDispatchMs)
	}
	if got.HasTrace {
		t.Errorf("HasTrace = true, want false (TraceData was empty)")
	}

	// UsageLogTrace 行不应存在
	var traceCount int64
	db.Model(&models.UsageLogTrace{}).Where("request_id = ?", "req-trace-empty").Count(&traceCount)
	if traceCount != 0 {
		t.Errorf("UsageLogTrace rows = %d, want 0 (TraceData was empty)", traceCount)
	}
}

// TestSettler_TraceDataNonEmpty_FailedRequest 验证失败强制 verbose 场景：
//
//	entry.TraceData 非空 → 写 UsageLogTrace + has_trace=true。
func TestSettler_TraceDataNonEmpty_FailedRequest(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "trace-fail-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "test-model", InputPrice: 0, OutputPrice: 0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)

	// 构造一个合法的 TraceData JSON（与 TraceRecord.MarshalJSON 输出格式对齐）
	traceJSON := `{
		"inbound_path": "/v1/chat/completions",
		"outbound_path": "/v1/chat/completions",
		"inbound_headers": "{}",
		"outbound_headers": "{}",
		"inbound_body": "{\"model\":\"test-model\"}",
		"outbound_body": "{\"model\":\"test-model\"}",
		"response_headers": "{}",
		"response_body": "{\"error\":\"upstream boom\"}",
		"client_response_body": "{\"error\":\"upstream boom\"}",
		"upstream_status": 502
	}`

	entry := protocol.UsageLogEntry{
		RequestID:          "req-trace-fail",
		UserID:             1,
		TokenID:            1,
		ModelName:          "test-model",
		IsStream:           false,
		Timestamp:          time.Now().Unix(),
		Status:             0, // fail
		ErrorMessage:       "upstream 502",
		ErrorStage:         "upstream_status",
		InboundDecodeMs:    1,
		UpstreamDispatchMs: 50,
		TraceData:          traceJSON,
	}

	settler.Settle(context.Background(), "agent-test", []protocol.UsageLogEntry{entry})

	var got models.UsageLog
	if err := db.First(&got, "request_id = ?", "req-trace-fail").Error; err != nil {
		t.Fatalf("read back UsageLog: %v", err)
	}
	if got.ErrorStage != "upstream_status" {
		t.Errorf("ErrorStage = %q, want upstream_status", got.ErrorStage)
	}
	if !got.HasTrace {
		t.Errorf("HasTrace = false, want true (TraceData was filled)")
	}

	// UsageLogTrace 行应存在
	var trace models.UsageLogTrace
	if err := db.First(&trace, "request_id = ?", "req-trace-fail").Error; err != nil {
		t.Fatalf("read back UsageLogTrace: %v", err)
	}
	if trace.UpstreamStatus != 502 {
		t.Errorf("UsageLogTrace.UpstreamStatus = %d, want 502", trace.UpstreamStatus)
	}
}

func TestSettle_PersistsFallbackChainAndTraces(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "fb-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := newTestSettler(appProv, bus, logger)

	entry := protocol.UsageLogEntry{
		RequestID: "req-fb",
		UserID:    1,
		TokenID:   1,
		ChannelID: 1,
		ModelName: "gpt-4o",
		Status:    1,
		Timestamp: time.Now().Unix(),
		FallbackChain: []models.AttemptRecord{
			{Seq: 1, Status: "fail"},
			{Seq: 2, Status: "ok"},
		},
		AttemptTraces: []models.UsageLogTrace{
			{AttemptIndex: 0, UpstreamStatus: 503},
			{AttemptIndex: 1, UpstreamStatus: 200},
		},
	}

	settler.Settle(context.Background(), "agent-1", []protocol.UsageLogEntry{entry})

	daoCtx := dao.NewContext(appProv)
	q := dao.NewAdminQuery(daoCtx)

	log, err := q.UsageLog().GetByRequestID("req-fb")
	if err != nil {
		t.Fatalf("GetByRequestID: %v", err)
	}
	if len(log.FallbackChain) != 2 {
		t.Fatalf("want 2 chain entries persisted, got %d", len(log.FallbackChain))
	}
	if log.FallbackChain[0].Status != "fail" || log.FallbackChain[1].Status != "ok" {
		t.Fatalf("chain entries wrong: %+v", log.FallbackChain)
	}

	traces, err := q.UsageLog().GetTracesByRequestID("req-fb")
	if err != nil {
		t.Fatalf("GetTracesByRequestID: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("want 2 traces persisted, got %d", len(traces))
	}
	if traces[0].AttemptIndex != 0 || traces[0].UpstreamStatus != 503 {
		t.Fatalf("trace[0] wrong: %+v", traces[0])
	}
	if traces[1].AttemptIndex != 1 || traces[1].UpstreamStatus != 200 {
		t.Fatalf("trace[1] wrong: %+v", traces[1])
	}
	if !log.HasTrace {
		t.Fatalf("has_trace = false, want true when AttemptTraces present")
	}
}

func TestSettleBatch_ReturnsErrorButSettlesRest(t *testing.T) {
	t.Run("success_all_settle_returns_nil_error", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		bus := eventbus.NewMemoryBus()
		logger := zap.NewNop()

		db.Create(&models.User{Username: "batch-ok-u", Password: "x", Role: 1, Status: 1, Quota: 10000})
		db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

		settler := newTestSettler(appProv, bus, logger)
		err := settler.SettleBatch(context.Background(), "agent-batch", []protocol.UsageLogEntry{
			{
				RequestID:        "req-batch-1",
				UserID:           1,
				TokenID:          1,
				ChannelID:        1,
				ModelName:        "gpt-4o",
				PromptTokens:     100,
				CompletionTokens: 50,
				Status:           1,
				Timestamp:        time.Now().Unix(),
			},
			{
				RequestID:        "req-batch-2",
				UserID:           1,
				TokenID:          1,
				ChannelID:        1,
				ModelName:        "gpt-4o",
				PromptTokens:     200,
				CompletionTokens: 75,
				Status:           1,
				Timestamp:        time.Now().Unix(),
			},
		})
		require.NoError(t, err, "all entries settle cleanly → nil error")

		var count int64
		db.Model(&models.UsageLog{}).Where("request_id IN ?", []string{"req-batch-1", "req-batch-2"}).Count(&count)
		require.Equal(t, int64(2), count, "both entries must be persisted")
	})

	// behavior change: request-log persistence is weakly reliable and cannot
	// turn a committed billing fact into an ingest failure.
	t.Run("dropped_log_table_keeps_committed_billing_successful", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		bus := eventbus.NewMemoryBus()
		logger := zap.NewNop()

		db.Create(&models.User{Username: "batch-fail-u", Password: "x", Role: 1, Status: 1, Quota: 10000})
		db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

		// 模拟结算期基础设施故障(如 rebuild 长事务持锁 → SQLITE_BUSY):drop
		// usage_logs 表让每条 Create 都失败。
		require.NoError(t, db.Migrator().DropTable(&models.UsageLog{}))

		settler := newTestSettler(appProv, bus, logger)
		err := settler.SettleBatch(context.Background(), "agent-batch-fail", []protocol.UsageLogEntry{
			{
				RequestID:        "req-batch-fail-1",
				UserID:           1,
				TokenID:          1,
				ChannelID:        1,
				ModelName:        "gpt-4o",
				PromptTokens:     100,
				CompletionTokens: 50,
				Status:           1,
				Timestamp:        time.Now().Unix(),
			},
			{
				RequestID:        "req-batch-fail-2",
				UserID:           1,
				TokenID:          1,
				ChannelID:        1,
				ModelName:        "gpt-4o",
				PromptTokens:     200,
				CompletionTokens: 75,
				Status:           1,
				Timestamp:        time.Now().Unix(),
			},
		})
		require.NoError(t, err)
		var count int64
		require.NoError(t, db.Model(&models.BillingLog{}).Where("request_id LIKE ?", "req-batch-fail-%").Count(&count).Error)
		require.Equal(t, int64(2), count)
	})

	t.Run("void_wrapper_does_not_panic_on_failure", func(t *testing.T) {
		db, appProv := setupTestDB(t)
		bus := eventbus.NewMemoryBus()
		logger := zap.NewNop()

		db.Create(&models.User{Username: "batch-void-u", Password: "x", Role: 1, Status: 1, Quota: 10000})
		db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})
		require.NoError(t, db.Migrator().DropTable(&models.UsageLog{}))

		settler := newTestSettler(appProv, bus, logger)
		require.NotPanics(t, func() {
			settler.Settle(context.Background(), "agent-batch-void", []protocol.UsageLogEntry{
				{
					RequestID:        "req-batch-void-1",
					UserID:           1,
					TokenID:          1,
					ChannelID:        1,
					ModelName:        "gpt-4o",
					PromptTokens:     100,
					CompletionTokens: 50,
					Status:           1,
					Timestamp:        time.Now().Unix(),
				},
			})
		})
	})
}
