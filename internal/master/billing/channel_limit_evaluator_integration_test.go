package billing

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

func TestLimitEvaluator_Tick(t *testing.T) {
	db, app := setupTestDB(t) // settler_test.go:25 — returns (*gorm.DB, *testAppProvider), auto-migrates all tables
	ctx := dao.NewContext(app)
	q := dao.NewAdminQuery(ctx).Channel()

	// channel: 日金额限额 1000;当天已用 1500 → 应被禁用
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{Name: "over", Type: 1, Status: 1},
		Limit: datatypes.NewJSONType(models.ChannelLimit{
			Rules: []models.LimitRule{{Metric: models.LimitMetricCost, Window: models.LimitWindowDaily, Threshold: 1000}},
		}),
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&models.ChannelDailyBilling{Date: "2026-05-27", ChannelID: ch.ID, RequestCount: 10, TotalCost: 1500}).Error; err != nil {
		t.Fatalf("seed billing: %v", err)
	}

	ev := NewLimitEvaluator(app, nil, zap.NewNop(), time.Minute) // bus=nil:Tick 内 publish 容错跳过
	if err := ev.Tick(now); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got, _ := q.GetByID(ch.ID)
	if got.Status != 0 {
		t.Fatalf("Status=%d want 0 (disabled)", got.Status)
	}
	st := got.LimitState.Data()
	if !st.Tripped || !st.AutoRecover || st.Reason != "cost/daily" {
		t.Fatalf("LimitState=%+v want tripped+autoRecover+cost/daily", st)
	}

	// 第二轮:把当天用量降到阈值下(模拟窗口重置/回落)→ 应自动恢复
	db.Model(&models.ChannelDailyBilling{}).Where("channel_id = ?", ch.ID).Update("total_cost", 0)
	if err := ev.Tick(now); err != nil {
		t.Fatalf("Tick2: %v", err)
	}
	got2, _ := q.GetByID(ch.ID)
	if got2.Status != 1 || got2.LimitState.Data().Tripped {
		t.Fatalf("Status=%d tripped=%v want 1/false (auto-recovered)", got2.Status, got2.LimitState.Data().Tripped)
	}
}
