package stats

import (
	"context"
	"encoding/json"
	"errors"
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
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type dashboardFailingFinder struct {
	failAt string
}

type dashboardTrendRoutingFinder struct {
	dashboardFailingFinder
	coreBuckets   []dao.TimeBucket
	hourlyBuckets []dao.TimeBucket
	coreErr       error
	hourlyErr     error
	coreCalls     int
	hourlyCalls   int
}

func (f *dashboardTrendRoutingFinder) CoreDashboardTrend(dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.TimeBucket, error) {
	f.coreCalls++
	return f.coreBuckets, f.coreErr
}

func (f *dashboardTrendRoutingFinder) HourlyTrend(dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.TimeBucket, error) {
	f.hourlyCalls++
	return f.hourlyBuckets, f.hourlyErr
}

func (f dashboardFailingFinder) CoreDashboardKpis(dao.ObsRange, dao.Scope, dao.ObsFilter) (dao.KpiBundle, error) {
	return dao.KpiBundle{Requests: dao.KpiMetric{Value: 4}}, nil
}

func (f dashboardFailingFinder) CoreDashboardTrend(dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.TimeBucket, error) {
	return []dao.TimeBucket{{Requests: 4, Cost: 40, Tokens: 400}}, nil
}

func (f dashboardFailingFinder) DashboardSuccessRate(dao.ObsRange, dao.Scope, dao.ObsFilter) (dao.KpiMetric, error) {
	if f.failAt == "success" {
		return dao.KpiMetric{}, dao.ErrLogDatabaseUnavailable
	}
	return dao.KpiMetric{Value: 3}, nil
}

func (f dashboardFailingFinder) HourlyTrend(dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.TimeBucket, error) {
	if f.failAt == "trend" {
		return nil, dao.ErrLogDatabaseUnavailable
	}
	return []dao.TimeBucket{{TTFTMs: 10}}, nil
}

func (f dashboardFailingFinder) Leaderboard(by, _ string, _ int, _ dao.ObsRange, _ dao.Scope, _ dao.ObsFilter) ([]dao.LeaderRow, error) {
	if f.failAt == "leaderboard_"+by {
		return nil, dao.ErrLogDatabaseUnavailable
	}
	return []dao.LeaderRow{{Name: by}}, nil
}

func (f dashboardFailingFinder) SpeedCompare(dimension string, _ dao.ObsRange, _ dao.Scope, _ dao.ObsFilter) ([]dao.SpeedRow, error) {
	if f.failAt == "speed_"+dimension {
		return nil, dao.ErrLogDatabaseUnavailable
	}
	return []dao.SpeedRow{{Name: dimension}}, nil
}

func TestDashboardUsesInjectedStatsCacheAndReturnsIsolatedResponses(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	h.Cache = dao.NewStatsCache()
	start, end := dayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	seedDashboardHourlyBucket(t, db, date, 10, "gpt-4o", 5)
	ctx := makeDashboardCtx(t, application, 1, true)
	req := DashboardRequest{Start: start, End: end, Gran: "day"}

	first, err := h.Dashboard(ctx, req)
	if err != nil {
		t.Fatalf("first Dashboard: %v", err)
	}
	first.LogMetrics.Trend.Buckets[0].TTFTMs = 999
	seedDashboardHourlyBucket(t, db, date, 11, "claude-3", 3)

	second, err := h.Dashboard(ctx, req)
	if err != nil {
		t.Fatalf("cached Dashboard: %v", err)
	}
	if second.Kpis.Requests.Value != 5 {
		t.Fatalf("cached requests = %d, want 5", second.Kpis.Requests.Value)
	}
	if second.LogMetrics.Trend.Buckets[0].TTFTMs == 999 {
		t.Fatalf("cached performance trend was polluted: %+v", second.LogMetrics.Trend.Buckets[0])
	}
}

func TestDashboardKeepsCoreDataWhenLogDatabaseUnavailable(t *testing.T) {
	h, core, application := newDashboardTestCtx(t)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	application.SetLogDB(nil)
	start, end := dayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	seedDashboardHourlyBucket(t, core, date, 10, "gpt-4o", 5)

	resp, err := h.Dashboard(makeDashboardCtx(t, application, 1, true), DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard degraded: %v", err)
	}
	if resp.DataStatus.LogDB != "unavailable" {
		t.Fatalf("DataStatus.LogDB = %q, want unavailable", resp.DataStatus.LogDB)
	}
	if resp.LogMetrics != nil {
		t.Fatalf("LogMetrics = %+v, want nil", resp.LogMetrics)
	}
	if len(resp.Trend.Buckets) != 1 || resp.Trend.Buckets[0].Requests != 5 {
		t.Fatalf("core Trend = %+v, want one concrete billing bucket", resp.Trend)
	}
	if want := []string{"cost", "requests", "tokens"}; !reflect.DeepEqual(resp.Trend.Metrics, want) {
		t.Fatalf("core Trend.Metrics = %v, want %v", resp.Trend.Metrics, want)
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal degraded dashboard: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal degraded dashboard: %v", err)
	}
	bucket := wire["trend"].(map[string]any)["buckets"].([]any)[0].(map[string]any)
	for _, logOnlyField := range []string{"ttft_ms", "tps", "cache_hit_rate", "prompt_tokens", "completion_tokens", "cache_read_tokens", "cache_write_tokens"} {
		if _, exists := bucket[logOnlyField]; exists {
			t.Fatalf("core trend bucket unexpectedly exposes %q: %s", logOnlyField, payload)
		}
	}
	if resp.Kpis.Requests.Value != 5 || resp.Kpis.Cost.Value != 500 || resp.Kpis.Tokens.Value != 150 {
		t.Fatalf("core Kpis = %+v, want requests=5 cost=500 tokens=150", resp.Kpis)
	}
	if _, exists := wire["model_distribution"]; exists {
		t.Fatalf("dashboard unexpectedly embeds model_distribution: %s", payload)
	}
	distribution, err := h.ModelDistribution(
		makeDashboardCtx(t, application, 1, true),
		ModelDistributionRequest{Start: start, End: end, Gran: "day", TopN: 5},
	)
	if err != nil {
		t.Fatalf("independent model distribution with log database unavailable: %v", err)
	}
	if len(distribution.Buckets) != 1 || distribution.Buckets[0].Name != "gpt-4o" {
		t.Fatalf("independent core ModelDistribution = %+v, want gpt-4o", distribution)
	}
}

func TestDashboardMarksLogDataUnavailableRatherThanEmpty(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	application.SetLogDB(nil)
	start, end := dayRange()

	resp, err := h.Dashboard(makeDashboardCtx(t, application, 1, false), DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard degraded: %v", err)
	}
	if resp.DataStatus.LogDB != "unavailable" || resp.LogMetrics != nil {
		t.Fatalf("degraded response = %+v, want explicit unavailable and null log_metrics", resp)
	}
}

func TestDashboardMarksLogUnavailableWhileConnectorIsNotReady(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	h.LogDatabaseReady = func() bool { return false }
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	application.SetLogDB(db)
	start, end := dayRange()

	resp, err := h.Dashboard(makeDashboardCtx(t, application, 1, false), DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard degraded: %v", err)
	}
	if resp.DataStatus.LogDB != "unavailable" || resp.LogMetrics != nil {
		t.Fatalf("degraded response = %+v, want connector readiness to control log status", resp)
	}
}

func TestDashboardLogSectionDegradesAtomicallyOnMidRequestDisconnect(t *testing.T) {
	for _, failAt := range []string{
		"success", "trend", "leaderboard_user", "leaderboard_model", "leaderboard_channel", "speed_model", "speed_channel",
	} {
		t.Run(failAt, func(t *testing.T) {
			h, db, application := newDashboardTestCtx(t)
			application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
			application.SetLogDB(db)
			finder := dashboardFailingFinder{failAt: failAt}
			h.DashboardDataFinder = func(app.Application, context.Context) DashboardDataFinder { return finder }
			start, end := dayRange()

			resp, err := h.Dashboard(makeDashboardCtx(t, application, 1, true), DashboardRequest{Start: start, End: end, Gran: "day"})
			if err != nil {
				t.Fatalf("Dashboard: %v", err)
			}
			if resp.DataStatus.LogDB != "unavailable" || resp.LogMetrics != nil {
				t.Fatalf("degraded log section = %+v", resp)
			}
			if resp.Kpis.SuccessRate != nil {
				t.Fatalf("SuccessRate = %+v, want nil after %s disconnect", resp.Kpis.SuccessRate, failAt)
			}
			if len(resp.Trend.Buckets) != 1 || resp.Trend.Buckets[0].Requests != 4 {
				t.Fatalf("core trend lost after %s disconnect: %+v", failAt, resp.Trend)
			}
		})
	}
}

// Legacy admin KPI still has its own billing-table baseline dependency. These
// routing tests isolate only the trend ownership regression.
func TestDashboardCoreTrendRoutingUsesHourlyTrendForLegacyAdmin(t *testing.T) {
	finder := &dashboardTrendRoutingFinder{hourlyBuckets: []dao.TimeBucket{{Requests: 5, Cost: 120, Tokens: 75}}}
	start, end := dayRange()

	got, err := dashboardCoreTrend(finder, app.DatabaseLayoutLegacySingle, dao.ObsRange{Start: start, End: end, Gran: dao.GranDay}, dao.Scope{IsAdmin: true}, dao.ObsFilter{})
	if err != nil {
		t.Fatalf("legacy admin trend: %v", err)
	}
	if finder.hourlyCalls != 1 || finder.coreCalls != 0 {
		t.Fatalf("legacy calls hourly/core = %d/%d, want 1/0", finder.hourlyCalls, finder.coreCalls)
	}
	if len(got) != 1 || got[0].Requests != 5 || got[0].Cost != 120 || got[0].Tokens != 75 {
		t.Fatalf("legacy admin trend = %+v, want usage-hourly aggregate", got)
	}
}

func TestDashboardLegacyUserTrendHonorsExactBoundariesWithoutBillingTables(t *testing.T) {
	h, db, application := newLegacyDashboardTestCtx(t)
	base := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC).Unix()
	for i, offset := range []int64{0, 1, 3598, 3599} {
		seedDashboardUserLogWithID(t, db, 1, base+offset, "legacy-boundary-"+string(rune('a'+i)))
	}

	resp, err := h.Dashboard(makeDashboardCtx(t, application, 1, false), DashboardRequest{Start: base + 1, End: base + 3599, Gran: "hour"})
	if err != nil {
		t.Fatalf("legacy user Dashboard: %v", err)
	}
	if len(resp.Trend.Buckets) != 1 || resp.Trend.Buckets[0].Requests != 2 {
		t.Fatalf("legacy user trend = %+v, want two rows inside [start,end)", resp.Trend)
	}
}

func TestDashboardCoreTrendRoutingKeepsLegacyEmptyResult(t *testing.T) {
	finder := &dashboardTrendRoutingFinder{}
	start, end := dayRange()

	got, err := dashboardCoreTrend(finder, app.DatabaseLayoutLegacySingle, dao.ObsRange{Start: start, End: end, Gran: dao.GranDay}, dao.Scope{IsAdmin: true}, dao.ObsFilter{})
	if err != nil {
		t.Fatalf("empty legacy trend: %v", err)
	}
	if len(got) != 0 || finder.hourlyCalls != 1 || finder.coreCalls != 0 {
		t.Fatalf("empty legacy result=%+v calls=%d/%d, want empty and 1/0", got, finder.hourlyCalls, finder.coreCalls)
	}
}

func TestDashboardCoreTrendRoutingPropagatesLegacyHourlyError(t *testing.T) {
	wantErr := errors.New("legacy hourly failed")
	finder := &dashboardTrendRoutingFinder{hourlyErr: wantErr}
	start, end := dayRange()

	_, err := dashboardCoreTrend(finder, app.DatabaseLayoutLegacySingle, dao.ObsRange{Start: start, End: end, Gran: dao.GranDay}, dao.Scope{IsAdmin: true}, dao.ObsFilter{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("legacy trend error = %v, want %v", err, wantErr)
	}
}

func TestDashboardCoreTrendRoutingKeepsSplitOnCoreBilling(t *testing.T) {
	finder := &dashboardTrendRoutingFinder{coreBuckets: []dao.TimeBucket{{Requests: 7}}}
	start, end := dayRange()

	got, err := dashboardCoreTrend(finder, app.DatabaseLayoutSplit, dao.ObsRange{Start: start, End: end, Gran: dao.GranDay}, dao.Scope{IsAdmin: true}, dao.ObsFilter{})
	if err != nil {
		t.Fatalf("split core trend: %v", err)
	}
	if len(got) != 1 || got[0].Requests != 7 || finder.coreCalls != 1 || finder.hourlyCalls != 0 {
		t.Fatalf("split result=%+v calls=%d/%d, want core billing only", got, finder.coreCalls, finder.hourlyCalls)
	}
}

func newLegacyDashboardTestCtx(t *testing.T) (*Handler, *gorm.DB, app.Application) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	if db.Migrator().HasTable(&models.BillingLog{}) || db.Migrator().HasTable(&models.BillingHourlyBucket{}) {
		t.Fatal("legacy fixture unexpectedly contains split billing tables")
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed legacy group: %v", err)
	}
	if err := db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice", Quota: 1000, UsedQuota: 200}).Error; err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	return &Handler{}, db, application
}

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
	if err := db.AutoMigrate(&models.BillingHourlyBucket{}, &models.BillingLog{}); err != nil {
		t.Fatalf("migrate billing hourly fixture: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice", Quota: 1000, UsedQuota: 200}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	return &Handler{}, db, application
}

func makeDashboardCtx(t *testing.T, application app.Application, userID uint, isAdmin bool) *app.Context {
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

// seedDashboardHourlyBucket 写入一个 admin 维度的小时桶,带 stream 累计字段以便 SpeedCompare/leaderboard tps 有数据。
func seedDashboardHourlyBucket(t *testing.T, db *gorm.DB, date string, hour int, modelName string, reqs int64) {
	t.Helper()
	if err := db.Create(&models.UsageHourlyBucket{
		Date:                      date,
		Hour:                      hour,
		ChannelID:                 5,
		ChannelName:               "ch-5",
		ModelName:                 modelName,
		AgentID:                   "ag-1",
		OwnerType:                 "admin",
		RequestCount:              reqs,
		SuccessCount:              reqs,
		PromptTokens:              reqs * 10,
		CompletionTokens:          reqs * 20,
		TotalCost:                 reqs * 100,
		StreamRequestCount:        reqs,
		SumFirstResponseMs:        reqs * 50,
		SumGenerationMs:           reqs * 1000,
		SumStreamCompletionTokens: reqs * 20,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}
	if err := db.Create(&models.BillingHourlyBucket{
		Date: date, Hour: hour, UserID: 1, TokenID: uint(hour + 1), ChannelID: 5,
		ModelName: modelName, OwnerType: "admin", RequestCount: reqs, SuccessCount: reqs,
		PromptTokens: reqs * 10, CompletionTokens: reqs * 20, TotalCost: reqs * 100,
	}).Error; err != nil {
		t.Fatalf("seed billing hourly bucket: %v", err)
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

	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.Dashboard(ctx, DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard admin: %v", err)
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal admin dashboard: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal admin dashboard: %v", err)
	}
	if _, exists := wire["model_distribution"]; exists {
		t.Fatalf("admin dashboard unexpectedly embeds model_distribution: %s", payload)
	}
	if resp.LogMetrics == nil || resp.LogMetrics.Leaderboard == nil {
		t.Fatalf("admin: Leaderboard should be non-nil")
	}
	if resp.LogMetrics.SpeedCompare == nil {
		t.Fatalf("admin: SpeedCompare should be non-nil")
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
	wantMetrics := []string{"ttft", "tps", "cache_hit_rate"}
	if !reflect.DeepEqual(resp.LogMetrics.Trend.Metrics, wantMetrics) {
		t.Fatalf("Trend.Metrics = %v, want %v", resp.LogMetrics.Trend.Metrics, wantMetrics)
	}
	if want := []string{"cost", "requests", "tokens"}; !reflect.DeepEqual(resp.Trend.Metrics, want) {
		t.Fatalf("core Trend.Metrics = %v, want %v", resp.Trend.Metrics, want)
	}
	if len(resp.LogMetrics.Leaderboard.AvailableMetrics) != 5 {
		t.Fatalf("Leaderboard.AvailableMetrics len = %d, want 5", len(resp.LogMetrics.Leaderboard.AvailableMetrics))
	}
}

func TestDashboard_User_OmitsAdminFields(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	// seed 一条 usage_log 让 user-scope KpiBundle.Requests 有值。
	seedDashboardUserLog(t, db, 1, start+3600)

	ctx := makeDashboardCtx(t, application, 1, false)
	resp, err := h.Dashboard(ctx, DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard user: %v", err)
	}
	if resp.LogMetrics == nil {
		t.Fatalf("user: LogMetrics should be non-nil")
	}
	if resp.LogMetrics.Leaderboard != nil {
		t.Fatalf("user: Leaderboard should be nil, got %+v", resp.LogMetrics.Leaderboard)
	}
	if resp.LogMetrics.SpeedCompare != nil {
		t.Fatalf("user: SpeedCompare should be nil, got %+v", resp.LogMetrics.SpeedCompare)
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

func TestDashboard_Leaderboard_SortedByTokens(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	// expensive: 高 cost / 低 token;chatty: 低 cost / 高 token。
	if err := db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ChannelName: "ch-5", ModelName: "expensive",
		AgentID: "ag-1", OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 10, CompletionTokens: 10, TotalCost: 1000,
	}).Error; err != nil {
		t.Fatalf("seed expensive: %v", err)
	}
	if err := db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ChannelName: "ch-5", ModelName: "chatty",
		AgentID: "ag-1", OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 5000, CompletionTokens: 5000, TotalCost: 1,
	}).Error; err != nil {
		t.Fatalf("seed chatty: %v", err)
	}
	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.Dashboard(ctx, DashboardRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if resp.LogMetrics == nil || resp.LogMetrics.Leaderboard == nil || len(resp.LogMetrics.Leaderboard.Models) == 0 {
		t.Fatalf("expected leaderboard models")
	}
	if resp.LogMetrics.Leaderboard.Models[0].Name != "chatty" {
		t.Fatalf("models[0].Name = %q, want chatty (默认按总 token 排序)", resp.LogMetrics.Leaderboard.Models[0].Name)
	}
}

// seedDashboardUserLogWithID 与 seedDashboardUserLog 相同，但允许指定 request_id 以避免 UNIQUE 冲突。
func seedDashboardUserLogWithID(t *testing.T, db *gorm.DB, userID uint, ts int64, requestID string) {
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
		RequestID:        requestID,
		CreatedAt:        ts,
	}).Error; err != nil {
		t.Fatalf("seed usage log (%s): %v", requestID, err)
	}
}

// TestDashboard_NonAdmin_UserIDCollapsed 验证越权防护：非 admin 用 UserID=99 请求时
// filter.UserID 被清零，实际按 scope.UserID 计，不会看到其他用户的数据。
func TestDashboard_NonAdmin_UserIDCollapsed(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	// 为 user 1 写一条 usage_log。
	seedDashboardUserLogWithID(t, db, 1, start+3600, "collapse-user-1")
	// 为另一个 user 99 写一条 usage_log（本次请求不应看到）。
	seedDashboardUserLogWithID(t, db, 99, start+3600, "collapse-user-99")

	ctx := makeDashboardCtx(t, application, 1, false)
	// 非 admin 传入 UserID=99（他人），应被折叠为 0，DAO EffectiveUserID 取 scope.UserID=1。
	resp, err := h.Dashboard(ctx, DashboardRequest{Start: start, End: end, Gran: "day", UserID: 99})
	if err != nil {
		t.Fatalf("Dashboard non-admin with foreign UserID: %v", err)
	}
	// user scope 只能看到自己(1条)，不能看到 user 99 的那条。
	if resp.Kpis.Requests.Value != 1 {
		t.Fatalf("privilege collapse: Kpis.Requests.Value = %d, want 1 (only own log)", resp.Kpis.Requests.Value)
	}
}

func TestDashboard_RangeOutOfBounds_Returns400(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	now := time.Now().UTC().Unix()
	// gran=day max 365 天；这里给 400 天必越界。
	start := now - 400*86400
	ctx := makeDashboardCtx(t, application, 1, true)
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
