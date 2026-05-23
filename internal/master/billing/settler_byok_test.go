package billing

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TestSettleOne_PrivateFreeMode_ZeroCostWritesDaily 验证 free 模式：
//   - cost 全归零、quota 不扣
//   - usage_log 仍写、daily 表仍写（让 BYOK 用户在 portal 看到自己的
//     request/token 计数；cost 字段是 0）
//
// 这是 review note #5 的对比测试——之前 free 模式跳过日表写入导致
// portal billing 页面失明，本测试是行为契约的回归守护。
func TestSettleOne_PrivateFreeMode_ZeroCostWritesDaily(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "alice", Password: "x", Role: 1, Status: 1, Quota: 10000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})
	db.Create(&models.Setting{Key: "byok_billing_mode", Value: "free"})

	initialQuota := getByokUserQuota(t, db, 1)

	settler := NewSettlerWithAggregator(appProv, bus, logger, &syncAggregator{app: appProv})
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{{
		RequestID:        "byok-free-req-1",
		UserID:           1,
		OwnerType:        "private",
		PrivateChannelID: 42,
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 50,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})

	// User quota must NOT be deducted in free mode (cost=0 → if totalCost > 0 fails)
	if getByokUserQuota(t, db, 1) != initialQuota {
		t.Fatalf("quota was deducted in free mode: want %d, got %d", initialQuota, getByokUserQuota(t, db, 1))
	}

	// usage_log row must be written with correct owner fields and zero cost
	var ul models.UsageLog
	if err := db.Where("request_id = ?", "byok-free-req-1").First(&ul).Error; err != nil {
		t.Fatalf("usage_log not written: %v", err)
	}
	if ul.OwnerType != "private" {
		t.Fatalf("owner_type = %q, want \"private\"", ul.OwnerType)
	}
	if ul.PrivateChannelID != 42 {
		t.Fatalf("private_channel_id = %d, want 42", ul.PrivateChannelID)
	}
	if ul.TotalCost != 0 {
		t.Fatalf("free mode should set total_cost to 0; got %d", ul.TotalCost)
	}

	// Daily billing rows MUST be written even in free mode — they carry zero cost
	// but non-zero request_count / token_count for portal stats.
	var tokenDaily models.TokenDailyBilling
	if err := db.Where("user_id = ?", 1).First(&tokenDaily).Error; err != nil {
		t.Fatalf("free mode must still write token_daily_billings: %v", err)
	}
	if tokenDaily.RequestCount != 1 {
		t.Fatalf("token_daily request_count = %d, want 1", tokenDaily.RequestCount)
	}
	if tokenDaily.TotalCost != 0 {
		t.Fatalf("token_daily total_cost = %d, want 0 in free mode", tokenDaily.TotalCost)
	}

	var channelDaily models.ChannelDailyBilling
	if err := db.Where("private_channel_id = ? AND owner_type = ?", 42, "private").First(&channelDaily).Error; err != nil {
		t.Fatalf("free mode must still write channel_daily_billings for BYOK row: %v", err)
	}
	if channelDaily.RequestCount != 1 {
		t.Fatalf("channel_daily request_count = %d, want 1", channelDaily.RequestCount)
	}
	if channelDaily.TotalCost != 0 {
		t.Fatalf("channel_daily total_cost = %d, want 0 in free mode", channelDaily.TotalCost)
	}
}

func TestSettleOne_PrivateServiceFeeMode_DiscountedCost(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "alice", Password: "x", Role: 1, Status: 1, Quota: 100000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})
	db.Create(&models.Setting{Key: "byok_billing_mode", Value: "service_fee"})
	db.Create(&models.Setting{Key: "byok_service_fee_ratio", Value: "0.1"})

	settler := NewSettlerWithAggregator(appProv, bus, logger, &syncAggregator{app: appProv})
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{{
		RequestID:        "byok-svc-req-1",
		UserID:           1,
		OwnerType:        "private",
		PrivateChannelID: 42,
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 50,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})

	var ul models.UsageLog
	if err := db.Where("request_id = ?", "byok-svc-req-1").First(&ul).Error; err != nil {
		t.Fatalf("usage_log not written: %v", err)
	}
	if ul.OwnerType != "private" {
		t.Fatalf("owner_type = %q, want \"private\"", ul.OwnerType)
	}
	if ul.PrivateChannelID != 42 {
		t.Fatalf("private_channel_id = %d, want 42", ul.PrivateChannelID)
	}

	// Admin cost calculation: 100*5/1_000_000*100_000 + 50*15/1_000_000*100_000 = 50 + 75 = 125
	// service_fee at 0.1: int64(50*0.1)=5 + int64(75*0.1)=7 = 12
	// So total_cost should be > 0 and well below the full admin cost of 125
	if ul.TotalCost <= 0 {
		t.Fatalf("service_fee mode should produce positive cost; got %d", ul.TotalCost)
	}
	if ul.TotalCost >= 125 {
		t.Fatalf("service_fee cost should be discounted vs full admin cost 125; got %d", ul.TotalCost)
	}

	// Quota should be deducted in service_fee mode
	afterQuota := getByokUserQuota(t, db, 1)
	if afterQuota >= 100000 {
		t.Fatalf("service_fee mode should deduct quota; before=100000, after=%d", afterQuota)
	}

	// Daily billing rows should be written in service_fee mode
	var tokenDailyCount int64
	db.Model(&models.TokenDailyBilling{}).Count(&tokenDailyCount)
	if tokenDailyCount == 0 {
		t.Fatalf("service_fee mode should write token daily billing")
	}
}

func TestSettleOne_AdminPath_Unchanged(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "alice", Password: "x", Role: 1, Status: 1, Quota: 1000000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})

	initialQuota := getByokUserQuota(t, db, 1)

	settler := NewSettler(appProv, bus, logger)
	settler.Settle(context.Background(), "test-agent", []protocol.UsageLogEntry{{
		RequestID:        "byok-admin-req-1",
		UserID:           1,
		OwnerType:        "admin",
		ChannelID:        5,
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 50,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})

	// Admin path: quota must be deducted
	if getByokUserQuota(t, db, 1) >= initialQuota {
		t.Fatalf("admin path should have deducted quota; before=%d, after=%d", initialQuota, getByokUserQuota(t, db, 1))
	}

	var ul models.UsageLog
	if err := db.Where("request_id = ?", "byok-admin-req-1").First(&ul).Error; err != nil {
		t.Fatalf("usage_log not written: %v", err)
	}
	if ul.OwnerType != "admin" {
		t.Fatalf("owner_type = %q, want \"admin\"", ul.OwnerType)
	}
	if ul.ChannelID != 5 {
		t.Fatalf("channel_id = %d, want 5", ul.ChannelID)
	}
	if ul.TotalCost <= 0 {
		t.Fatalf("admin path should produce positive cost; got %d", ul.TotalCost)
	}
}

// getByokUserQuota reads user.quota from DB.
func getByokUserQuota(t *testing.T, db *gorm.DB, userID uint) int64 {
	t.Helper()
	var u models.User
	if err := db.First(&u, userID).Error; err != nil {
		t.Fatal(err)
	}
	return u.Quota
}

// TestApplyByokBillingMode_ServiceFeeTotalClosed verifies that in service_fee
// mode the returned total_cost is the sum of the per-bucket adjusted costs
// (each truncated independently), not a separately-truncated discount of the
// original total. This closes the float-truncation off-by-one drift that would
// otherwise break the invariant total_cost == input + output + cacheR + cacheW.
func TestApplyByokBillingMode_ServiceFeeTotalClosed(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.Setting{Key: "byok_billing_mode", Value: "service_fee"})
	db.Create(&models.Setting{Key: "byok_service_fee_ratio", Value: "0.1"})

	settler := NewSettler(appProv, bus, logger)
	q := dao.NewAdminQuery(dao.NewContext(appProv))

	// Inputs chosen so that per-bucket truncation produces 99+100+100+100 = 399,
	// while truncating the un-discounted total (4002 * 0.1 = 400.2 → 400) drifts
	// by one. The bug is exactly that drift.
	entry := protocol.UsageLogEntry{OwnerType: "private"}
	in, out, cr, cw, total, mode := settler.applyByokBillingMode(q, entry, 999, 1000, 1001, 1002, 4002)

	if mode != "service_fee" {
		t.Fatalf("mode = %q, want %q", mode, "service_fee")
	}
	if got, want := in+out+cr+cw, int64(399); got != want {
		t.Fatalf("per-bucket sum = %d, want %d (in=%d out=%d cr=%d cw=%d)", got, want, in, out, cr, cw)
	}
	if total != in+out+cr+cw {
		t.Fatalf("total = %d, want %d = sum(in=%d, out=%d, cr=%d, cw=%d); total_cost must close against per-bucket costs",
			total, in+out+cr+cw, in, out, cr, cw)
	}
}
