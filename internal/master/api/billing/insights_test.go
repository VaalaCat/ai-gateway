package billing

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newInsightsTestCtx 构造 handler + DB + Application 三件套 (跟 dashboard_test 同形)。
func newInsightsTestCtx(t *testing.T) (*Handler, *gorm.DB, app.Application) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	return &Handler{}, db, application
}

func makeInsightsCtx(application app.Application, userID uint, isAdmin bool) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
	return &app.Context{
		Context:  ginCtx,
		App:      application,
		UserInfo: &app.UserInfo{UserID: userID, GroupID: 1},
	}
}

// seedInsightsBucket 写一行 hourly bucket;total_cost 与 input_cost / prompt / cache_read 都给值
// 以便 CostTrendStacked 和 CacheSaving 两条路径都被覆盖。
func seedInsightsBucket(t *testing.T, db *gorm.DB, date string, hour int, model string, cost, prompt, cacheRead, inputCost int64) {
	t.Helper()
	if err := db.Create(&models.UsageHourlyBucket{
		Date:            date,
		Hour:            hour,
		ChannelID:       5,
		ChannelName:     "ch-5",
		ModelName:       model,
		AgentID:         "ag-1",
		OwnerType:       "admin",
		RequestCount:    1,
		SuccessCount:    1,
		PromptTokens:    prompt,
		CacheReadTokens: cacheRead,
		InputCost:       inputCost,
		TotalCost:       cost,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}
}

func insightsDayRange() (int64, int64) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	end := start + 86400
	return start, end
}

func TestInsights_Admin_PopulatesStackAndSaving(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	// 两个 model,各一桶,模拟 stack-by-model。
	seedInsightsBucket(t, db, date, 10, "gpt-4o", 500, 100, 30, 200)
	seedInsightsBucket(t, db, date, 11, "claude-3", 300, 100, 10, 150)

	ctx := makeInsightsCtx(application, 1, true)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day", Stack: "model"})
	if err != nil {
		t.Fatalf("Insights admin: %v", err)
	}
	if len(resp.CostTrendStacked.Buckets) == 0 {
		t.Fatalf("admin: CostTrendStacked.Buckets empty; want >=1")
	}
	if len(resp.CostTrendStacked.SeriesOrder) != 2 {
		t.Fatalf("admin: SeriesOrder len = %d, want 2 (gpt-4o + claude-3, no overflow to others)", len(resp.CostTrendStacked.SeriesOrder))
	}
	// gpt-4o (cost 500) 应排在 claude-3 (cost 300) 之前。
	if resp.CostTrendStacked.SeriesOrder[0] != "gpt-4o" {
		t.Fatalf("SeriesOrder[0] = %q, want gpt-4o (highest total cost)", resp.CostTrendStacked.SeriesOrder[0])
	}
	// cache_saving: sum(cache_read)=40, sum(prompt)=200, sum(prompt+cache_read)=240
	// hit_ratio = 40/240 ≈ 0.1666...
	wantHit := 40.0 / 240.0
	if resp.CacheSaving.HitRatio < wantHit-1e-6 || resp.CacheSaving.HitRatio > wantHit+1e-6 {
		t.Fatalf("HitRatio = %f, want %f", resp.CacheSaving.HitRatio, wantHit)
	}
	if resp.CacheSaving.SavedTokens != 40 {
		t.Fatalf("SavedTokens = %d, want 40", resp.CacheSaving.SavedTokens)
	}
	// saved_cost = 40 * (350 / 200) = 70
	if resp.CacheSaving.SavedCost != 70 {
		t.Fatalf("SavedCost = %d, want 70", resp.CacheSaving.SavedCost)
	}
	if resp.CacheSaving.VsLabel != "vs no-cache" {
		t.Fatalf("VsLabel = %q, want 'vs no-cache'", resp.CacheSaving.VsLabel)
	}
}

func TestInsights_User_EmptyStackAndSaving(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	seedInsightsBucket(t, db, date, 10, "gpt-4o", 500, 100, 30, 200)

	ctx := makeInsightsCtx(application, 1, false)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Insights user: %v", err)
	}
	// Phase 1 admin-only: user scope 返回空 stacked,无 series。
	if len(resp.CostTrendStacked.Buckets) != 0 {
		t.Fatalf("user: CostTrendStacked.Buckets = %d, want 0", len(resp.CostTrendStacked.Buckets))
	}
	if len(resp.CostTrendStacked.SeriesOrder) != 0 {
		t.Fatalf("user: SeriesOrder = %d, want 0", len(resp.CostTrendStacked.SeriesOrder))
	}
	if resp.CacheSaving.SavedTokens != 0 {
		t.Fatalf("user: SavedTokens = %d, want 0", resp.CacheSaving.SavedTokens)
	}
	if resp.CacheSaving.VsLabel != "vs no-cache" {
		t.Fatalf("user: VsLabel = %q, want 'vs no-cache'", resp.CacheSaving.VsLabel)
	}
}

func TestInsights_RangeOutOfBounds_Returns400(t *testing.T) {
	h, _, application := newInsightsTestCtx(t)
	now := time.Now().UTC().Unix()
	// gran=day max 365 天; 400 天必越界。
	start := now - 400*86400
	ctx := makeInsightsCtx(application, 1, true)
	_, err := h.Insights(ctx, InsightsRequest{Start: start, End: now, Gran: "day"})
	if err == nil {
		t.Fatalf("expected 400 RangeOutOfBounds, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("Status = %d, want 400", apiErr.Status)
	}
	if apiErr.Code != "RangeOutOfBounds" {
		t.Fatalf("Code = %q, want RangeOutOfBounds", apiErr.Code)
	}
}
