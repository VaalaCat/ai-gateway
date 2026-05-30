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

// TestApplyChannelPriceRatio_Discount 验证 0.8 折逐桶生效,total = 四桶之和。
func TestApplyChannelPriceRatio_Discount(t *testing.T) {
	in, out, cr, cw, total, ratio := applyChannelPriceRatio(
		protocol.UsageLogEntry{PriceRatio: 0.8}, 100, 200, 300, 400)
	if ratio != 0.8 {
		t.Fatalf("ratio = %v, want 0.8", ratio)
	}
	if in != 80 || out != 160 || cr != 240 || cw != 320 {
		t.Fatalf("buckets = %d/%d/%d/%d, want 80/160/240/320", in, out, cr, cw)
	}
	if total != in+out+cr+cw {
		t.Fatalf("total = %d, want %d (sum of buckets)", total, in+out+cr+cw)
	}
}

// TestApplyChannelPriceRatio_ZeroIsFullPrice 验证 ratio=0(旧 agent/未配)归一到 1.0,成本不变。
func TestApplyChannelPriceRatio_ZeroIsFullPrice(t *testing.T) {
	in, out, cr, cw, total, ratio := applyChannelPriceRatio(
		protocol.UsageLogEntry{PriceRatio: 0}, 100, 200, 300, 400)
	if ratio != 1.0 {
		t.Fatalf("ratio = %v, want 1.0 (0 means full price)", ratio)
	}
	if in != 100 || out != 200 || cr != 300 || cw != 400 || total != 1000 {
		t.Fatalf("zero should keep full costs; got %d/%d/%d/%d total=%d", in, out, cr, cw, total)
	}
}

// TestApplyChannelPriceRatio_TotalClosed 防漂移:逐桶截断后 total 必须 == 四桶和,
// 而非单独对原 total 打折再截断。inputs 选得让两种算法差 1。
func TestApplyChannelPriceRatio_TotalClosed(t *testing.T) {
	in, out, cr, cw, total, _ := applyChannelPriceRatio(
		protocol.UsageLogEntry{PriceRatio: 0.1}, 999, 1000, 1001, 1002)
	if got, want := in+out+cr+cw, int64(399); got != want {
		t.Fatalf("per-bucket sum = %d, want %d", got, want)
	}
	if total != in+out+cr+cw {
		t.Fatalf("total = %d must close against per-bucket sum %d", total, in+out+cr+cw)
	}
}

// TestSettleOne_AdminChannelRatio_Discounted 端到端验证公共 channel 0.5 折:
// total_cost 减半且 UsageLog.PriceRatio 记 0.5。
func TestSettleOne_AdminChannelRatio_Discounted(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "alice", Password: "x", Role: 1, Status: 1, Quota: 1000000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{{
		RequestID: "ratio-req-1", UserID: 1, OwnerType: "admin", ChannelID: 5,
		ModelName: "gpt-4o", PromptTokens: 100, CompletionTokens: 50,
		PriceRatio: 0.5, Status: 1, Timestamp: time.Now().Unix(),
	}})

	var ul models.UsageLog
	if err := db.Where("request_id = ?", "ratio-req-1").First(&ul).Error; err != nil {
		t.Fatalf("usage_log not written: %v", err)
	}
	// 全价: 100*5/1e6*1e5 + 50*15/1e6*1e5 = 50 + 75 = 125; 0.5 折 = 25 + 37 = 62
	if ul.TotalCost != 62 {
		t.Fatalf("total_cost = %d, want 62 (0.5 of full 125, per-bucket truncated)", ul.TotalCost)
	}
	if ul.PriceRatio != 0.5 {
		t.Fatalf("price_ratio recorded = %v, want 0.5", ul.PriceRatio)
	}
}

// TestSettleOne_AdminChannelRatio_ZeroIsFullPrice 验证 ratio=0(旧 agent/未配)按全价计费,
// 记 price_ratio=1,正常扣 quota。
func TestSettleOne_AdminChannelRatio_ZeroIsFullPrice(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "bob", Password: "x", Role: 1, Status: 1, Quota: 1000000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})

	settler := NewSettler(appProv, bus, logger)
	// 不设 PriceRatio,零值 0 → 全价(与旧 agent 行为一致)。
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{{
		RequestID: "ratio-zero-1", UserID: 1, OwnerType: "admin", ChannelID: 5,
		ModelName: "gpt-4o", PromptTokens: 100, CompletionTokens: 50,
		Status: 1, Timestamp: time.Now().Unix(),
	}})

	var ul models.UsageLog
	if err := db.Where("request_id = ?", "ratio-zero-1").First(&ul).Error; err != nil {
		t.Fatalf("usage_log not written: %v", err)
	}
	// 全价: 100*5/1e6*1e5 + 50*15/1e6*1e5 = 50 + 75 = 125
	if ul.TotalCost != 125 {
		t.Fatalf("total_cost = %d, want 125 (ratio 0 = full price)", ul.TotalCost)
	}
	if ul.PriceRatio != 1 {
		t.Fatalf("price_ratio recorded = %v, want 1 (normalized from 0)", ul.PriceRatio)
	}
	var u models.User
	db.First(&u, 1)
	if u.Quota == 1000000 {
		t.Fatalf("quota should be deducted at full price; still %d", u.Quota)
	}
}
