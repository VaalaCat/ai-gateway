package billing

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// TestSettleOne_FreeChannel_ZeroCostRecordsTokensAndRaw 验证免费渠道:
//   - 四桶实付清零、total_cost=0、Free=true、BillingFactor=0
//   - 原价四桶(Raw*)落库 = 乘因子前的全价(便于弹窗展示 ×0 公式)
//   - token 照记
func TestSettleOne_FreeChannel_ZeroCostRecordsTokensAndRaw(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "alice", Password: "x", Role: 1, Status: 1, Quota: 1000000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{{
		RequestID: "free-req-1", UserID: 1, OwnerType: "admin", ChannelID: 7,
		ModelName: "gpt-4o", PromptTokens: 100, CompletionTokens: 50,
		Free: true, Status: 1, Timestamp: time.Now().Unix(),
	}})

	var ul models.UsageLog
	if err := db.Where("request_id = ?", "free-req-1").First(&ul).Error; err != nil {
		t.Fatalf("usage_log not written: %v", err)
	}
	if ul.TotalCost != 0 || ul.InputCost != 0 || ul.OutputCost != 0 {
		t.Fatalf("free channel must zero costs; got total=%d input=%d output=%d", ul.TotalCost, ul.InputCost, ul.OutputCost)
	}
	if !ul.Free {
		t.Fatalf("ul.Free = false, want true")
	}
	if ul.BillingFactor == nil || *ul.BillingFactor != 0 {
		t.Fatalf("BillingFactor = %v, want 0", ul.BillingFactor)
	}
	if ul.PromptTokens != 100 || ul.CompletionTokens != 50 {
		t.Fatalf("tokens not recorded: prompt=%d completion=%d", ul.PromptTokens, ul.CompletionTokens)
	}
	// 原价: 100*5/1e6*1e5=50, 50*15/1e6*1e5=75
	if ul.RawInputCost == nil || *ul.RawInputCost != 50 || ul.RawOutputCost == nil || *ul.RawOutputCost != 75 {
		t.Fatalf("raw costs wrong: in=%v out=%v, want 50/75", ul.RawInputCost, ul.RawOutputCost)
	}
}

// TestSettleOne_AdminRatio_PersistsRawAndFactor 验证普通倍率行:
//   - Raw* = 原价(乘因子前) > 实付; BillingFactor == 生效倍率; 四桶实付相加 == total
func TestSettleOne_AdminRatio_PersistsRawAndFactor(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "bob", Password: "x", Role: 1, Status: 1, Quota: 1000000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{{
		RequestID: "ratio-raw-1", UserID: 1, OwnerType: "admin", ChannelID: 5,
		ModelName: "gpt-4o", PromptTokens: 100, CompletionTokens: 50,
		PriceRatio: 0.5, Status: 1, Timestamp: time.Now().Unix(),
	}})

	var ul models.UsageLog
	if err := db.Where("request_id = ?", "ratio-raw-1").First(&ul).Error; err != nil {
		t.Fatalf("usage_log not written: %v", err)
	}
	if ul.RawInputCost == nil || *ul.RawInputCost != 50 || ul.RawOutputCost == nil || *ul.RawOutputCost != 75 {
		t.Fatalf("raw costs = in:%v out:%v, want 50/75 (pre-ratio)", ul.RawInputCost, ul.RawOutputCost)
	}
	if ul.BillingFactor == nil || *ul.BillingFactor != 0.5 {
		t.Fatalf("BillingFactor = %v, want 0.5", ul.BillingFactor)
	}
	if ul.InputCost+ul.OutputCost+ul.CacheReadCost+ul.CacheWriteCost != ul.TotalCost {
		t.Fatalf("four buckets must sum to total: %d+%d+%d+%d != %d",
			ul.InputCost, ul.OutputCost, ul.CacheReadCost, ul.CacheWriteCost, ul.TotalCost)
	}
}
