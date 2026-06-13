package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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
	// p95 fields are placeholders until Task 2.8.
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

// seedHourlyBucketCache 插入一条含 cache token 字段的 usage_hourly_bucket 行。
func seedHourlyBucketCache(t *testing.T, db *gorm.DB, date string, prompt, cacheRead, cacheWrite, inputCost int64) {
	t.Helper()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
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
	fromLog, err := hourlyTrendFromUsageLog(db, r, 7, "")
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

func TestLeaderboard_ByUser_ModelFilter_UsesUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 8, ts, "claude-3", 1, 1, 0, 0)
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
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 10, 100)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "claude-3", 5, 50)
	got, err := q.Stats().CostTrendStackedByModel(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}, Scope{IsAdmin: true}, 5, ObsFilter{ModelName: "gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-4o"}, got.SeriesOrder, "只剩 gpt-4o 一条 series")
}

func TestCostTrendStacked_AdminUserFilter_UsesUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0) // TotalCost=100 per seedUsageLogModel
	seedUsageLogModel(t, db, 8, ts, "gpt-4o", 1, 1, 0, 0)
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

func TestCacheSaving_AdminUserFilter_UsesUsageLog(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	// 用户 7:prompt=100, cache_read=100 → hit_ratio = 100/200 = 0.5
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: 7, ModelName: "gpt-4o", PromptTokens: 100, CacheReadTokens: 100,
		InputCost: 50, Status: 1, RequestID: "cs-7", CreatedAt: ts,
	}).Error)
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		UserID: 8, ModelName: "gpt-4o", PromptTokens: 100, CacheReadTokens: 0,
		InputCost: 50, Status: 1, RequestID: "cs-8", CreatedAt: ts,
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
	// two requests for user 7 in the same hour (13:xx), one in 14:xx
	seedUsageLogModel(t, db, 7, time.Date(2026, 5, 20, 13, 10, 0, 0, time.UTC).Unix(), "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 7, time.Date(2026, 5, 20, 13, 50, 0, 0, time.UTC).Unix(), "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 7, time.Date(2026, 5, 20, 14, 5, 0, 0, time.UTC).Unix(), "gpt-4o", 1, 1, 0, 0)
	got, err := q.Stats().CostTrendStackedByModel(ObsRange{
		Start: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranHour,
	}, Scope{IsAdmin: true}, 5, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-4o"}, got.SeriesOrder)
	require.Len(t, got.Buckets, 2, "13:00 and 14:00 hour buckets")
	// seedUsageLogModel sets TotalCost=100 per row → hour 13 = 200, hour 14 = 100
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
	// active-users 走 usage_logs(uhb 无 user_id)
	ts := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC).Unix()
	seedUsageLogModel(t, db, 7, ts, "gpt-4o", 1, 1, 0, 0)
	seedUsageLogModel(t, db, 8, ts, "claude-3", 1, 1, 0, 0)
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
	require.Equal(t, int64(1), got.Users.Active, "usage_logs 里用 gpt-4o 的 distinct user = 仅 user 7")
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
	db := ctx.GetDB()
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
