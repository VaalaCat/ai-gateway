package dao

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedBillingHourlyBucket(t *testing.T, db *gorm.DB, row models.BillingHourlyBucket) {
	t.Helper()
	require.NoError(t, db.Create(&row).Error)
}

func setupBillingStatsQuery(t *testing.T) (*adminStatsQuery, *gorm.DB) {
	t.Helper()
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	return NewAdminQuery(ctx).Stats().(*adminStatsQuery), core
}

func TestActiveUsersUsesBillingHourlyAndExcludesAnonymous(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC).Unix()
	end := start + 7*86400
	for _, row := range []models.BillingHourlyBucket{
		{Date: "2026-07-16", Hour: 0, UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-5", RequestCount: 1},
		{Date: "2026-07-17", Hour: 4, UserID: 1, TokenID: 2, ChannelID: 1, ModelName: "gpt-5-mini", RequestCount: 1},
		{Date: "2026-07-22", Hour: 23, UserID: 2, TokenID: 3, ChannelID: 1, ModelName: "gpt-5", RequestCount: 1},
		{Date: "2026-07-18", Hour: 3, UserID: 0, TokenID: 0, ChannelID: 1, ModelName: "gpt-5", RequestCount: 1},
		{Date: "2026-07-23", Hour: 0, UserID: 3, TokenID: 4, ChannelID: 1, ModelName: "gpt-5", RequestCount: 1},
	} {
		seedBillingHourlyBucket(t, db, row)
	}
	for _, user := range []models.User{{ID: 1, Username: "alice"}, {ID: 2, Username: "bob"}, {ID: 3, Username: "carol"}} {
		require.NoError(t, db.Create(&user).Error)
	}

	got, err := q.DashboardKpis(ObsRange{Start: start, End: end, Gran: GranDay}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.NotNil(t, got.Users)
	require.Equal(t, int64(2), got.Users.Active, "distinct users inside [from,to), excluding user_id=0")
}

func TestCostTrendUsesBillingHourlyWithoutStrftime(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Unix()
	end := start + int64((2 * time.Hour).Seconds())
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-07-20", Hour: 10, UserID: 7, TokenID: 1, ChannelID: 1, ModelName: "gpt-5", RequestCount: 2, TotalCost: 30})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-07-20", Hour: 11, UserID: 7, TokenID: 2, ChannelID: 1, ModelName: "gpt-5", RequestCount: 3, TotalCost: 70})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-07-20", Hour: 12, UserID: 7, TokenID: 3, ChannelID: 1, ModelName: "gpt-5", RequestCount: 4, TotalCost: 900})

	got, err := q.CostTrendStackedByModel(ObsRange{Start: start, End: end, Gran: GranHour}, Scope{UserID: 7}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 2)
	require.Equal(t, int64(30), got.Buckets[0].Series["gpt-5"])
	require.Equal(t, int64(70), got.Buckets[1].Series["gpt-5"])
}

func TestUserLeaderboardByModelUsesBillingHourly(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Unix()
	end := start + 86400
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "alice"}).Error)
	require.NoError(t, db.Create(&models.User{ID: 2, Username: "bob"}).Error)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-07-20", Hour: 1, UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-5", RequestCount: 2, TotalCost: 20, PromptTokens: 10})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-07-20", Hour: 2, UserID: 2, TokenID: 2, ChannelID: 1, ModelName: "gpt-5", RequestCount: 3, TotalCost: 30, PromptTokens: 20})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-07-20", Hour: 3, UserID: 1, TokenID: 3, ChannelID: 1, ModelName: "other", RequestCount: 100, TotalCost: 999})

	got, err := q.Leaderboard("user", "requests", 10, ObsRange{Start: start, End: end, Gran: GranDay}, Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, []LeaderRow{
		{ID: 2, Name: "bob", Cost: 30, Requests: 3, Tokens: 20},
		{ID: 1, Name: "alice", Cost: 20, Requests: 2, Tokens: 10},
	}, got)
}

func TestBillingHourlySevenDayQueryPlanUsesIndexesAndNeverScansDetailTables(t *testing.T) {
	_, db := setupBillingStatsQuery(t)
	queries := []struct {
		sql       string
		wantIndex string
	}{
		{sql: `SELECT COUNT(DISTINCT user_id) FROM billing_hourly_buckets WHERE (date > '2026-07-16' OR (date = '2026-07-16' AND hour >= 0)) AND (date < '2026-07-22' OR (date = '2026-07-22' AND hour <= 23)) AND user_id > 0`, wantIndex: "idx_bhb_window_user"},
		{sql: `SELECT date, hour, model_name, SUM(total_cost) FROM billing_hourly_buckets WHERE user_id = 7 AND (date, hour) >= ('2026-07-16', 0) AND (date, hour) < ('2026-07-23', 0) GROUP BY date, hour, model_name`, wantIndex: "idx_bhb_user_window"},
		{sql: `SELECT user_id, SUM(request_count) FROM billing_hourly_buckets WHERE (date > '2026-07-16' OR (date = '2026-07-16' AND hour >= 0)) AND (date < '2026-07-22' OR (date = '2026-07-22' AND hour <= 23)) AND model_name = 'gpt-5' GROUP BY user_id`, wantIndex: "idx_bhb_model_user"},
	}
	for _, query := range queries {
		var rows []struct{ Detail string }
		require.NoError(t, db.Raw("EXPLAIN QUERY PLAN "+query.sql).Scan(&rows).Error)
		details := make([]string, 0, len(rows))
		for _, row := range rows {
			details = append(details, row.Detail)
		}
		plan := strings.Join(details, "\n")
		require.Contains(t, plan, query.wantIndex, query.sql)
		if query.wantIndex == "idx_bhb_user_window" {
			require.Contains(t, plan, "(date,hour)>(?,?)", query.sql)
			require.Contains(t, plan, "(date,hour)<(?,?)", query.sql)
		}
		require.NotContains(t, plan, "SCAN billing_logs", query.sql)
		require.NotContains(t, plan, "SCAN usage_logs", query.sql)
	}
}

func TestSplitStatsQueriesReadLogDatabase(t *testing.T) {
	core := setupTestDB(t)
	logDB := setupTestDB(t)
	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	r := ObsRange{Start: now.Add(-time.Hour).Unix(), End: now.Add(time.Hour).Unix(), Gran: GranHour}
	require.NoError(t, core.Create(&models.UsageHourlyBucket{Date: date, Hour: now.Hour(), ChannelID: 7, ModelName: "core", RequestCount: 99}).Error)
	require.NoError(t, logDB.Create(&models.UsageHourlyBucket{Date: date, Hour: now.Hour(), ChannelID: 7, ModelName: "log", RequestCount: 3, FailedCount: 3}).Error)
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})).Stats()
	rows, err := q.ChannelModelBreakdown(7, r)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "log", rows[0].ModelName)
	totals, err := q.LogsTotals(r, Scope{IsAdmin: true})
	require.NoError(t, err)
	require.Equal(t, int64(3), totals.Total)
	require.NoError(t, logDB.Exec("CREATE TABLE request_logs AS SELECT * FROM usage_logs WHERE 0").Error)
	request := models.UsageLog{RequestID: "request-log", UserID: 42, Status: 0, CreatedAt: now.Unix()}
	require.NoError(t, logDB.Table("request_logs").Create(&request).Error)
	userTotals, err := q.LogsTotals(r, Scope{UserID: 42})
	require.NoError(t, err)
	require.Equal(t, int64(1), userTotals.Total)
}

func seedHourlyBucket(t *testing.T, db *gorm.DB, date string, hour int, reqs, tokens int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: hour,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		OwnerType:    "admin",
		RequestCount: reqs, SuccessCount: reqs,
		PromptTokens: tokens / 2, CompletionTokens: tokens / 2,
		TotalCost: reqs * 10,
	}).Error)
}

func seedUsageLogRow(t *testing.T, db *gorm.DB, userID uint, ts int64, prompt, completion int) {
	t.Helper()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: userID, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		PromptTokens: prompt, CompletionTokens: completion, TotalCost: 100,
		IsStream: true, Status: 1, Duration: 1000, FirstResponseMs: 100,
		RequestID: fmt.Sprintf("seed-%d-%d", userID, ts), CreatedAt: ts,
	}).Error)
}

func TestHourlyTrend_Admin_HourGran(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucket(t, db, "2026-05-20", 13, 100, 1000)
	seedHourlyBucket(t, db, "2026-05-20", 14, 50, 500)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: true}, ObsFilter{})

	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(100), got[0].Requests)
	require.Equal(t, int64(50), got[1].Requests)
}

func TestHourlyTrend_User_FallbackToUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC).Unix()
	seedUsageLogRow(t, db, 1, ts, 10, 200)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: false, UserID: 1}, ObsFilter{})

	require.NoError(t, err)
	require.NotEmpty(t, got)
	var totalRequests int64
	for _, b := range got {
		totalRequests += b.Requests
	}
	require.Equal(t, int64(1), totalRequests)
}

func TestHourlyTrend_EmptyRange_ReturnsNil(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().HourlyTrend(ObsRange{Start: 100, End: 99, Gran: GranHour}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestHourlyTrend_Admin_DayGran(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucket(t, db, "2026-05-20", 13, 100, 1000)
	seedHourlyBucket(t, db, "2026-05-20", 14, 50, 500)
	seedHourlyBucket(t, db, "2026-05-21", 10, 30, 300)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})

	require.NoError(t, err)
	require.Len(t, got, 2, "two days")
	require.Equal(t, int64(150), got[0].Requests, "day 1 = 100 + 50")
	require.Equal(t, int64(30), got[1].Requests, "day 2 = 30")
}

// seedHourlyBucketModel is a variant of seedHourlyBucket that lets you set model_name.
func seedHourlyBucketModel(t *testing.T, db *gorm.DB, date string, hour int, modelName string, reqs, tokens int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: hour,
		ChannelID: 5, ModelName: modelName, AgentID: "cn-1",
		OwnerType:    "admin",
		RequestCount: reqs, SuccessCount: reqs,
		PromptTokens: tokens / 2, CompletionTokens: tokens / 2,
		TotalCost: reqs * 10,
	}).Error)
}

func TestDistribution_ByModel_Admin(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 30, 1000)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "claude-3", 10, 500)

	got, err := q.Stats().Distribution("model", ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})

	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "gpt-4o", got[0].Name, "descending by value")
	require.Equal(t, int64(30), got[0].Value)
	require.InEpsilon(t, 30.0/40.0, got[0].Ratio, 0.0001)
	require.Equal(t, "claude-3", got[1].Name)
	require.Equal(t, int64(10), got[1].Value)
	require.InEpsilon(t, 10.0/40.0, got[1].Ratio, 0.0001)
}

func TestDistribution_NoData_ReturnsEmpty(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().Distribution("model", ObsRange{
		Start: 0, End: 100, Gran: GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestDistribution_SingleModel_Boundary(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 1, 1)
	got, err := q.Stats().Distribution("model", ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.InEpsilon(t, 1.0, got[0].Ratio, 0.0001)
}

func TestDistribution_UnsupportedDimension_ReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	_, err := q.Stats().Distribution("garbage", ObsRange{Gran: GranDay}, Scope{IsAdmin: true}, ObsFilter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "garbage")
}

// seedTokenDaily inserts a token_daily_billing row for user-level leaderboard tests.
func seedTokenDaily(t *testing.T, db *gorm.DB, date string, userID, tokenID uint, tokenName string, reqs, prompt, completion, cost int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.TokenDailyBilling{
		Date: date, UserID: userID, TokenID: tokenID, TokenName: tokenName,
		RequestCount: reqs, SuccessCount: reqs,
		PromptTokens: prompt, CompletionTokens: completion,
		TotalCost: cost,
	}).Error)
}

// seedHourlyBucketChannelStream seeds a stream-aware admin-channel hourly bucket row.
func seedHourlyBucketChannelStream(t *testing.T, db *gorm.DB, date string, hour int, channelID uint, channelName, modelName string, reqs, streamReqs, ttftSum, genMs, streamCompletion int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: hour,
		ChannelID: channelID, ModelName: modelName, AgentID: "cn-1",
		OwnerType:                 "admin",
		ChannelName:               channelName,
		RequestCount:              reqs,
		SuccessCount:              reqs,
		PromptTokens:              reqs * 5,
		CompletionTokens:          reqs * 5,
		TotalCost:                 reqs * 10,
		StreamRequestCount:        streamReqs,
		SumFirstResponseMs:        ttftSum,
		SumGenerationMs:           genMs,
		SumStreamCompletionTokens: streamCompletion,
	}).Error)
}

func TestLeaderboard_ByModel_OrderedByCostDesc_Admin(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// gpt-4o: 30 req × 10 cost each = 300
	// claude-3: 50 req × 10 = 500 → claude-3 cost higher
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 30, 1000)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "claude-3", 50, 500)
	got, err := q.Stats().Leaderboard("model", "cost", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "claude-3", got[0].Name)
	require.Equal(t, int64(500), got[0].Cost)
	require.Equal(t, int64(50), got[0].Requests)
	require.Equal(t, "gpt-4o", got[1].Name)
}

func TestLeaderboard_UnknownMetric_FallsBackToCost(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 30, 1000)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "claude-3", 50, 500)
	got, err := q.Stats().Leaderboard("model", "garbage", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	// fallback = cost DESC; claude-3 cost=500 > gpt-4o cost=300
	require.Equal(t, "claude-3", got[0].Name)
}

func TestLeaderboard_LimitZero_ReturnsEmpty(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 30, 1000)
	got, err := q.Stats().Leaderboard("model", "cost", 0, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestLeaderboard_UnsupportedBy_ReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	_, err := q.Stats().Leaderboard("garbage", "cost", 10, ObsRange{}, Scope{IsAdmin: true}, ObsFilter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "garbage")
}

func TestLeaderboard_ByModel_TPSMetric_PrefersFasterModel(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// gpt-4o: 100 stream-completion tokens / 1000 generation_ms → tps=100
	// claude-3: 100 stream-completion tokens / 500 generation_ms → tps=200 (faster)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch5", "gpt-4o", 10, 10, 500, 1000, 100)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch5", "claude-3", 10, 10, 500, 500, 100)

	got, err := q.Stats().Leaderboard("model", "tps", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "claude-3", got[0].Name, "faster tps wins")
	require.Greater(t, got[0].TPS, got[1].TPS)
}

func TestLeaderboard_ByUser_AdminOnly(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.User{Username: "alice"}).Error) // id=1
	require.NoError(t, db.Create(&models.User{Username: "bob"}).Error)   // id=2
	seedTokenDaily(t, db, "2026-05-20", 1, 1, "tok-a", 10, 100, 100, 500)
	seedTokenDaily(t, db, "2026-05-20", 2, 2, "tok-b", 20, 200, 200, 1500)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().Leaderboard("user", "cost", 10, r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "bob", got[0].Name, "higher cost first")
	require.Equal(t, uint(2), got[0].ID)
	require.Equal(t, int64(1500), got[0].Cost)

	// User scope: by="user" returns nil, nil
	gotUser, err := q.Stats().Leaderboard("user", "cost", 10, r, Scope{IsAdmin: false, UserID: 1}, ObsFilter{})
	require.NoError(t, err)
	require.Nil(t, gotUser)
}

func TestLeaderboard_ByChannel_Admin(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// channel 5 (low cost), channel 7 (high cost)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch-five", "gpt-4o", 10, 0, 0, 0, 0)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 7, "ch-seven", "gpt-4o", 30, 0, 0, 0, 0)

	got, err := q.Stats().Leaderboard("channel", "cost", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, uint(7), got[0].ID)
	require.Equal(t, "ch-seven", got[0].Name)
	require.Equal(t, int64(300), got[0].Cost)
}

// seedHourlyBucketSpeed seeds a single hour bucket with explicit TTFT/TPS implied values.
// streamReq=1 row with sum_first_response_ms=ttft and sum_generation_ms=genMs, sum_stream_completion_tokens=tokens
// so avg_ttft = ttft, avg_tps = tokens*1000/genMs
func seedHourlyBucketSpeed(t *testing.T, db *gorm.DB, modelName string, ttft int64, genMs int64, tokens int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13,
		ChannelID: 5, ModelName: modelName, AgentID: "cn-1",
		OwnerType:                 "admin",
		RequestCount:              1,
		SuccessCount:              1,
		PromptTokens:              100,
		CompletionTokens:          tokens,
		TotalCost:                 10,
		StreamRequestCount:        1,
		SumFirstResponseMs:        ttft,
		SumGenerationMs:           genMs,
		SumStreamCompletionTokens: tokens,
	}).Error)
}

func todayRangeDay(t *testing.T) ObsRange {
	return ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
}

func TestSpeedCompare_ByModel_OrderedByTTFTAsc(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// gpt-4o: TTFT=280, TPS = 52*1000/1000 = 52
	seedHourlyBucketSpeed(t, db, "gpt-4o", 280, 1000, 52)
	// claude-3: TTFT=510, TPS = 31*1000/1000 = 31
	seedHourlyBucketSpeed(t, db, "claude-3", 510, 1000, 31)

	got, err := q.Stats().SpeedCompare("model", todayRangeDay(t), Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "gpt-4o", got[0].Name)
	require.Equal(t, int64(280), got[0].TTFTMs)
	require.InDelta(t, 52.0, got[0].TPS, 0.0001)
	require.Equal(t, "claude-3", got[1].Name)
}

func TestSpeedCompare_NoStreamData_RowSkipped(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// Insert a non-stream model: stream_request_count=0 → HAVING filters out
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13,
		ChannelID: 5, ModelName: "non-stream-model", AgentID: "cn-1",
		OwnerType:        "admin",
		RequestCount:     1,
		SuccessCount:     1,
		PromptTokens:     100,
		CompletionTokens: 30,
		TotalCost:        10,
		// StreamRequestCount default 0
	}).Error)

	got, err := q.Stats().SpeedCompare("model", todayRangeDay(t), Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Empty(t, got, "non-stream models filtered out")
}

func TestSpeedCompare_UnknownDimension_ReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	_, err := q.Stats().SpeedCompare("garbage", todayRangeDay(t), Scope{IsAdmin: true}, ObsFilter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "garbage")
}

func TestSpeedCompare_UserScope_ReturnsNil(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().SpeedCompare("model", todayRangeDay(t), Scope{IsAdmin: false, UserID: 1}, ObsFilter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStatsDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Stats()

	// Seed data
	db.Create(&models.User{Username: "u1"})
	db.Create(&models.User{Username: "u2"})
	db.Create(&models.Token{UserID: 1, Key: "k1", Name: "t1"})
	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "ch1", Type: 1}})
	db.Create(&models.Agent{AgentID: "a1", Name: "agent1"})
	db.Create(&models.ModelConfig{ModelName: "gpt-4"})

	now := time.Now().Unix()
	db.Select("*").Create(&models.UsageLog{UserID: 1, RequestID: "r1", TotalCost: 100, CreatedAt: now})
	db.Select("*").Create(&models.UsageLog{UserID: 2, RequestID: "r2", TotalCost: 250, CreatedAt: now})

	t.Run("GetOverview", func(t *testing.T) {
		s, err := q.GetOverview()
		if err != nil {
			t.Fatalf("GetOverview: %v", err)
		}
		if s.UserCount != 2 {
			t.Fatalf("expected 2 users, got %d", s.UserCount)
		}
		if s.TokenCount != 1 {
			t.Fatalf("expected 1 token, got %d", s.TokenCount)
		}
		if s.ChannelCount != 1 {
			t.Fatalf("expected 1 channel, got %d", s.ChannelCount)
		}
		if s.AgentCount != 1 {
			t.Fatalf("expected 1 agent, got %d", s.AgentCount)
		}
		if s.ModelConfigCount != 1 {
			t.Fatalf("expected 1 model config, got %d", s.ModelConfigCount)
		}
		if s.UsageLogCount != 2 {
			t.Fatalf("expected 2 usage logs, got %d", s.UsageLogCount)
		}
		if s.TotalCost != 350 {
			t.Fatalf("expected total cost 350, got %d", s.TotalCost)
		}
	})

	t.Run("GetTableCount", func(t *testing.T) {
		count, err := q.GetTableCount(TableUsers)
		if err != nil {
			t.Fatalf("GetTableCount: %v", err)
		}
		if count != 2 {
			t.Fatalf("expected 2, got %d", count)
		}
	})

	t.Run("GetTotalCost no filter", func(t *testing.T) {
		cost, err := q.GetTotalCost(UsageLogListFilter{})
		if err != nil {
			t.Fatalf("GetTotalCost: %v", err)
		}
		if cost != 350 {
			t.Fatalf("expected 350, got %d", cost)
		}
	})

	t.Run("GetTotalCost with UserID filter", func(t *testing.T) {
		uid := uint(1)
		cost, err := q.GetTotalCost(UsageLogListFilter{UserID: &uid})
		if err != nil {
			t.Fatalf("GetTotalCost: %v", err)
		}
		if cost != 100 {
			t.Fatalf("expected 100, got %d", cost)
		}
	})

	t.Run("GetTotalCost empty result", func(t *testing.T) {
		uid := uint(9999)
		cost, err := q.GetTotalCost(UsageLogListFilter{UserID: &uid})
		if err != nil {
			t.Fatalf("GetTotalCost: %v", err)
		}
		if cost != 0 {
			t.Fatalf("expected 0, got %d", cost)
		}
	})

	t.Run("GetTrend", func(t *testing.T) {
		items, err := q.GetTrend(30, nil)
		if err != nil {
			t.Fatalf("GetTrend: %v", err)
		}
		if len(items) == 0 {
			t.Fatal("expected at least one trend item")
		}
		total := int64(0)
		for _, item := range items {
			total += item.Cost
		}
		if total != 350 {
			t.Fatalf("expected total cost 350, got %d", total)
		}
	})

	t.Run("GetTrend with userID", func(t *testing.T) {
		uid := uint(1)
		items, err := q.GetTrend(30, &uid)
		if err != nil {
			t.Fatalf("GetTrend: %v", err)
		}
		total := int64(0)
		for _, item := range items {
			total += item.Cost
		}
		if total != 100 {
			t.Fatalf("expected total cost 100, got %d", total)
		}
	})
}

// ---- Task 2.5: ChannelMetrics / AgentMetrics ----

func TestChannelMetrics_Success(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// ch-a (id=5): 10 + 20 = 30 requests, failed_count = 1 + 2 = 3
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch-a", "gpt-4o", 10, 1, 100, 1000, 50)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 14, 5, "ch-a", "gpt-4o", 20, 2, 200, 2000, 100)
	// ch-b (id=7): 5 requests
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 7, "ch-b", "gpt-4o", 5, 0, 0, 0, 0)

	got, err := q.Stats().ChannelMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Ordered DESC by requests; ch-a (30) before ch-b (5).
	require.Equal(t, uint(5), got[0].ID)
	require.Equal(t, "ch-a", got[0].Name)
	require.Equal(t, int64(30), got[0].Requests)
	require.Equal(t, uint(7), got[1].ID)
	require.Equal(t, "ch-b", got[1].Name)
	require.Equal(t, int64(5), got[1].Requests)
	// No TTFT/duration histogram rows seeded for ch-a → p95 stays 0 (insufficient sample).
	require.Equal(t, int64(0), got[0].TTFTP95Ms)
	require.Equal(t, int64(0), got[0].LatencyP95Ms)
}

func TestChannelMetrics_NoData_ReturnsEmpty(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().ChannelMetrics(ObsRange{Start: 0, End: 100, Gran: GranDay})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestChannelMetrics_Spark24h_LengthMatches(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// Seed a row at 2026-05-20 13:00 UTC.
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch-a", "gpt-4o", 10, 1, 100, 1000, 50)
	endTs := time.Date(2026, 5, 20, 20, 0, 0, 0, time.UTC).Unix()
	got, err := q.Stats().ChannelMetrics(ObsRange{
		Start: endTs - 86400, End: endTs, Gran: GranHour,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].Spark24h, 24, "spark must have 24 slots when data exists in 24h window")
	// The seeded 13:00 row falls 7 hours before endTs (20:00) → offset 16.
	var sum int64
	for _, v := range got[0].Spark24h {
		sum += v
	}
	require.Equal(t, int64(10), sum)
}

func TestAgentMetrics_Success(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.Agent{
		AgentID: "cn-1", Name: "agent-cn-1", Status: 1, LastSeen: time.Now().Unix(),
	}).Error)
	// seedHourlyBucketModel inserts a row with agent_id="cn-1".
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 10, 1000)

	got, err := q.Stats().AgentMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "cn-1", got[0].ID)
	require.Equal(t, "agent-cn-1", got[0].Name)
	require.True(t, got[0].Online)
	require.Equal(t, int64(10), got[0].Requests)
	// No TTFT/duration histogram rows seeded for cn-1 → p95 stays 0 (insufficient sample).
	require.Equal(t, int64(0), got[0].TTFTP95Ms)
	require.Equal(t, int64(0), got[0].LatencyP95Ms)
}

func TestAgentMetrics_OfflineAgent_OnlineFalse(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// models.Agent.Status has gorm default:1, so 0 (zero value) gets replaced.
	// Use 2 (disabled/offline) to confirm AgentMetric.Online treats anything != 1 as offline.
	require.NoError(t, db.Create(&models.Agent{
		AgentID: "cn-1", Name: "agent-cn-1", Status: 2,
		LastSeen: time.Now().Add(-time.Hour).Unix(),
	}).Error)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 10, 1000)

	got, err := q.Stats().AgentMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.False(t, got[0].Online, "Status != 1 → not online")
}

func TestAgentMetrics_Spark24h_Has24Slots(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.Agent{
		AgentID: "cn-1", Name: "agent-cn-1", Status: 1,
	}).Error)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 10, 1000)
	endTs := time.Date(2026, 5, 20, 20, 0, 0, 0, time.UTC).Unix()
	got, err := q.Stats().AgentMetrics(ObsRange{
		Start: endTs - 86400, End: endTs, Gran: GranHour,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].Spark24h, 24)
}

// ---- Task 2.6: ErrorDistribution ----

// seedFailedUsageLog 插入一条失败 usage_log (status=0),用于 ErrorDistribution 测试。
func seedFailedUsageLog(t *testing.T, db *gorm.DB, reqID, stage string, channelID uint, ts int64) {
	t.Helper()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: 1, ChannelID: channelID, ModelName: "gpt-4o",
		Status: 0, ErrorStage: stage,
		RequestID: reqID, CreatedAt: ts,
	}).Error)
}

func TestErrorDistribution_ByStage_OrderedByCountDesc(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC).Unix()
	// stage=upstream_decode × 3
	seedFailedUsageLog(t, db, "e1", "upstream_decode", 5, ts)
	seedFailedUsageLog(t, db, "e2", "upstream_decode", 5, ts)
	seedFailedUsageLog(t, db, "e3", "upstream_decode", 7, ts)
	// stage=inbound_decode × 1
	seedFailedUsageLog(t, db, "e4", "inbound_decode", 5, ts)
	// non-failed should be excluded
	seedUsageLogRow(t, db, 1, ts, 1, 1)

	got, err := q.Stats().ErrorDistribution("stage", ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "upstream_decode", got[0].Stage)
	require.Equal(t, int64(3), got[0].Count)
	require.InEpsilon(t, 3.0/4.0, got[0].Ratio, 0.0001)
	require.Equal(t, "inbound_decode", got[1].Stage)
	require.Equal(t, int64(1), got[1].Count)
	require.InEpsilon(t, 1.0/4.0, got[1].Ratio, 0.0001)
}

func TestErrorDistribution_ByChannel_JoinsName(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// channels: id=5 name=ch-five, id=7 name=ch-seven (no row for channel_id=99 -> empty name)
	require.NoError(t, db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "ch-five", Type: 1}}).Error) // id=1
	// Make sure id=5 exists with a name; insert until id=5 by creating placeholders is messy. Use raw SQL with explicit id.
	require.NoError(t, db.Exec("DELETE FROM channels").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id, name, type) VALUES (5, 'ch-five', 1)").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id, name, type) VALUES (7, 'ch-seven', 1)").Error)

	ts := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC).Unix()
	seedFailedUsageLog(t, db, "ec1", "upstream_decode", 5, ts)
	seedFailedUsageLog(t, db, "ec2", "upstream_decode", 5, ts)
	seedFailedUsageLog(t, db, "ec3", "upstream_decode", 7, ts)
	// orphan channel id=99 (no channels row) - simulates BYOK/stale; LEFT JOIN keeps it with empty name
	seedFailedUsageLog(t, db, "ec4", "outbound_encode", 99, ts)

	got, err := q.Stats().ErrorDistribution("channel", ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true})
	require.NoError(t, err)
	require.Len(t, got, 3)
	// channel 5 wins with count=2
	require.Equal(t, uint(5), got[0].ID)
	require.Equal(t, "ch-five", got[0].Name)
	require.Equal(t, int64(2), got[0].Count)
	// orphan channel id=99 with empty name preserved
	var foundOrphan bool
	for _, b := range got {
		if b.ID == 99 {
			require.Equal(t, "", b.Name, "orphan channel keeps empty name")
			require.Equal(t, int64(1), b.Count)
			foundOrphan = true
		}
	}
	require.True(t, foundOrphan, "orphan channel must remain via LEFT JOIN")
}

func TestErrorDistribution_NoData_ReturnsEmpty(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().ErrorDistribution("stage", ObsRange{Start: 0, End: 100, Gran: GranDay}, Scope{IsAdmin: true})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestErrorDistribution_UnknownBy_ReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	_, err := q.Stats().ErrorDistribution("garbage", ObsRange{Gran: GranDay}, Scope{IsAdmin: true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "garbage")
}

func TestErrorDistribution_NonAdmin_ReturnsNil(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().ErrorDistribution("stage", ObsRange{Gran: GranDay}, Scope{IsAdmin: false, UserID: 1})
	require.NoError(t, err)
	require.Nil(t, got)
}

// ---- Task 2.7: StageLatencyP95 ----

// seedUsageLogStage 插入一条 status=1 的 usage_log, 5 个 stage_ms 列均为 ms。
func seedUsageLogStage(t *testing.T, db *gorm.DB, reqID string, ts int64, ms int) {
	t.Helper()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: 1, ChannelID: 5, ModelName: "gpt-4o",
		Status: 1, IsStream: true, Duration: ms, FirstResponseMs: ms,
		InboundDecodeMs:    ms,
		UpstreamDispatchMs: ms,
		UpstreamDecodeMs:   ms,
		OutboundEncodeMs:   ms,
		ClientEncodeMs:     ms,
		RequestID:          reqID,
		CreatedAt:          ts,
	}).Error)
}

func TestStageLatencyP95_KnownDistribution(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC).Unix()
	for i := 1; i <= 100; i++ {
		seedUsageLogStage(t, db, fmt.Sprintf("sl-%d", i), base+int64(i), i)
	}
	got, err := q.Stats().StageLatencyP95(UsageLogListFilter{}, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got.Stages, 5)
	expectedOrder := []string{"inbound_decode", "upstream_dispatch", "upstream_decode", "outbound_encode", "client_encode"}
	for i, sc := range got.Stages {
		require.Equal(t, expectedOrder[i], sc.Name)
		// offset = floor(100 * 95 / 100) = 95 → row[95] (0-indexed) = value 96
		require.Equal(t, int64(96), sc.P95Ms, "stage %s p95 should be 96 (offset 95 of 1..100)", sc.Name)
	}
}

func TestStageLatencyP95_NoData_AllZero(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().StageLatencyP95(UsageLogListFilter{}, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got.Stages, 5)
	for _, sc := range got.Stages {
		require.Equal(t, int64(0), sc.P95Ms)
	}
}

// ---- Task 2.9: DashboardKpis ----

func TestDashboardKpis_AdminCase(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 30, 1000)

	got, err := q.Stats().DashboardKpis(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})

	require.NoError(t, err)
	require.NotNil(t, got.Users)
	require.NotNil(t, got.SuccessRate)
	require.Nil(t, got.Quota)
	require.Equal(t, int64(30), got.Requests.Value)
	require.NotEmpty(t, got.Requests.Spark)
}

func TestDashboardKpis_UserCase(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.User{
		ID: 1, Username: "alice", Password: "x", Role: 1, Status: 1,
		Quota: 1000, UsedQuota: 200,
	}).Error)
	ts := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: 1, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		PromptTokens: 100, CompletionTokens: 50, TotalCost: 30,
		IsStream: true, Status: 1, Duration: 1000, FirstResponseMs: 100,
		RequestID: "u1-1", CreatedAt: ts,
	}).Error)

	got, err := q.Stats().DashboardKpis(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: false, UserID: 1}, ObsFilter{})

	require.NoError(t, err)
	require.Nil(t, got.Users, "user scope omits Users")
	require.Nil(t, got.SuccessRate, "user scope omits SuccessRate")
	require.NotNil(t, got.Quota)
	require.Equal(t, int64(1000), got.Quota.Quota)
	require.Equal(t, int64(200), got.Quota.UsedQuota)
}

func TestDashboardKpis_EmptyData_NoPanic(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	got, err := q.Stats().DashboardKpis(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(0), got.Requests.Value)
	require.Empty(t, got.Requests.Spark)
	require.Equal(t, float64(0), got.Requests.Delta)
}

func TestStageLatencyP95_OnlyFailedRows_AllZero(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC).Unix()
	// Insert 10 status=0 rows; should be filtered out
	for i := 0; i < 10; i++ {
		require.NoError(t, db.Select("*").Create(&models.UsageLog{
			UserID: 1, ChannelID: 5, ModelName: "gpt-4o",
			Status:             0,
			InboundDecodeMs:    100 + i,
			UpstreamDispatchMs: 100 + i,
			UpstreamDecodeMs:   100 + i,
			OutboundEncodeMs:   100 + i,
			ClientEncodeMs:     100 + i,
			RequestID:          fmt.Sprintf("fail-%d", i),
			CreatedAt:          ts + int64(i),
		}).Error)
	}
	got, err := q.Stats().StageLatencyP95(UsageLogListFilter{}, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got.Stages, 5)
	for _, sc := range got.Stages {
		require.Equal(t, int64(0), sc.P95Ms, "status=0 rows excluded → p95=0")
	}
}

// TestHourlyTrend_GranDay_BoundaryOverlap 验证 hourlyTrendFromBuckets 在 gran=day 时
// 用区间重叠语义过滤,而非简单 ts < r.Start 单点判定。
//
// 复现场景: r.Start 是当天 07:09 UTC,数据在同一天 04:55 UTC。day bucket 的 ts
// 是当天 00:00 UTC,< r.Start;旧逻辑会丢掉整个 day。新逻辑判 bucketEnd > r.Start
// → 该 day 仍包含。
func TestHourlyTrend_GranDay_BoundaryOverlap_DataBeforeStartSameDay(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// 数据落在 2026-05-19 04:00 (UTC),早于 r.Start 但同一天
	seedHourlyBucket(t, db, "2026-05-19", 4, 1, 200)
	seedHourlyBucket(t, db, "2026-05-19", 13, 3, 600)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 19, 7, 9, 0, 0, time.UTC).Unix(), // 07:09
		End:   time.Date(2026, 5, 20, 7, 9, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1, "包含同一天,即使 day 起点早于 r.Start")
	require.Equal(t, int64(4), got[0].Requests, "聚合该天所有 hour")
}

// failure case: 数据落在完全早于 r.Start 的前一天 → 不应包含
func TestHourlyTrend_GranDay_BoundaryOverlap_DataFullyBeforeRange(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucket(t, db, "2026-05-17", 12, 10, 1000) // 2 天前
	seedHourlyBucket(t, db, "2026-05-19", 13, 3, 600)   // 当天

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1, "只包含 2026-05-19,2026-05-17 完全在范围外")
	require.Equal(t, int64(3), got[0].Requests)
}

// boundary: r.Start 正好落在 day 边界 00:00 → 该 day 仍应包含
func TestHourlyTrend_GranDay_BoundaryOverlap_StartAtDayBoundary(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucket(t, db, "2026-05-19", 0, 7, 1400)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(7), got[0].Requests)
}

// TestKpiSuccessRate_HourBoundary 验证 kpiSuccessRate 在 hourR 边界外的 hour 不被计入。
// 复现场景: r 是 24h 滑动窗口 (2026-05-19 07:09 → 2026-05-20 07:09)。同一天的
// 04:55 数据按 date 过滤会被算进来,但按 hour 边界应剔除。
func TestKpiSuccessRate_HourBoundary_EarlyOfDayExcluded(t *testing.T) {
	_, db := setupAdminContext(t)
	// 起点之前 (同一天) 的 hour: 不该算 (seedHourlyBucket 里 SuccessCount=reqs)
	seedHourlyBucket(t, db, "2026-05-19", 4, 1, 200)
	// 起点之后的 hour
	seedHourlyBucket(t, db, "2026-05-19", 13, 3, 600)

	r := ObsRange{
		Start: time.Date(2026, 5, 19, 7, 9, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 7, 9, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	hourR := r
	hourR.Gran = GranHour

	got, err := kpiSuccessRate(db, r, hourR, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(3), got.Value, "只算 07:09 之后的成功 count,04:55 的 hour 不计入")
}

// failure case: 所有 hour 都在 hourR 内 → success 等于全部
func TestKpiSuccessRate_HourBoundary_AllInRange(t *testing.T) {
	_, db := setupAdminContext(t)
	seedHourlyBucket(t, db, "2026-05-19", 13, 3, 600)
	seedHourlyBucket(t, db, "2026-05-19", 15, 2, 400)

	r := ObsRange{
		Start: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	hourR := r
	hourR.Gran = GranHour

	got, err := kpiSuccessRate(db, r, hourR, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(5), got.Value)
}

// boundary: hour 正好等于 hourR.Start → 包含 (>= 而非 >)
func TestKpiSuccessRate_HourBoundary_ExactStartHourIncluded(t *testing.T) {
	_, db := setupAdminContext(t)
	// hour 7 落在 r.Start = 07:00 那一秒
	seedHourlyBucket(t, db, "2026-05-19", 7, 4, 800)

	r := ObsRange{
		Start: time.Date(2026, 5, 19, 7, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 7, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	hourR := r
	hourR.Gran = GranHour

	got, err := kpiSuccessRate(db, r, hourR, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(4), got.Value, "ts == hourR.Start 的 hour 应被包含")
}

// TestHourlyTrend_GranHour_BoundaryOverlap_NonIntegerStart 锁定 hour 粒度下区间重叠语义:
// 当 r.Start 非整点 (07:09) 时,07:00 那个 hour bucket 与 [07:09, ...) 仍有重叠,
// 应被包含 —— 这是相比旧版 (ts < r.Start) 的预期行为改进。
func TestHourlyTrend_GranHour_BoundaryOverlap_NonIntegerStart(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucket(t, db, "2026-05-19", 7, 4, 800)  // 07:00 bucket
	seedHourlyBucket(t, db, "2026-05-19", 8, 6, 1200) // 08:00 bucket

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 19, 7, 9, 0, 0, time.UTC).Unix(), // 07:09
		End:   time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2, "07:00 bucket 与 [07:09, ...) 重叠也应包含,加上 08:00")
	require.Equal(t, int64(4), got[0].Requests)
	require.Equal(t, int64(6), got[1].Requests)
}

// ---- Task 4: CacheSaving ReadTokens / WriteTokens ----

// seedHourlyBucketCache 插入一条含 cache token 字段的 billing_hourly_bucket 行。
func seedHourlyBucketCache(t *testing.T, db *gorm.DB, date string, prompt, cacheRead, cacheWrite, inputCost int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: date, Hour: 10,
		ChannelID: 5, ModelName: "gpt-4o",
		OwnerType:        "admin",
		RequestCount:     1,
		SuccessCount:     1,
		PromptTokens:     prompt,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		InputCost:        inputCost,
	}).Error)
}

// TestCacheSaving_ReadWriteTokens_BothPresent: cache read + cache write 都有值
func TestCacheSaving_ReadWriteTokens_BothPresent(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketCache(t, db, "2026-05-20", 100, 50, 20, 200)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	out, err := q.Stats().CacheSaving(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Greater(t, out.HitRatio, float64(0), "有 cache_read → hit_ratio > 0")
	require.Equal(t, int64(50), out.SavedTokens)
	require.Equal(t, int64(50), out.ReadTokens)
	require.Equal(t, int64(20), out.WriteTokens)
}

// TestCacheSaving_ReadWriteTokens_OnlyWrite: 只有 cache write,无 cache read (冷启动场景)
func TestCacheSaving_ReadWriteTokens_OnlyWrite(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketCache(t, db, "2026-05-20", 100, 0, 30, 200)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	out, err := q.Stats().CacheSaving(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, float64(0), out.HitRatio, "cache_read=0 → hit_ratio=0")
	require.Equal(t, int64(0), out.SavedTokens)
	require.Equal(t, int64(0), out.ReadTokens)
	require.Equal(t, int64(30), out.WriteTokens, "cache_write 仍应正确填充")
}

// TestCacheSaving_ReadWriteTokens_NoCacheActivity: cache 完全没有活动,三项均为 0 (边界)
func TestCacheSaving_ReadWriteTokens_NoCacheActivity(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketCache(t, db, "2026-05-20", 100, 0, 0, 200)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	out, err := q.Stats().CacheSaving(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, float64(0), out.HitRatio)
	require.Equal(t, int64(0), out.SavedTokens)
	require.Equal(t, int64(0), out.ReadTokens)
	require.Equal(t, int64(0), out.WriteTokens)
	require.Equal(t, int64(0), out.SavedCost)
}

// TestKpiSuccessRate_HourBoundary_ExactEndHourExcluded 锁定右开区间语义:
// ts 等于 hourR.End 的整点 bucket 不应被计入 (range 是 [Start, End))。
func TestKpiSuccessRate_HourBoundary_ExactEndHourExcluded(t *testing.T) {
	_, db := setupAdminContext(t)
	// hour 7 落在 hourR.End = 07:00 那一秒
	seedHourlyBucket(t, db, "2026-05-19", 7, 5, 1000)

	r := ObsRange{
		Start: time.Date(2026, 5, 18, 7, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 19, 7, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	hourR := r
	hourR.Gran = GranHour

	got, err := kpiSuccessRate(db, r, hourR, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(0), got.Value, "ts == hourR.End 的 hour 不应计入 (右开区间)")
}

func TestHourlyTrend_TokensIncludeCache_Admin(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o",
		AgentID: "cn-1", OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 100, CompletionTokens: 200,
		CacheReadTokens: 30, CacheWriteTokens: 40, TotalCost: 10,
	}).Error)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(370), got[0].Tokens, "100+200+30+40 含 cache")
	require.Equal(t, int64(100), got[0].PromptTokens)
	require.Equal(t, int64(200), got[0].CompletionTokens)
	require.Equal(t, int64(30), got[0].CacheReadTokens)
	require.Equal(t, int64(40), got[0].CacheWriteTokens)
}

func TestHourlyTrend_TokensIncludeCache_UserPath(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: 1, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		PromptTokens: 100, CompletionTokens: 200, CacheReadTokens: 30, CacheWriteTokens: 40,
		TotalCost: 10, Status: 1, RequestID: "cache-user-1", CreatedAt: ts,
	}).Error)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: false, UserID: 1}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(370), got[0].Tokens, "user 路径同口径含 cache")
	require.Equal(t, int64(100), got[0].PromptTokens)
	require.Equal(t, int64(200), got[0].CompletionTokens)
	require.Equal(t, int64(30), got[0].CacheReadTokens)
	require.Equal(t, int64(40), got[0].CacheWriteTokens)
}

func TestHourlyTrend_TokensNoCache_Unchanged(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucket(t, db, "2026-05-20", 13, 10, 1000) // cache=0
	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1000), got[0].Tokens, "cache=0 时退化为 prompt+completion")
}

func TestLeaderboard_ByModel_TokensIncludeCache(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o",
		AgentID: "cn-1", OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 100, CompletionTokens: 200, CacheReadTokens: 30, CacheWriteTokens: 40,
		TotalCost: 10,
	}).Error)
	got, err := q.Stats().Leaderboard("model", "tokens", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(370), got[0].Tokens, "leaderboard tokens 含 cache")
}

func TestLeaderboard_ByUser_TokensIncludeCache(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.User{Username: "alice"}).Error) // id=1
	require.NoError(t, db.Create(&models.TokenDailyBilling{
		Date: "2026-05-20", UserID: 1, TokenID: 1, TokenName: "tok-a",
		RequestCount: 1, SuccessCount: 1,
		PromptTokens: 100, CompletionTokens: 200, CacheReadTokens: 30, CacheWriteTokens: 40,
		TotalCost: 10,
	}).Error)
	got, err := q.Stats().Leaderboard("user", "tokens", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(370), got[0].Tokens, "token_daily_billings 路径同口径含 cache")
}

// ---- Task 3: HourlyTrend source routing + model filter ----

func seedUsageLogModel(t *testing.T, db *gorm.DB, userID uint, ts int64, model string, prompt, completion, cacheRead, cacheWrite int) {
	t.Helper()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: userID, ChannelID: 5, ModelName: model, AgentID: "cn-1",
		PromptTokens: prompt, CompletionTokens: completion,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		TotalCost: 100, Status: 1, Duration: 1000,
		RequestID: fmt.Sprintf("seedm-%d-%d-%s-%d", userID, ts, model, time.Now().UnixNano()), CreatedAt: ts,
	}).Error)
}

func TestHourlyTrend_ModelFilter_Admin(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 100, 1000)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "claude-3", 40, 400)
	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(100), got[0].Requests, "只剩 gpt-4o")
}

func TestHourlyTrend_AdminUserFilter_MatchesUserScope(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	day := "2026-05-20"
	seedTokenDaily(t, db, day, 7, 1, "tk", 5, 100, 200, 999)
	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	gotAdmin, err := q.Stats().HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{UserID: 7})
	require.NoError(t, err)
	gotUser, err := q.Stats().HourlyTrend(r, Scope{IsAdmin: false, UserID: 7}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, gotUser, gotAdmin, "admin 筛某用户 == 该用户自己看到的")
	require.Len(t, gotAdmin, 1)
	require.Equal(t, int64(5), gotAdmin[0].Requests)
	require.Equal(t, int64(999), gotAdmin[0].Cost)
	require.Equal(t, int64(300), gotAdmin[0].Tokens, "100+200,无 cache")
}

// TestHourlyTrend_TokenDaily_EqualsUsageLog_UserDay 是 spec §9 的"换源一致性"检查:
// 同一天为 user 7 在 token_daily_billings 和 usage_logs 各播等价数据,
// 直接调两条 package-private helper,断言两者输出的 []TimeBucket 完全相同。
func TestHourlyTrend_TokenDaily_EqualsUsageLog_UserDay(t *testing.T) {
	_, db := setupAdminContext(t)
	seedTokenDaily(t, db, "2026-05-20", 7, 1, "tk", 1, 10, 20, 100)
	seedUsageLogModel(t, db, 7, time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix(), "gpt-4o", 10, 20, 0, 0)
	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	fromDaily, err := hourlyTrendFromTokenDaily(db, r, 7)
	require.NoError(t, err)
	fromLog, err := hourlyTrendFromUsageLog(db, r, 7, 0, "")
	require.NoError(t, err)
	require.Equal(t, fromLog, fromDaily, "两条数据源应产出相同的 []TimeBucket")
	require.Len(t, fromDaily, 1)
	require.Equal(t, "2026-05-20", fromDaily[0].Label)
	require.Equal(t, int64(1), fromDaily[0].Requests)
	require.Equal(t, int64(30), fromDaily[0].Tokens)
	require.Equal(t, int64(100), fromDaily[0].Cost)
}

func TestHourlyTrend_UserModelFilter_UsesUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	day := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, day, "gpt-4o", 10, 20, 1, 2)
	seedUsageLogModel(t, db, 7, day, "claude-3", 5, 5, 0, 0)
	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{UserID: 7, ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[0].Requests, "只剩 gpt-4o 那条")
	require.Equal(t, int64(33), got[0].Tokens, "10+20+1+2")
}

func TestHourlyTrend_UserHourGran_NoModel_UsesUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 10, 20, 0, 0)
	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: false, UserID: 7}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1, "single hour bucket from usage_logs")
	require.Equal(t, int64(1), got[0].Requests)
	require.Equal(t, int64(30), got[0].Tokens)
}

// ---- Task 8: HourlyTrend 派生序列 ttft_ms / tps / cache_hit_rate ----

func TestHourlyTrend_DerivedSeries_CacheHitRate(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// prompt=200, cache_read=800 → cache_hit_rate = 800/(200+800)*100 = 80
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		OwnerType:       "admin",
		RequestCount:    10,
		SuccessCount:    10,
		PromptTokens:    200,
		CacheReadTokens: 800,
		TotalCost:       100,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	out, err := q.Stats().HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.InDelta(t, 80.0, out[0].CacheHitRate, 0.001, "800/(200+800)*100 = 80")
}

func TestHourlyTrend_DerivedSeries_CacheHitRate_ZeroDenominator(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// prompt=0, cache_read=0 → 分母为 0,cache_hit_rate 应为 0,不 panic/不除零
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		OwnerType:    "admin",
		RequestCount: 1,
		SuccessCount: 1,
		TotalCost:    10,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	out, err := q.Stats().HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, float64(0), out[0].CacheHitRate, "prompt+cache_read=0 → cache_hit_rate=0")
}

func TestHourlyTrend_DerivedSeries_TTFTAndTPS(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// stream_request_count=2, sum_first_response_ms=300 → ttft_ms = 300/2 = 150
	// sum_generation_ms=1000, sum_stream_completion_tokens=50 → tps = 50*1000/1000 = 50
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		OwnerType:                 "admin",
		RequestCount:              2,
		SuccessCount:              2,
		StreamRequestCount:        2,
		SumFirstResponseMs:        300,
		SumGenerationMs:           1000,
		SumStreamCompletionTokens: 50,
		TotalCost:                 20,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	out, err := q.Stats().HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, int64(150), out[0].TTFTMs, "sum_first_response_ms/stream_request_count = 300/2")
	require.InDelta(t, 50.0, out[0].TPS, 0.001, "sum_stream_completion_tokens*1000/sum_generation_ms = 50*1000/1000")
}

// seedUsageLogStream 播一条带 stream/cache 明细的 usage_log 行,专供 hourlyTrendFromUsageLog
// 派生序列(ttft/tps/cache_hit_rate)测试用;区别于 seedUsageLogModel(不设 IsStream/FirstResponseMs)。
func seedUsageLogStream(t *testing.T, db *gorm.DB, userID uint, ts int64, model string, prompt, completion, cacheRead, cacheWrite, durationMs, firstResponseMs int) {
	t.Helper()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: userID, ChannelID: 5, ModelName: model, AgentID: "cn-1",
		PromptTokens: prompt, CompletionTokens: completion,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		TotalCost: 100, Status: 1, IsStream: true, Duration: durationMs, FirstResponseMs: firstResponseMs,
		RequestID: fmt.Sprintf("seedstream-%d-%d-%s-%d", userID, ts, model, time.Now().UnixNano()), CreatedAt: ts,
	}).Error)
}

// seedTokenDailyCache 播一条带 cache 列的 token_daily_billings 行,专供
// hourlyTrendFromTokenDaily 的 cache_hit_rate 测试用(seedTokenDaily 不带 cache 参数)。
func seedTokenDailyCache(t *testing.T, db *gorm.DB, date string, userID, tokenID uint, tokenName string, reqs, prompt, completion, cacheRead, cacheWrite, cost int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.TokenDailyBilling{
		Date: date, UserID: userID, TokenID: tokenID, TokenName: tokenName,
		RequestCount: reqs, SuccessCount: reqs,
		PromptTokens: prompt, CompletionTokens: completion,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		TotalCost: cost,
	}).Error)
}

// TestHourlyTrend_UsageLogPath_DerivedSeries 锁定 hourlyTrendFromUsageLog(user-scoped 路径)
// 的 ttft/tps/cache_hit_rate 派生序列:此前这条路径恒传 0,对非 admin 用户是误导性假线。
// prompt=200,cache_read=800 → cache_hit_rate=800/(200+800)*100=80;
// completion=50,duration=1200,first_response_ms=200 → tps=50*1000/(1200-200)=50,ttft_ms=200。
func TestHourlyTrend_UsageLogPath_DerivedSeries(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC).Unix()
	seedUsageLogStream(t, db, 7, ts, "gpt-4o", 200, 50, 800, 0, 1200, 200)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: false, UserID: 7}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1, "single hour bucket from usage_logs")
	require.Equal(t, int64(200), got[0].TTFTMs, "first_response_ms/stream_request_count = 200/1")
	require.InDelta(t, 50.0, got[0].TPS, 0.001, "completion*1000/(duration-first_response_ms) = 50*1000/1000")
	require.InDelta(t, 80.0, got[0].CacheHitRate, 0.001, "800/(200+800)*100 = 80")
}

// TestHourlyTrend_UsageLogPath_DerivedSeries_NonStreamZero 验证非流式/失败行不会污染
// ttft/tps(usageLogStreamSelect 只对 is_stream=1 AND status=1 AND completion_tokens>0 计数),
// 但 cache_hit_rate 与 stream 无关,仍应算出非零值。
func TestHourlyTrend_UsageLogPath_DerivedSeries_NonStreamZero(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC).Unix()
	// 非流式请求: prompt=100, cache_read=100 → cache_hit_rate=50, 但 ttft/tps 应为 0
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 100, 20, 100, 0)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: false, UserID: 7}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(0), got[0].TTFTMs, "非流式行不计入 ttft")
	require.Equal(t, float64(0), got[0].TPS, "非流式行不计入 tps")
	require.InDelta(t, 50.0, got[0].CacheHitRate, 0.001, "100/(100+100)*100 = 50,与 stream 无关")
}

// TestHourlyTrend_TokenDailyPath_CacheHitRate_TTFTZero 锁定 hourlyTrendFromTokenDaily 路径:
// cache_hit_rate 靠 prompt/cache_read 列可算,ttft/tps 因该表无逐请求 stream 明细,
// 保持 0(真实限制,非遗漏)。
func TestHourlyTrend_TokenDailyPath_CacheHitRate_TTFTZero(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// prompt=300, cache_read=200 → cache_hit_rate = 200/(300+200)*100 = 40
	seedTokenDailyCache(t, db, "2026-05-20", 7, 1, "tk", 5, 300, 100, 200, 0, 999)

	got, err := q.Stats().HourlyTrend(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: false, UserID: 7}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(0), got[0].TTFTMs, "token_daily_billings 无 stream 明细,ttft 恒为 0")
	require.Equal(t, float64(0), got[0].TPS, "token_daily_billings 无 stream 明细,tps 恒为 0")
	require.InDelta(t, 40.0, got[0].CacheHitRate, 0.001, "200/(300+200)*100 = 40")
}

// ---- Task 4 (Distribution): model/user filter ----

func TestDistribution_ModelFilter_Admin(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 80, 800)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "claude-3", 20, 200)
	got, err := q.Stats().Distribution("model", ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "gpt-4o", got[0].Name)
	require.Equal(t, 1.0, got[0].Ratio, "单模型占比 100%")
}

func TestDistribution_AdminUserFilter_UsesUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 8, ts, "claude-3", 1, 1, 0, 0) // 别的用户,不该计入
	got, err := q.Stats().Distribution("model", ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "gpt-4o", got[0].Name)
	require.Equal(t, int64(2), got[0].Value)
}

// ---- Task 5: Leaderboard model/user filter ----

func TestLeaderboard_ByModel_ModelFilter(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 30, 300)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "claude-3", 50, 500)
	got, err := q.Stats().Leaderboard("model", "cost", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "gpt-4o", got[0].Name)
}

func TestLeaderboard_ByModel_AdminUserFilter_UsesUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 8, ts, "gpt-4o", 1, 1, 0, 0)
	got, err := q.Stats().Leaderboard("model", "requests", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[0].Requests, "只算用户 7")
}

func TestLeaderboard_ByUser_SingleUserFilter_ReturnsNil(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedTokenDaily(t, db, "2026-05-20", 7, 1, "tk", 5, 1, 1, 1)
	got, err := q.Stats().Leaderboard("user", "cost", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Nil(t, got, "锁定单用户时用户榜退化为 nil(前端隐藏)")
}

func TestLeaderboard_ByUser_ModelFilter_UsesBillingHourly(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 9, UserID: 7, TokenID: 7, ChannelID: 5, ModelName: "gpt-4o", RequestCount: 2})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 9, UserID: 8, TokenID: 8, ChannelID: 5, ModelName: "claude-3", RequestCount: 1})
	got, err := q.Stats().Leaderboard("user", "requests", 10, ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint(7), got[0].ID, "只有用 gpt-4o 的用户 7")
	require.Equal(t, int64(2), got[0].Requests)
}

// ---- Task 6: CostTrendStackedByModel model/user filter ----

func TestCostTrendStacked_ModelFilter(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 13, UserID: 7, TokenID: 7, ChannelID: 5, ModelName: "gpt-4o", RequestCount: 10, TotalCost: 100})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 13, UserID: 8, TokenID: 8, ChannelID: 5, ModelName: "claude-3", RequestCount: 5, TotalCost: 50})
	got, err := q.Stats().CostTrendStackedByModel(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, 5, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-4o"}, got.SeriesOrder, "只剩 gpt-4o 一条 series")
}

func TestCostTrendStacked_AdminUserFilter_UsesBillingHourly(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 9, UserID: 7, TokenID: 7, ChannelID: 5, ModelName: "gpt-4o", RequestCount: 1, TotalCost: 100})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 9, UserID: 8, TokenID: 8, ChannelID: 5, ModelName: "gpt-4o", RequestCount: 1, TotalCost: 100})
	got, err := q.Stats().CostTrendStackedByModel(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, 5, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-4o"}, got.SeriesOrder)
	require.Len(t, got.Buckets, 1)
	require.Equal(t, int64(100), got.Buckets[0].Series["gpt-4o"], "只用户 7 的 1 条 = 100")
}

// ---- Task 7: CacheSaving per-user + SpeedCompare model filter ----

func TestCacheSaving_AdminUserFilter_UsesBillingFacts(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// 用户 7:prompt=100, cache_read=100 → hit_ratio = 100/200 = 0.5
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 9, UserID: 7, TokenID: 7, ModelName: "gpt-4o",
		PromptTokens: 100, CacheReadTokens: 100, InputCost: 50, RequestCount: 1,
	}).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 9, UserID: 8, TokenID: 8, ModelName: "gpt-4o",
		PromptTokens: 100, CacheReadTokens: 0, InputCost: 50, RequestCount: 1,
	}).Error)
	got, err := q.Stats().CacheSaving(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.InDelta(t, 0.5, got.HitRatio, 0.0001, "只算用户 7")
	require.Equal(t, int64(100), got.ReadTokens)
}

func TestSpeedCompare_ModelFilter(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketSpeed(t, db, "gpt-4o", 100, 1000, 500)
	seedHourlyBucketSpeed(t, db, "claude-3", 200, 1000, 500)
	got, err := q.Stats().SpeedCompare("model", todayRangeDay(t), Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "gpt-4o", got[0].Name)
}

func TestCostTrendStacked_UserFilter_HourGran(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 13, UserID: 7, TokenID: 7, ChannelID: 5, ModelName: "gpt-4o", RequestCount: 2, TotalCost: 200})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 14, UserID: 7, TokenID: 7, ChannelID: 5, ModelName: "gpt-4o", RequestCount: 1, TotalCost: 100})
	got, err := q.Stats().CostTrendStackedByModel(ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: true}, 5, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-4o"}, got.SeriesOrder)
	require.Len(t, got.Buckets, 2, "13:00 and 14:00 hour buckets")
	byLabel := map[string]int64{}
	for _, b := range got.Buckets {
		byLabel[b.Label] = b.Series["gpt-4o"]
	}
	require.Equal(t, int64(200), byLabel["05-20 13:00"], "hour 13 = 2 reqs × 100")
	require.Equal(t, int64(100), byLabel["05-20 14:00"], "hour 14 = 1 req × 100")
}

// ---- Task 8: DashboardKpis filter: success_rate + active users ----

func TestDashboardKpis_UserFilter_RequestsFollow(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 10, 20, 0, 0)
	seedUsageLogModel(t, db, 8, ts, "gpt-4o", 1, 1, 0, 0)
	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().DashboardKpis(r, Scope{IsAdmin: true}, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Requests.Value, "只算用户 7 的 1 条请求")
	require.Equal(t, int64(30), got.Tokens.Value)
}

func TestDashboardKpis_ModelFilter_SuccessAndActiveUsers(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// success_rate 走 usage_hourly_buckets(有 model_name + success_count)
	seedHourlyBucketModel(t, db, "2026-05-20", 9, "gpt-4o", 1, 10)   // success_count=1
	seedHourlyBucketModel(t, db, "2026-05-20", 9, "claude-3", 1, 10) // 不该计入
	// success-rate 的现有路径仍读取 usage logs/hourly；active-users 改读 billing hourly。
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 8, ts, "claude-3", 1, 1, 0, 0)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 9, UserID: 7, TokenID: 7, ChannelID: 5, ModelName: "gpt-4o", RequestCount: 1})
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{Date: "2026-05-20", Hour: 9, UserID: 8, TokenID: 8, ChannelID: 5, ModelName: "claude-3", RequestCount: 1})
	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().DashboardKpis(r, Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.NotNil(t, got.SuccessRate)
	require.Equal(t, int64(1), got.SuccessRate.Value, "uhb 里只算 gpt-4o 的 success_count")
	require.NotNil(t, got.Users)
	require.Equal(t, int64(1), got.Users.Active, "billing hourly 里用 gpt-4o 的 distinct user = 仅 user 7")
}

func seedUsageLogTotals(t *testing.T, db *gorm.DB, reqID string, userID uint, status, duration int, ts int64) {
	t.Helper()
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: userID, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		Status: status, Duration: duration,
		RequestID: reqID, CreatedAt: ts,
	}).Error)
}

func TestLogsTotals_AggregatesCountsP95SlowestAndSparks(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	start := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC).Unix()
	r := ObsRange{Start: start, End: start + 3*3600, Gran: GranHour}

	seedUsageLogTotals(t, db, "logs-total-u1-100", 1, 1, 100, start+10)
	seedUsageLogTotals(t, db, "logs-total-u1-200", 1, 1, 200, start+20)
	seedUsageLogTotals(t, db, "logs-total-u1-failed", 1, 0, 999, start+3600+10)
	seedUsageLogTotals(t, db, "logs-total-u1-300", 1, 1, 300, start+2*3600+10)
	seedUsageLogTotals(t, db, "logs-total-u2-400", 2, 1, 400, start+2*3600+20)
	seedUsageLogTotals(t, db, "logs-total-u1-old", 1, 1, 10000, start-10)

	adminGot, err := q.Stats().LogsTotals(r, Scope{IsAdmin: true})
	require.NoError(t, err)
	require.Equal(t, int64(5), adminGot.Total)
	require.Equal(t, int64(1), adminGot.Failed)
	require.Equal(t, int64(400), adminGot.P95Ms)
	require.Equal(t, int64(400), adminGot.SlowestMs)
	require.GreaterOrEqual(t, len(adminGot.SparkTotal), 3)
	require.GreaterOrEqual(t, len(adminGot.SparkFailed), 3)
	require.GreaterOrEqual(t, len(adminGot.SparkP95), 3)
	require.Equal(t, []int64{2, 1, 2}, adminGot.SparkTotal[:3])
	require.Equal(t, []int64{0, 1, 0}, adminGot.SparkFailed[:3])
	require.Equal(t, []int64{200, 0, 400}, adminGot.SparkP95[:3])

	userGot, err := q.Stats().LogsTotals(r, Scope{IsAdmin: false, UserID: 1})
	require.NoError(t, err)
	require.Equal(t, int64(4), userGot.Total)
	require.Equal(t, int64(1), userGot.Failed)
	require.Equal(t, int64(300), userGot.P95Ms)
	require.Equal(t, int64(300), userGot.SlowestMs)
	require.GreaterOrEqual(t, len(userGot.SparkTotal), 3)
	require.GreaterOrEqual(t, len(userGot.SparkFailed), 3)
	require.GreaterOrEqual(t, len(userGot.SparkP95), 3)
	require.Equal(t, []int64{2, 1, 1}, userGot.SparkTotal[:3])
	require.Equal(t, []int64{0, 1, 0}, userGot.SparkFailed[:3])
	require.Equal(t, []int64{200, 0, 300}, userGot.SparkP95[:3])
}

// ---- Task 4.2: RecentAgentHealth ----

func TestRecentAgentHealth(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	db := ctx.GetCoreDB()
	now := time.Now().Unix()
	db.Create(&models.UsageLog{AgentID: "a1", Status: 1, CreatedAt: now - 10, RequestID: "x1"})
	db.Create(&models.UsageLog{AgentID: "a1", Status: 1, CreatedAt: now - 20, RequestID: "x2"})
	db.Create(&models.UsageLog{AgentID: "a1", Status: 0, CreatedAt: now - 30, RequestID: "x3"})
	db.Create(&models.UsageLog{AgentID: "a1", Status: 1, CreatedAt: now - 99999, RequestID: "old"})

	rows, err := NewAdminQuery(ctx).Stats().RecentAgentHealth(now - 300)
	require.NoError(t, err)
	var a1 *AgentRecentHealth
	for i := range rows {
		if rows[i].AgentID == "a1" {
			a1 = &rows[i]
		}
	}
	require.NotNil(t, a1)
	require.EqualValues(t, 3, a1.Requests)
	require.EqualValues(t, 1, a1.Failed)
}

// ---- Task 9: ChannelModelBreakdown ----

func seedHourlyBucketChannelModel(t *testing.T, db *gorm.DB, date string, hour int, channelID uint, model string,
	reqs, promptTok, completionTok, cacheRead, cacheWrite, totalCost, rawCost int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: hour,
		ChannelID: channelID, ModelName: model, AgentID: "cn-1",
		OwnerType:        "admin",
		RequestCount:     reqs,
		SuccessCount:     reqs,
		PromptTokens:     promptTok,
		CompletionTokens: completionTok,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		TotalCost:        totalCost,
		RawCost:          rawCost,
	}).Error)
}

func TestChannelModelBreakdown_GroupsByModel_OrderedByCostDesc(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// channel 5, model gpt-4o: two hourly rows same day → should sum together.
	seedHourlyBucketChannelModel(t, db, "2026-05-20", 10, 5, "gpt-4o", 10, 100, 50, 5, 2, 300, 400)
	seedHourlyBucketChannelModel(t, db, "2026-05-20", 11, 5, "gpt-4o", 5, 50, 25, 0, 0, 150, 200)
	// channel 5, model claude-3: single row, lower total_cost → should sort after gpt-4o.
	seedHourlyBucketChannelModel(t, db, "2026-05-20", 10, 5, "claude-3", 2, 20, 10, 1, 1, 50, 80)
	// other channel — must not leak into channel 5's breakdown.
	seedHourlyBucketChannelModel(t, db, "2026-05-20", 10, 7, "gpt-4o", 100, 1000, 500, 0, 0, 9000, 9000)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	rows, err := q.Stats().ChannelModelBreakdown(5, r)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	// Ordered DESC by total_cost: gpt-4o (300+150=450) before claude-3 (50).
	require.Equal(t, "gpt-4o", rows[0].ModelName)
	require.Equal(t, int64(15), rows[0].Requests)
	require.Equal(t, int64(150), rows[0].PromptTokens)
	require.Equal(t, int64(75), rows[0].CompletionTokens)
	require.Equal(t, int64(5), rows[0].CacheReadTokens)
	require.Equal(t, int64(2), rows[0].CacheWriteTokens)
	require.Equal(t, int64(450), rows[0].TotalCost)
	require.Equal(t, int64(600), rows[0].RawCost)

	require.Equal(t, "claude-3", rows[1].ModelName)
	require.Equal(t, int64(2), rows[1].Requests)
	require.Equal(t, int64(50), rows[1].TotalCost)
	require.Equal(t, int64(80), rows[1].RawCost)
}

func TestChannelModelBreakdown_NoData_ReturnsEmpty(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	rows, err := q.Stats().ChannelModelBreakdown(999, ObsRange{Start: 0, End: 100, Gran: GranDay})
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestChannelModelBreakdown_OutOfWindow_Excluded(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// Row falls entirely before the window start → excluded.
	seedHourlyBucketChannelModel(t, db, "2026-05-19", 23, 5, "gpt-4o", 10, 100, 50, 0, 0, 300, 300)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	rows, err := q.Stats().ChannelModelBreakdown(5, r)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// ---- Task 10: MarketShareTrend (model/channel dims) ----

func TestMarketShareTrend_ByModel_TwoBucketsTwoModels(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// hour 13: gpt-4o=150 tokens, claude-3=50 tokens
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 100, CompletionTokens: 50,
	}).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "claude-3",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 40, CompletionTokens: 10,
	}).Error)
	// hour 14: gpt-4o=30 tokens, claude-3=300 tokens → claude-3 wins overall (350 > 180)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 14, ChannelID: 5, ModelName: "gpt-4o",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 20, CompletionTokens: 10,
	}).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 14, ChannelID: 5, ModelName: "claude-3",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 200, CompletionTokens: 100,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}
	got, err := q.Stats().MarketShareTrend("model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 2, "hour 13 and hour 14 buckets")
	require.Equal(t, []string{"claude-3", "gpt-4o"}, got.SeriesOrder, "claude-3 total 350 > gpt-4o total 180")

	byLabel := map[string]map[string]int64{}
	for _, b := range got.Buckets {
		byLabel[b.Label] = b.Series
	}
	require.Equal(t, int64(150), byLabel["05-20 13:00"]["gpt-4o"])
	require.Equal(t, int64(50), byLabel["05-20 13:00"]["claude-3"])
	require.Equal(t, int64(30), byLabel["05-20 14:00"]["gpt-4o"])
	require.Equal(t, int64(300), byLabel["05-20 14:00"]["claude-3"])
}

func TestMarketShareTrend_ByChannel_UsesChannelNameWithFallback(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// channel 5 has a name → series uses it.
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 5, ChannelName: "openai-shared", ModelName: "gpt-4o",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 60, CompletionTokens: 40,
	}).Error)
	// channel 7 has no name recorded → fallback to the channel_id string.
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 7, ModelName: "claude-3",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 25, CompletionTokens: 15,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().MarketShareTrend("channel", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	require.ElementsMatch(t, []string{"openai-shared", "7"}, got.SeriesOrder)
	require.Equal(t, int64(100), got.Buckets[0].Series["openai-shared"])
	require.Equal(t, int64(40), got.Buckets[0].Series["7"], "channel 7 has no channel_name, falls back to id string")
}

func TestMarketShareTrend_InvalidDim_ReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	_, err := q.Stats().MarketShareTrend("author", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.ErrorIs(t, err, ErrUnsupportedMarketShareDim)
}

// ---- Task: MetricTrendGrouped (grouped metric time series by model/channel) ----
//
// Unlike MarketShareTrend (token量, 求和), several metrics here are ratios
// (ttft/tps/cache_hit_rate) whose numerator/denominator must be summed
// separately and divided once (weighted average), never simple-averaged or
// summed directly. cost/requests/tokens remain plain sums.

func TestMetricTrendGrouped_TTFT_ByModel_WeightedPerBucketAndRankedByRequests(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// modelA: fewer total requests (6) than modelB (13) -> modelB ranked first,
	// even though we're querying the ttft metric (ranking key is always requests).
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "modelA", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 5,
		StreamRequestCount: 2, SumFirstResponseMs: 300,
	}).Error)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "modelB", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 3,
		StreamRequestCount: 1, SumFirstResponseMs: 80,
	}).Error)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 14, ChannelID: 5, ModelName: "modelA", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 1,
		StreamRequestCount: 1, SumFirstResponseMs: 50,
	}).Error)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 14, ChannelID: 5, ModelName: "modelB", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 10,
		StreamRequestCount: 3, SumFirstResponseMs: 600,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}
	got, err := q.Stats().MetricTrendGrouped("ttft", "avg", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 2, "hour 13 and hour 14 buckets")
	require.Equal(t, []string{"modelB", "modelA"}, got.SeriesOrder, "modelB total requests 13 > modelA total 6")

	byLabel := map[string]map[string]float64{}
	for _, b := range got.Buckets {
		byLabel[b.Label] = b.Series
	}
	require.InDelta(t, 150.0, byLabel["05-20 13:00"]["modelA"], 0.001) // 300/2
	require.InDelta(t, 80.0, byLabel["05-20 13:00"]["modelB"], 0.001)  // 80/1
	require.InDelta(t, 50.0, byLabel["05-20 14:00"]["modelA"], 0.001)  // 50/1
	require.InDelta(t, 200.0, byLabel["05-20 14:00"]["modelB"], 0.001) // 600/3
}

func TestMetricTrendGrouped_Cost_SumsAndRanksByRequestsNotCost(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// modelA: high cost but low requests; modelB: low cost but high requests.
	// SeriesOrder must follow request volume, not the cost metric itself.
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 5, ModelName: "modelA", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 1, TotalCost: 1000,
	}).Error)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 5, ModelName: "modelB", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 10, TotalCost: 200,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().MetricTrendGrouped("cost", "sum", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	require.Equal(t, []string{"modelB", "modelA"}, got.SeriesOrder, "ranked by requests(10>1), not cost")
	require.Equal(t, float64(1000), got.Buckets[0].Series["modelA"])
	require.Equal(t, float64(200), got.Buckets[0].Series["modelB"])
}

func TestMetricTrendGrouped_OthersFold_WeightedAvgForTTFTAndPlainSumForCost(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// 4 models, topN=2 -> model1/model2 are top (highest requests),
	// model3/model4 fold into "others".
	seed := func(name string, requests, streamReqs, sumFirstResp, cost int64) {
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-20", Hour: 10, ChannelID: 5, ModelName: name, AgentID: "cn-1",
			OwnerType: "admin", RequestCount: requests,
			StreamRequestCount: streamReqs, SumFirstResponseMs: sumFirstResp, TotalCost: cost,
		}).Error)
	}
	seed("model1", 100, 10, 1000, 10000) // top
	seed("model2", 90, 5, 250, 5000)     // top
	seed("model3", 10, 4, 400, 400)      // others
	seed("model4", 5, 1, 50, 50)         // others

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}

	gotTTFT, err := q.Stats().MetricTrendGrouped("ttft", "avg", "model", r, Scope{IsAdmin: true}, 2, ObsFilter{})
	require.NoError(t, err)
	require.Contains(t, gotTTFT.SeriesOrder, "others")
	// weighted avg of folded model3/model4: (400+50)/(4+1) = 90, NOT naive average
	// of each model's own ttft ((100+50)/2 = 75).
	require.InDelta(t, 90.0, gotTTFT.Buckets[0].Series["others"], 0.001)

	gotCost, err := q.Stats().MetricTrendGrouped("cost", "sum", "model", r, Scope{IsAdmin: true}, 2, ObsFilter{})
	require.NoError(t, err)
	require.Contains(t, gotCost.SeriesOrder, "others")
	// plain sum of folded model3(400) + model4(50) = 450, NOT divided by fold count (2).
	require.Equal(t, float64(450), gotCost.Buckets[0].Series["others"])
}

func TestMetricTrendGrouped_InvalidMetric_ReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	_, err := q.Stats().MetricTrendGrouped("latency_p99", "avg", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.ErrorIs(t, err, ErrUnsupportedTrendMetric)
}

func TestMetricTrendGrouped_InvalidDim_ReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	_, err := q.Stats().MetricTrendGrouped("ttft", "avg", "author", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.ErrorIs(t, err, ErrUnsupportedTrendDim)
}

func TestMetricTrendGrouped_TTFT_NoStreamRequests_ReturnsZeroNotPanic(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// non-stream requests only: stream_request_count=0, sum_first_response_ms=0 -> denom 0.
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 5, ModelName: "modelA", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 5,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	require.NotPanics(t, func() {
		got, err := q.Stats().MetricTrendGrouped("ttft", "avg", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
		require.NoError(t, err)
		require.Equal(t, float64(0), got.Buckets[0].Series["modelA"])
	})
}

func TestMetricTrendGrouped_NonAdmin_ReturnsEmpty(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().MetricTrendGrouped("cost", "sum", "model", r, Scope{IsAdmin: false, UserID: 1}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Empty(t, got.Buckets)
	require.Empty(t, got.SeriesOrder)
}

// TestMetricTrendGrouped_TPS_ExactValue_GuardsThousandMultiplier locks tps's
// numerator scaling (*1000, tokens/ms -> tokens/sec) to a known value so an
// accidental removal of the *1000 factor fails loudly instead of silently
// shrinking every tps-derived chart by three orders of magnitude.
func TestMetricTrendGrouped_TPS_ExactValue_GuardsThousandMultiplier(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// tps = sum_stream_completion_tokens * 1000 / sum_generation_ms
	//     = 600 * 1000 / 2000 = 300
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 5, ModelName: "modelX", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 1,
		SumStreamCompletionTokens: 600, SumGenerationMs: 2000,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().MetricTrendGrouped("tps", "avg", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	require.InDelta(t, 300.0, got.Buckets[0].Series["modelX"], 0.001,
		"tps must apply the *1000 ms->sec conversion; a stripped multiplier yields 0.3 here")
}

// TestMetricTrendGrouped_CacheHitRate_ExactValue_GuardsHundredMultiplier locks
// cache_hit_rate's numerator scaling (*100, ratio -> percentage) to a known
// value so an accidental removal of the *100 factor fails loudly instead of
// silently reporting a 0-1 fraction where callers expect a 0-100 percentage.
func TestMetricTrendGrouped_CacheHitRate_ExactValue_GuardsHundredMultiplier(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// cache_hit_rate = cache_read_tokens * 100 / (prompt_tokens + cache_read_tokens)
	//                = 800 * 100 / (200 + 800) = 80
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 5, ModelName: "modelY", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 1,
		PromptTokens: 200, CacheReadTokens: 800,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().MetricTrendGrouped("cache_hit_rate", "ratio", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	require.InDelta(t, 80.0, got.Buckets[0].Series["modelY"], 0.001,
		"cache_hit_rate must apply the *100 percentage conversion; a stripped multiplier yields 0.8 here")
}

// TestMetricTrendGrouped_ByChannel_SeriesNamedByChannelName_FallsBackToID
// covers dim="channel", which had zero test coverage: every other
// MetricTrendGrouped test above only exercises dim="model". This locks in
// metricTrendStackRows' channel nameExpr (COALESCE(NULLIF(MAX(channel_name),
// ”), CAST(channel_id AS TEXT))): series must be labelled by the human
// channel_name, falling back to the numeric channel_id (as a string) only
// when channel_name is empty (e.g. orphan/BYOK channel rows).
func TestMetricTrendGrouped_ByChannel_SeriesNamedByChannelName_FallsBackToID(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 5, ChannelName: "chan-five", ModelName: "gpt-4o", AgentID: "cn-1",
		OwnerType: "admin", RequestCount: 10, TotalCost: 100,
	}).Error)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 7, ChannelName: "chan-seven", ModelName: "gpt-4o", AgentID: "cn-2",
		OwnerType: "admin", RequestCount: 20, TotalCost: 50,
	}).Error)
	// channel_name empty -> series name must fall back to the channel_id string.
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-20", Hour: 10, ChannelID: 99, ChannelName: "", ModelName: "gpt-4o", AgentID: "cn-3",
		OwnerType: "admin", RequestCount: 5, TotalCost: 10,
	}).Error)

	r := ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
	got, err := q.Stats().MetricTrendGrouped("cost", "sum", "channel", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	require.Equal(t, []string{"chan-seven", "chan-five", "99"}, got.SeriesOrder,
		"series named by channel_name (ranked by requests: 20 > 10 > 5), empty name falls back to channel_id")

	series := got.Buckets[0].Series
	require.Equal(t, float64(100), series["chan-five"])
	require.Equal(t, float64(50), series["chan-seven"])
	require.Equal(t, float64(10), series["99"], "empty channel_name falls back to channel_id as string")
}

func TestChartRankingStableTieOrderingAndOthersExact(t *testing.T) {
	r := ObsRange{Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(), End: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(), Gran: GranDay}
	rows := []stackRow{
		{Date: "2026-05-20", Name: "zeta", Cost: 10},
		{Date: "2026-05-20", Name: "alpha", Cost: 10},
		{Date: "2026-05-20", Name: "hidden-b", Cost: 4},
		{Date: "2026-05-20", Name: "hidden-a", Cost: 3},
	}
	for i := 0; i < 20; i++ {
		got := assembleCostStacked(rows, r, 2)
		require.Equal(t, []string{"alpha", "zeta", "others"}, got.SeriesOrder)
		require.Equal(t, int64(7), got.Buckets[0].Series["others"])
	}

	metricRows := []metricStackRow{
		{Date: "2026-05-20", Name: "zeta", Requests: 10, Num: 100, Den: 1},
		{Date: "2026-05-20", Name: "alpha", Requests: 10, Num: 1, Den: 1},
		{Date: "2026-05-20", Name: "hidden", Requests: 1, Num: 9, Den: 1},
	}
	gotMetric := assembleMetricStacked(metricRows, r, 2, false)
	require.Equal(t, []string{"alpha", "zeta", "others"}, gotMetric.SeriesOrder)
	require.Equal(t, float64(9), gotMetric.Buckets[0].Series["others"])
}
