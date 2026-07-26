package billing

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestInsightsUsesInjectedStatsCacheAndReturnsIsolatedResponses(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	h.Cache = dao.NewStatsCache()
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	seedInsightsBucket(t, db, date, 10, "gpt-4o", 500, 100, 30, 200)
	ctx := makeInsightsCtx(t, application, 1, true)
	req := InsightsRequest{Start: start, End: end, Gran: "day", Stack: "model"}

	first, err := h.Insights(ctx, req)
	if err != nil {
		t.Fatalf("first Insights: %v", err)
	}
	first.Trend[0].Requests = 999
	seedInsightsBucket(t, db, date, 11, "claude-3", 300, 100, 10, 150)

	second, err := h.Insights(ctx, req)
	if err != nil {
		t.Fatalf("cached Insights: %v", err)
	}
	if second.Trend[0].Requests != 1 {
		t.Fatalf("cached insights was polluted: requests = %d, want 1", second.Trend[0].Requests)
	}
}

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
	if err := db.AutoMigrate(&models.BillingHourlyBucket{}, &models.BillingLog{}); err != nil {
		t.Fatalf("migrate billing hourly fixture: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	return &Handler{}, db, application
}

func makeInsightsCtx(t *testing.T, application app.Application, userID uint, isAdmin bool) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
	return &app.Context{
		Context:      ginCtx,
		App:          application,
		UserInfo:     &app.UserInfo{UserID: userID, GroupID: 1},
		OwnerContext: t.Context(),
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
	if err := db.Create(&models.BillingHourlyBucket{
		Date: date, Hour: hour, UserID: 1, TokenID: 1, ChannelID: 5,
		ModelName: model, OwnerType: "admin", RequestCount: 1,
		PromptTokens: prompt, CacheReadTokens: cacheRead, InputCost: inputCost, TotalCost: cost,
	}).Error; err != nil {
		t.Fatalf("seed billing hourly bucket: %v", err)
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

	ctx := makeInsightsCtx(t, application, 1, true)
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

func TestInsights_User_ReadsOwnBillingHourlyWithoutCrossUserLeak(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	seedInsightsBucket(t, db, date, 10, "gpt-4o", 500, 100, 30, 200)
	if err := db.Create(&models.BillingHourlyBucket{
		Date: date, Hour: 10, UserID: 2, TokenID: 2, ChannelID: 5,
		ModelName: "gpt-4o", OwnerType: "admin", RequestCount: 1, TotalCost: 900,
	}).Error; err != nil {
		t.Fatalf("seed other user billing hourly: %v", err)
	}

	ctx := makeInsightsCtx(t, application, 1, false)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Insights user: %v", err)
	}
	// cost stack 与 cache saving 都读 user_id=1 的 core billing facts。
	if len(resp.CostTrendStacked.Buckets) != 1 {
		t.Fatalf("user: CostTrendStacked.Buckets = %d, want 1", len(resp.CostTrendStacked.Buckets))
	}
	if len(resp.CostTrendStacked.SeriesOrder) != 1 || resp.CostTrendStacked.SeriesOrder[0] != "gpt-4o" {
		t.Fatalf("user: SeriesOrder = %v, want [gpt-4o]", resp.CostTrendStacked.SeriesOrder)
	}
	if got := resp.CostTrendStacked.Buckets[0].Series["gpt-4o"]; got != 500 {
		t.Fatalf("user: cost = %d, want own cost 500", got)
	}
	if resp.CacheSaving.SavedTokens != 30 {
		t.Fatalf("user: SavedTokens = %d, want own billing fact 30", resp.CacheSaving.SavedTokens)
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
	ctx := makeInsightsCtx(t, application, 1, true)
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

// TestInsights_ModelFilter_CacheSavingIsModelAgnostic 验证 spec §4:
// 带 model 筛选时, CacheSaving 仍反映所有 model 的数据(缓存命中卡不随模型过滤)。
func TestInsights_ModelFilter_CacheSavingIsModelAgnostic(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	// gpt-4o: cache_read=30, prompt=100; claude-3: cache_read=10, prompt=100
	seedInsightsBucket(t, db, date, 10, "gpt-4o", 500, 100, 30, 200)
	seedInsightsBucket(t, db, date, 11, "claude-3", 300, 100, 10, 150)

	// 请求带 model=gpt-4o 筛选
	ctx := makeInsightsCtx(t, application, 1, true)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Insights with model filter: %v", err)
	}

	// CacheSaving 必须反映两个 model(sum cache_read=40, sum prompt=200)
	// hit_ratio = 40/240 ≈ 0.1666…
	wantHit := 40.0 / 240.0
	if resp.CacheSaving.HitRatio < wantHit-1e-6 || resp.CacheSaving.HitRatio > wantHit+1e-6 {
		t.Fatalf("CacheSaving.HitRatio = %f, want %f (model-agnostic)", resp.CacheSaving.HitRatio, wantHit)
	}
	if resp.CacheSaving.SavedTokens != 40 {
		t.Fatalf("CacheSaving.SavedTokens = %d, want 40 (includes claude-3 cache)", resp.CacheSaving.SavedTokens)
	}

	// CostTrendStacked 只含 gpt-4o
	if len(resp.CostTrendStacked.SeriesOrder) != 1 || resp.CostTrendStacked.SeriesOrder[0] != "gpt-4o" {
		t.Fatalf("CostTrendStacked.SeriesOrder = %v, want [gpt-4o]", resp.CostTrendStacked.SeriesOrder)
	}
}

func TestInsights_Trend_IncludesCacheTokens(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	if err := db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ChannelName: "ch-5", ModelName: "gpt-4o",
		AgentID: "ag-1", OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 100, CompletionTokens: 200, CacheReadTokens: 30, CacheWriteTokens: 40,
		TotalCost: 10,
	}).Error; err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	ctx := makeInsightsCtx(t, application, 1, true)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(resp.Trend) == 0 {
		t.Fatalf("Trend empty; want >=1 bucket")
	}
	var tokens int64
	for _, b := range resp.Trend {
		tokens += b.Tokens
	}
	if tokens != 370 {
		t.Fatalf("trend tokens = %d, want 370 (含 cache)", tokens)
	}
}

func TestBillingInsightsAcceptsTopNFiveTenTwenty(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	for i := 0; i < 7; i++ {
		seedInsightsBucket(t, db, date, 10+i, fmt.Sprintf("model-%02d", i), int64(100-i), 1, 0, 1)
	}
	ctx := makeInsightsCtx(t, application, 1, true)
	for _, tc := range []struct {
		topN int
		want int
	}{{0, 6}, {5, 6}, {10, 7}, {20, 7}} {
		resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day", TopN: tc.topN})
		if err != nil {
			t.Fatalf("top_n=%d: %v", tc.topN, err)
		}
		if got := len(resp.CostTrendStacked.SeriesOrder); got != tc.want {
			t.Fatalf("top_n=%d series = %d, want %d", tc.topN, got, tc.want)
		}
	}
}

func TestBillingInsightsRejectsInvalidTopN(t *testing.T) {
	h, _, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	for _, topN := range []int{-1, 6} {
		_, err := h.Insights(makeInsightsCtx(t, application, 1, true), InsightsRequest{Start: start, End: end, TopN: topN})
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "InvalidTopN" {
			t.Fatalf("top_n=%d error = %v, want InvalidTopN", topN, err)
		}
	}
}

func TestBillingTokenFilterOnlyChangesTrendSeriesAndHidesForeignTokens(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	for _, token := range []models.Token{
		{ID: 11, UserID: 1, Key: "sk-own-a", Name: "own-a"},
		{ID: 12, UserID: 1, Key: "sk-own-b", Name: "own-b"},
		{ID: 21, UserID: 2, Key: "sk-foreign", Name: "foreign"},
	} {
		if err := db.Create(&token).Error; err != nil {
			t.Fatalf("seed token: %v", err)
		}
	}
	for _, row := range []models.UsageLog{
		{RequestID: "own-a", UserID: 1, TokenID: 11, ModelName: "model-a", CreatedAt: start + 10, PromptTokens: 10, CacheReadTokens: 2, TotalCost: 100},
		{RequestID: "own-b", UserID: 1, TokenID: 12, ModelName: "model-b", CreatedAt: start + 20, PromptTokens: 20, CacheReadTokens: 3, TotalCost: 200},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed usage log: %v", err)
		}
	}
	for _, row := range []models.BillingHourlyBucket{
		{Date: date, Hour: 0, UserID: 1, TokenID: 11, ModelName: "model-a", RequestCount: 1, PromptTokens: 10, CacheReadTokens: 2, TotalCost: 100},
		{Date: date, Hour: 0, UserID: 1, TokenID: 12, ModelName: "model-b", RequestCount: 1, PromptTokens: 20, CacheReadTokens: 3, TotalCost: 200},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed billing bucket: %v", err)
		}
	}

	h.Cache = dao.NewStatsCache()
	ctx := makeInsightsCtx(t, application, 1, false)
	filtered, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "hour", TokenID: 11, TopN: 5})
	if err != nil {
		t.Fatalf("filtered insights: %v", err)
	}
	if got := filtered.Trend[0].Requests; got != 1 {
		t.Fatalf("filtered trend requests = %d, want 1", got)
	}
	if got := filtered.CostTrendStacked.SeriesOrder; !reflect.DeepEqual(got, []string{"model-a"}) {
		t.Fatalf("filtered cost series = %v", got)
	}
	if got := filtered.CacheSaving.SavedTokens; got != 5 {
		t.Fatalf("cache saving = %d, want unfiltered 5", got)
	}

	other, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "hour", TokenID: 12, TopN: 10})
	if err != nil {
		t.Fatalf("second filtered insights: %v", err)
	}
	if got := other.CostTrendStacked.SeriesOrder; !reflect.DeepEqual(got, []string{"model-b"}) {
		t.Fatalf("second cost series = %v; cache key omitted token_id/top_n", got)
	}

	for _, tokenID := range []uint{21, 999} {
		_, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, TokenID: tokenID})
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			t.Fatalf("token_id=%d error = %v, want hidden 404", tokenID, err)
		}
	}
}

func TestBillingTokenFilterAllowsAdminTokenAccess(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	if err := db.Create(&models.Token{ID: 21, UserID: 2, Key: "sk-admin-visible", Name: "admin-visible"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UsageLog{RequestID: "admin-token", UserID: 2, TokenID: 21, ModelName: "foreign-model", CreatedAt: start + 10, TotalCost: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.BillingHourlyBucket{Date: time.Unix(start, 0).UTC().Format("2006-01-02"), UserID: 2, TokenID: 21, ModelName: "foreign-model", RequestCount: 1, TotalCost: 7}).Error; err != nil {
		t.Fatal(err)
	}
	resp, err := h.Insights(makeInsightsCtx(t, application, 1, true), InsightsRequest{Start: start, End: end, Gran: "hour", TokenID: 21})
	if err != nil {
		t.Fatalf("admin filter: %v", err)
	}
	if len(resp.Trend) != 1 || resp.Trend[0].Requests != 1 {
		t.Fatalf("admin filtered trend = %+v", resp.Trend)
	}
}

func TestBillingTokenFilterSplitLayoutNeedsOnlyCoreBillingFacts(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	start, _ := insightsDayRange()
	require.NoError(t, db.Create(&models.Token{ID: 31, UserID: 3, Key: "split-core", Name: "split-core"}).Error)
	require.NoError(t, db.Create(&models.BillingLog{
		RequestID: "split-core-only", UserID: 3, TokenID: 31, ModelName: "core-model",
		PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 5, CacheWriteTokens: 2,
		InputCost: 4, TotalCost: 9, CreatedAt: start + 2,
	}).Error)
	resp, err := h.Insights(makeInsightsCtx(t, application, 1, true), InsightsRequest{Start: start + 1, End: start + 3, Gran: "hour", TokenID: 31})
	require.NoError(t, err)
	require.Equal(t, []string{"core-model"}, resp.CostTrendStacked.SeriesOrder)
	require.Len(t, resp.Trend, 1)
	require.Equal(t, int64(37), resp.Trend[0].Tokens)
	require.Equal(t, int64(5), resp.CacheSaving.SavedTokens)
}
