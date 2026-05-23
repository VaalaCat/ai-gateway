package billing

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

// testAppProvider wraps *gorm.DB to satisfy dao.AppProvider.
type testAppProvider struct{ db *gorm.DB }

func (p *testAppProvider) GetDB() *gorm.DB { return p.db }

func setupTestDB(t *testing.T) (*gorm.DB, *testAppProvider) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models.AutoMigrate(db)
	return db, &testAppProvider{db: db}
}

// mockAggregator records every Submit invocation so tests can assert on
// post-commit handoff semantics without exercising real dao writes.
type mockAggregator struct {
	mu      sync.Mutex
	submits []models.UsageLog
}

func (m *mockAggregator) Submit(log *models.UsageLog) {
	if log == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submits = append(m.submits, *log)
}

func (m *mockAggregator) snapshot() []models.UsageLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]models.UsageLog, len(m.submits))
	copy(cp, m.submits)
	return cp
}

// syncAggregator drives the three dao upserts synchronously per-Submit. It
// preserves the pre-T2.8 per-log inline rollup behavior for tests whose
// assertions read token_daily_billings / channel_daily_billings rows directly.
// Production code uses the real *billing.Aggregator (batched flush).
type syncAggregator struct {
	app dao.AppProvider
}

func (s *syncAggregator) Submit(log *models.UsageLog) {
	if log == nil {
		return
	}
	m := dao.NewAdminMutation(dao.NewContext(s.app))
	_ = m.Billing().UpsertTokenDaily(log)
	_ = m.Billing().UpsertChannelDaily(log)
	_ = m.Billing().UpsertHourlyBucket(log)
}

func TestSettleUsage(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	// Setup: user with quota 10000, model pricing
	db.Create(&models.User{Username: "test", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)

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

func TestQuotaDepletion(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	// User with very small quota
	db.Create(&models.User{Username: "poor", Password: "x", Role: 1, Status: 1, Quota: 1})
	db.Create(&models.Token{UserID: 1, Key: "sk-poor", Name: "t1", Status: 1, ExpiredAt: -1})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)
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

	settler := NewSettler(appProv, bus, logger)
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

	settler := NewSettler(appProv, bus, logger)
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

	settler := NewSettler(appProv, bus, logger)
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

	settler := NewSettler(appProv, bus, logger)
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

	settler := NewSettler(appProv, bus, logger)
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

	settler := NewSettler(appProv, bus, logger)
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

	settler := NewSettler(appProv, bus, logger)
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

func TestSettler_WritesBillingRollups(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "billing-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.Token{UserID: 1, Key: "sk-billing", Name: "primary-key", Status: 1, ExpiredAt: -1})
	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "openai-primary", Type: 1, Status: 1}, Key: "sk-upstream"})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := NewSettlerWithAggregator(appProv, bus, logger, &syncAggregator{app: appProv})
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:        "req-rollup-1",
			UserID:           1,
			TokenID:          1,
			TokenName:        "primary-key",
			ChannelID:        1,
			ModelName:        "gpt-4o",
			PromptTokens:     1000,
			CompletionTokens: 500,
			Status:           1,
			Other:            `{"channel_type":1,"channel_name":"openai-primary"}`,
			Timestamp:        time.Now().Unix(),
		},
	})

	var log models.UsageLog
	if err := db.Where("request_id = ?", "req-rollup-1").First(&log).Error; err != nil {
		t.Fatalf("query usage log failed: %v", err)
	}
	if log.ChannelName != "openai-primary" {
		t.Fatalf("channel_name = %q, want %q", log.ChannelName, "openai-primary")
	}
	if log.ChannelType != 1 {
		t.Fatalf("channel_type = %d, want 1", log.ChannelType)
	}

	var tokenDaily models.TokenDailyBilling
	if err := db.Where("token_id = ?", 1).First(&tokenDaily).Error; err != nil {
		t.Fatalf("query token daily billing failed: %v", err)
	}
	if tokenDaily.TokenName != "primary-key" {
		t.Fatalf("token_name = %q, want %q", tokenDaily.TokenName, "primary-key")
	}
	if tokenDaily.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", tokenDaily.RequestCount)
	}
	if tokenDaily.SuccessCount != 1 {
		t.Fatalf("success_count = %d, want 1", tokenDaily.SuccessCount)
	}
	if tokenDaily.TotalCost != log.TotalCost {
		t.Fatalf("total_cost = %d, want %d", tokenDaily.TotalCost, log.TotalCost)
	}

	var channelDaily models.ChannelDailyBilling
	if err := db.Where("channel_id = ?", 1).First(&channelDaily).Error; err != nil {
		t.Fatalf("query channel daily billing failed: %v", err)
	}
	if channelDaily.ChannelName != "openai-primary" {
		t.Fatalf("channel_name = %q, want %q", channelDaily.ChannelName, "openai-primary")
	}
	if channelDaily.ChannelType != 1 {
		t.Fatalf("channel_type = %d, want 1", channelDaily.ChannelType)
	}
	if channelDaily.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", channelDaily.RequestCount)
	}
	if channelDaily.SuccessCount != 1 {
		t.Fatalf("success_count = %d, want 1", channelDaily.SuccessCount)
	}
	if channelDaily.TotalCost != log.TotalCost {
		t.Fatalf("total_cost = %d, want %d", channelDaily.TotalCost, log.TotalCost)
	}
}

func TestSettler_TracksFailedRequests(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "billing-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.Token{UserID: 1, Key: "sk-billing", Name: "primary-key", Status: 1, ExpiredAt: -1})
	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "openai-primary", Type: 1, Status: 1}, Key: "sk-upstream"})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := NewSettlerWithAggregator(appProv, bus, logger, &syncAggregator{app: appProv})
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{
		{
			RequestID:    "req-rollup-failed-1",
			UserID:       1,
			TokenID:      1,
			TokenName:    "primary-key",
			ChannelID:    1,
			ModelName:    "gpt-4o",
			Status:       0,
			ErrorMessage: "upstream timeout",
			Other:        `{"channel_type":1,"channel_name":"openai-primary"}`,
			Timestamp:    time.Now().Unix(),
		},
	})

	var tokenDaily models.TokenDailyBilling
	if err := db.Where("token_id = ?", 1).First(&tokenDaily).Error; err != nil {
		t.Fatalf("query token daily billing failed: %v", err)
	}
	if tokenDaily.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", tokenDaily.RequestCount)
	}
	if tokenDaily.SuccessCount != 0 {
		t.Fatalf("success_count = %d, want 0", tokenDaily.SuccessCount)
	}
	if tokenDaily.FailedCount != 1 {
		t.Fatalf("failed_count = %d, want 1", tokenDaily.FailedCount)
	}

	var channelDaily models.ChannelDailyBilling
	if err := db.Where("channel_id = ?", 1).First(&channelDaily).Error; err != nil {
		t.Fatalf("query channel daily billing failed: %v", err)
	}
	if channelDaily.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", channelDaily.RequestCount)
	}
	if channelDaily.SuccessCount != 0 {
		t.Fatalf("success_count = %d, want 0", channelDaily.SuccessCount)
	}
	if channelDaily.FailedCount != 1 {
		t.Fatalf("failed_count = %d, want 1", channelDaily.FailedCount)
	}
}

func TestSettler_PersistsErrorStageAndTimings(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "trace-fields-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "test-model", InputPrice: 0, OutputPrice: 0, Status: 1})

	settler := NewSettler(appProv, bus, logger)

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

	settler := NewSettler(appProv, bus, logger)

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

	settler := NewSettler(appProv, bus, logger)

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

func TestSettler_IgnoresDuplicateRequestID(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "billing-user", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.Token{UserID: 1, Key: "sk-billing", Name: "primary-key", Status: 1, ExpiredAt: -1})
	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "openai-primary", Type: 1, Status: 1}, Key: "sk-upstream"})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	entry := protocol.UsageLogEntry{
		RequestID:        "req-rollup-dup-1",
		UserID:           1,
		TokenID:          1,
		TokenName:        "primary-key",
		ChannelID:        1,
		ModelName:        "gpt-4o",
		PromptTokens:     1000,
		CompletionTokens: 500,
		Status:           1,
		Other:            `{"channel_type":1,"channel_name":"openai-primary"}`,
		Timestamp:        time.Now().Unix(),
	}

	settler := NewSettlerWithAggregator(appProv, bus, logger, &syncAggregator{app: appProv})
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{entry})
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{entry})

	var logCount int64
	if err := db.Model(&models.UsageLog{}).Where("request_id = ?", entry.RequestID).Count(&logCount).Error; err != nil {
		t.Fatalf("count usage logs failed: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("usage_logs = %d, want 1", logCount)
	}

	var tokenRollupCount int64
	if err := db.Model(&models.TokenDailyBilling{}).Where("token_id = ?", 1).Count(&tokenRollupCount).Error; err != nil {
		t.Fatalf("count token daily billing failed: %v", err)
	}
	if tokenRollupCount != 1 {
		t.Fatalf("token_daily_billings = %d, want 1", tokenRollupCount)
	}

	var tokenDaily models.TokenDailyBilling
	if err := db.Where("token_id = ?", 1).First(&tokenDaily).Error; err != nil {
		t.Fatalf("query token daily billing failed: %v", err)
	}
	if tokenDaily.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", tokenDaily.RequestCount)
	}

	var channelRollupCount int64
	if err := db.Model(&models.ChannelDailyBilling{}).Where("channel_id = ?", 1).Count(&channelRollupCount).Error; err != nil {
		t.Fatalf("count channel daily billing failed: %v", err)
	}
	if channelRollupCount != 1 {
		t.Fatalf("channel_daily_billings = %d, want 1", channelRollupCount)
	}

	var channelDaily models.ChannelDailyBilling
	if err := db.Where("channel_id = ?", 1).First(&channelDaily).Error; err != nil {
		t.Fatalf("query channel daily billing failed: %v", err)
	}
	if channelDaily.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", channelDaily.RequestCount)
	}
}

// TestSettler_SubmitsToAggregatorAfterCommit 验证 T2.8 的核心契约：settler
// 事务提交后才把 UsageLog 交给注入的 UsageAggregator，且重复 request_id
// 不会重复 Submit（去重短路在事务内 return nil 时 inserted 仍为 false）。
func TestSettler_SubmitsToAggregatorAfterCommit(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger := zap.NewNop()

	db.Create(&models.User{Username: "agg-u", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	mockAgg := &mockAggregator{}
	settler := NewSettlerWithAggregator(appProv, bus, logger, mockAgg)

	// success: 第一次 settle → aggregator 收到 1 条
	settler.Settle(context.Background(), "agent-x", []protocol.UsageLogEntry{{
		RequestID:        "req-agg-1",
		UserID:           1,
		TokenID:          1,
		ChannelID:        1,
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 50,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})
	submits := mockAgg.snapshot()
	require.Len(t, submits, 1, "first settle should Submit once")
	require.Equal(t, "req-agg-1", submits[0].RequestID)
	require.Equal(t, uint(1), submits[0].UserID)

	// 同 RequestID 第二次 → 去重，aggregator NOT 再 Submit
	settler.Settle(context.Background(), "agent-x", []protocol.UsageLogEntry{{
		RequestID:        "req-agg-1",
		UserID:           1,
		TokenID:          1,
		ChannelID:        1,
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 50,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})
	require.Len(t, mockAgg.snapshot(), 1, "duplicate RequestID must NOT re-Submit")

	// ownerless usage 仍 Submit（聚合不区分 owner-less，只关心是否成功落 UsageLog）
	settler.Settle(context.Background(), "agent-x", []protocol.UsageLogEntry{{
		RequestID:        "req-agg-2",
		UserID:           0,
		TokenID:          1,
		ChannelID:        1,
		ModelName:        "gpt-4o",
		PromptTokens:     50,
		CompletionTokens: 0,
		Status:           1,
		TokenName:        "anon",
		Timestamp:        time.Now().Unix(),
	}})
	submits = mockAgg.snapshot()
	require.Len(t, submits, 2, "ownerless usage must also Submit")
	require.Equal(t, "req-agg-2", submits[1].RequestID)
	require.Equal(t, uint(0), submits[1].UserID)
}

// TestSettler_BehaviorEquivalentToLegacy 验证 T2 重构核心契约：把同一批
// UsageLogEntry 跑两遍——
//
//	Path A: settler + *billing.Aggregator (T2.6 批量 flush)
//	Path B: settler + syncAggregator      (T2.8 legacy 行为, 每条 log 即时调 dao 单行 upsert)
//
// flush 之后比较 token_daily_billings / channel_daily_billings /
// usage_hourly_buckets 三张 rollup 表逐行相等 (zero-out 掉自动时间戳)。
func TestSettler_BehaviorEquivalentToLegacy(t *testing.T) {
	logger := zap.NewNop()

	// --- Path A: aggregator-based ---
	dbA, appA := setupTestDB(t)
	busA := eventbus.NewMemoryBus()

	dbA.Create(&models.User{Username: "ua", Password: "x", Role: 1, Status: 1, Quota: 1_000_000})
	dbA.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	agg := NewAggregator(appA, zap.NewNop(), AggregatorOptions{})
	agg.SetFlushFns(
		func(rows []dao.TokenDailyRow) error {
			return dao.NewAdminMutation(dao.NewContext(appA)).Billing().BatchUpsertTokenDaily(rows)
		},
		func(rows []dao.ChannelDailyRow) error {
			return dao.NewAdminMutation(dao.NewContext(appA)).Billing().BatchUpsertChannelDaily(rows)
		},
		func(rows []dao.HourlyBucketRow) error {
			return dao.NewAdminMutation(dao.NewContext(appA)).Billing().BatchUpsertHourlyBucket(rows)
		},
	)
	settlerA := NewSettlerWithAggregator(appA, busA, logger, agg)

	// --- Path B: legacy (syncAggregator drives per-log UpsertXxx) ---
	dbB, appB := setupTestDB(t)
	busB := eventbus.NewMemoryBus()
	dbB.Create(&models.User{Username: "ub", Password: "x", Role: 1, Status: 1, Quota: 1_000_000})
	dbB.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settlerB := NewSettlerWithAggregator(appB, busB, logger, &syncAggregator{app: appB})

	// --- Mixed inputs covering edge cases ---
	// 用固定的 ts 避免两次跑出现在不同分钟/小时窗口的边界 flake。
	now := time.Now().Unix()
	entries := []protocol.UsageLogEntry{
		// success stream
		{
			RequestID: "r-stream-success", UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-4o",
			PromptTokens: 100, CompletionTokens: 50, Status: 1, IsStream: true,
			FirstResponseMs: 300, Duration: 2200,
			InboundDecodeMs: 5, UpstreamDispatchMs: 6, UpstreamDecodeMs: 7, OutboundEncodeMs: 8, ClientEncodeMs: 9,
			Timestamp: now,
		},
		// failed
		{
			RequestID: "r-failed", UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-4o",
			PromptTokens: 50, Status: 0,
			Timestamp: now,
		},
		// non-stream success
		{
			RequestID: "r-nonstream", UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-4o",
			PromptTokens: 200, CompletionTokens: 100, Status: 1, IsStream: false,
			InboundDecodeMs: 10, OutboundEncodeMs: 20,
			Timestamp: now,
		},
		// different token same user
		{
			RequestID: "r-other-token", UserID: 1, TokenID: 2, ChannelID: 1, ModelName: "gpt-4o",
			PromptTokens: 75, CompletionTokens: 25, Status: 1,
			Timestamp: now,
		},
		// different channel same user/token
		{
			RequestID: "r-other-channel", UserID: 1, TokenID: 1, ChannelID: 2, ModelName: "gpt-4o",
			PromptTokens: 30, CompletionTokens: 15, Status: 1,
			Timestamp: now,
		},
	}

	settlerA.Settle(context.Background(), "agent-x", entries)
	settlerB.Settle(context.Background(), "agent-x", entries)

	// Path A: flush aggregator into DB
	require.NoError(t, agg.Flush())

	// --- Compare each rollup table row-by-row ---
	var tokA, tokB []models.TokenDailyBilling
	require.NoError(t, dbA.Order("date, user_id, token_id").Find(&tokA).Error)
	require.NoError(t, dbB.Order("date, user_id, token_id").Find(&tokB).Error)
	require.Equal(t, len(tokB), len(tokA), "token_daily row count")
	for i := range tokB {
		// Ignore CreatedAt/UpdatedAt: autoCreateTime/autoUpdateTime differ
		// across the two sequential settle calls; behavior equivalence is
		// about counter math + dimension keys, not wall-clock.
		// Also zero ID since the two DBs are independent.
		tokA[i].ID, tokB[i].ID = 0, 0
		tokA[i].CreatedAt, tokB[i].CreatedAt = 0, 0
		tokA[i].UpdatedAt, tokB[i].UpdatedAt = 0, 0
		require.Equal(t, tokB[i], tokA[i], "token_daily row %d", i)
	}

	var chA, chB []models.ChannelDailyBilling
	require.NoError(t, dbA.Order("date, channel_id, private_channel_id").Find(&chA).Error)
	require.NoError(t, dbB.Order("date, channel_id, private_channel_id").Find(&chB).Error)
	require.Equal(t, len(chB), len(chA), "channel_daily row count")
	for i := range chB {
		chA[i].ID, chB[i].ID = 0, 0
		chA[i].CreatedAt, chB[i].CreatedAt = 0, 0
		chA[i].UpdatedAt, chB[i].UpdatedAt = 0, 0
		require.Equal(t, chB[i], chA[i], "channel_daily row %d", i)
	}

	var hA, hB []models.UsageHourlyBucket
	require.NoError(t, dbA.Order("date, hour, channel_id, private_channel_id, model_name, agent_id").Find(&hA).Error)
	require.NoError(t, dbB.Order("date, hour, channel_id, private_channel_id, model_name, agent_id").Find(&hB).Error)
	require.Equal(t, len(hB), len(hA), "hourly_bucket row count")
	for i := range hB {
		hA[i].ID, hB[i].ID = 0, 0
		hA[i].CreatedAt, hB[i].CreatedAt = 0, 0
		hA[i].UpdatedAt, hB[i].UpdatedAt = 0, 0
		require.Equal(t, hB[i], hA[i], "hourly_bucket row %d", i)
	}
}

// TestSettler_NilAggregatorFallsBackToNoop 验证 NewSettlerWithAggregator(nil)
// 不会 panic：构造函数会把 nil 替换为 noopAggregator。
func TestSettler_NilAggregatorFallsBackToNoop(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger := zap.NewNop()

	db.Create(&models.User{Username: "nil-agg-u", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := NewSettlerWithAggregator(appProv, bus, logger, nil)
	require.NotNil(t, settler.Aggregator, "nil aggregator must fall back to noopAggregator")

	// settle 不应 panic
	settler.Settle(context.Background(), "agent-x", []protocol.UsageLogEntry{{
		RequestID:        "req-nil-agg-1",
		UserID:           1,
		TokenID:          1,
		ChannelID:        1,
		ModelName:        "gpt-4o",
		PromptTokens:     10,
		CompletionTokens: 5,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})

	var count int64
	db.Model(&models.UsageLog{}).Where("request_id = ?", "req-nil-agg-1").Count(&count)
	require.Equal(t, int64(1), count, "usage log should still be created")
}
