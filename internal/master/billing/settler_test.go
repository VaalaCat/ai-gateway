package billing

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
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
	db.Create(&models.Channel{Name: "openai-primary", Type: 1, Key: "sk-upstream", Status: 1})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)
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
	db.Create(&models.Channel{Name: "openai-primary", Type: 1, Key: "sk-upstream", Status: 1})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5, OutputPrice: 10.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)
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
	db.Create(&models.Channel{Name: "openai-primary", Type: 1, Key: "sk-upstream", Status: 1})
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

	settler := NewSettler(appProv, bus, logger)
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
