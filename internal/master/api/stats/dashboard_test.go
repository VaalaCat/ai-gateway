package stats

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

// newDashboardTestCtx 构造 Handler + DB + Application 三件套，模式参考 log/list_test.go。
func newDashboardTestCtx(t *testing.T) (*Handler, *gorm.DB, app.Application) {
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
	if err := db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice", Quota: 1000, UsedQuota: 200}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	return &Handler{}, db, application
}

func makeDashboardCtx(application app.Application, userID uint, isAdmin bool) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
	return &app.Context{
		Context:  ginCtx,
		App:      application,
		UserInfo: &app.UserInfo{UserID: userID, GroupID: 1},
	}
}

// seedDashboardHourlyBucket 写入一个 admin 维度的小时桶,带 stream 累计字段以便 SpeedCompare/leaderboard tps 有数据。
func seedDashboardHourlyBucket(t *testing.T, db *gorm.DB, date string, hour int, modelName string, reqs int64) {
	t.Helper()
	if err := db.Create(&models.UsageHourlyBucket{
		Date:                       date,
		Hour:                       hour,
		ChannelID:                  5,
		ChannelName:                "ch-5",
		ModelName:                  modelName,
		AgentID:                    "ag-1",
		OwnerType:                  "admin",
		RequestCount:               reqs,
		SuccessCount:               reqs,
		PromptTokens:               reqs * 10,
		CompletionTokens:           reqs * 20,
		TotalCost:                  reqs * 100,
		StreamRequestCount:         reqs,
		SumFirstResponseMs:         reqs * 50,
		SumGenerationMs:            reqs * 1000,
		SumStreamCompletionTokens:  reqs * 20,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}
}

// seedDashboardUserLog 写入 usage_log 行供 user-scope 测试 (HourlyTrend user 分支 + KpiUsers.Active)。
func seedDashboardUserLog(t *testing.T, db *gorm.DB, userID uint, ts int64) {
	t.Helper()
	if err := db.Select("*").Create(&models.UsageLog{
		UserID:           userID,
		ChannelID:        5,
		ModelName:        "gpt-4o",
		AgentID:          "ag-1",
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalCost:        100,
		IsStream:         true,
		Status:           1,
		Duration:         1000,
		FirstResponseMs:  50,
		RequestID:        "seed-user-log",
		CreatedAt:        ts,
	}).Error; err != nil {
		t.Fatalf("seed usage log: %v", err)
	}
}

// dayRange returns a [today 00:00, tomorrow 00:00) UTC range with day granularity.
// 与 DashboardKpis 内部 prev 周期 (start-86400) 不重叠，避免被 prev 拉走数据。
func dayRange() (int64, int64) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	end := start + 86400
	return start, end
}

func TestDashboard_Admin_IncludesAllBlocks(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	seedDashboardHourlyBucket(t, db, date, 10, "gpt-4o", 5)
	seedDashboardHourlyBucket(t, db, date, 11, "claude-3", 3)

	ctx := makeDashboardCtx(application, 1, true)
	resp, err := h.Dashboard(ctx, DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard admin: %v", err)
	}
	if resp.Leaderboard == nil {
		t.Fatalf("admin: Leaderboard should be non-nil")
	}
	if resp.SpeedCompare == nil {
		t.Fatalf("admin: SpeedCompare should be non-nil")
	}
	if resp.ModelDistribution == nil {
		t.Fatalf("admin: ModelDistribution should be non-nil (seeded data)")
	}
	if resp.Kpis.Requests.Value <= 0 {
		t.Fatalf("admin: Kpis.Requests.Value = %d, want > 0", resp.Kpis.Requests.Value)
	}
	if resp.Kpis.Users == nil {
		t.Fatalf("admin: Kpis.Users should be non-nil")
	}
	if resp.Kpis.Quota != nil {
		t.Fatalf("admin: Kpis.Quota should be nil")
	}
	wantMetrics := []string{"cost", "requests", "tokens"}
	if len(resp.Trend.Metrics) != len(wantMetrics) {
		t.Fatalf("Trend.Metrics = %v, want %v", resp.Trend.Metrics, wantMetrics)
	}
	if len(resp.Leaderboard.AvailableMetrics) != 5 {
		t.Fatalf("Leaderboard.AvailableMetrics len = %d, want 5", len(resp.Leaderboard.AvailableMetrics))
	}
}

func TestDashboard_User_OmitsAdminFields(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	// seed 一条 usage_log 让 user-scope KpiBundle.Requests 有值。
	seedDashboardUserLog(t, db, 1, start+3600)

	ctx := makeDashboardCtx(application, 1, false)
	resp, err := h.Dashboard(ctx, DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard user: %v", err)
	}
	if resp.Leaderboard != nil {
		t.Fatalf("user: Leaderboard should be nil, got %+v", resp.Leaderboard)
	}
	if resp.ModelDistribution != nil {
		t.Fatalf("user: ModelDistribution should be nil, got %+v", resp.ModelDistribution)
	}
	if resp.SpeedCompare != nil {
		t.Fatalf("user: SpeedCompare should be nil, got %+v", resp.SpeedCompare)
	}
	if resp.Kpis.Quota == nil {
		t.Fatalf("user: Kpis.Quota should be non-nil")
	}
	if resp.Kpis.Quota.Quota != 1000 || resp.Kpis.Quota.UsedQuota != 200 {
		t.Fatalf("user: Kpis.Quota = %+v, want {1000, 200}", resp.Kpis.Quota)
	}
	if resp.Kpis.Users != nil {
		t.Fatalf("user: Kpis.Users should be nil")
	}
	if resp.Kpis.SuccessRate != nil {
		t.Fatalf("user: Kpis.SuccessRate should be nil")
	}
	if resp.Kpis.Requests.Value != 1 {
		t.Fatalf("user: Kpis.Requests.Value = %d, want 1", resp.Kpis.Requests.Value)
	}
}

func TestDashboard_RangeOutOfBounds_Returns400(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	now := time.Now().UTC().Unix()
	// gran=day max 365 天；这里给 400 天必越界。
	start := now - 400*86400
	ctx := makeDashboardCtx(application, 1, true)
	_, err := h.Dashboard(ctx, DashboardRequest{Start: start, End: now, Gran: "day"})
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
	if got, _ := apiErr.Details["gran"].(string); got != "day" {
		t.Fatalf("Details.gran = %q, want day", got)
	}
}
