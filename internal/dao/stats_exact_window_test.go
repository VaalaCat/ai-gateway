package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

type exactBillingFact struct {
	offset int64
	userID uint
	model  string
	cost   int64
}

func seedExactBillingFacts(t *testing.T, facts []exactBillingFact) (*adminStatsQuery, int64) {
	t.Helper()
	q, db := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Unix()
	for userID := uint(1); userID <= 3; userID++ {
		require.NoError(t, db.Create(&models.User{ID: userID, Username: fmt.Sprintf("user-%d", userID)}).Error)
	}
	type hourlyKey struct {
		hour  int
		model string
	}
	hourly := make(map[hourlyKey]*models.UsageHourlyBucket)
	for index, fact := range facts {
		createdAt := base + fact.offset
		tokenID := uint(index + 1)
		require.NoError(t, db.Select("*").Create(&models.UsageLog{
			RequestID: fmt.Sprintf("exact-%02d", index), UserID: fact.userID, TokenID: tokenID,
			ChannelID: 1, ModelName: fact.model, PromptTokens: 10, CompletionTokens: 5,
			TotalCost: fact.cost, Status: 1, CreatedAt: createdAt,
		}).Error)
		key := hourlyKey{hour: time.Unix(createdAt, 0).UTC().Hour(), model: fact.model}
		bucket := hourly[key]
		if bucket == nil {
			bucket = &models.UsageHourlyBucket{
				Date: "2026-07-20", Hour: key.hour, ChannelID: 1,
				ModelName: key.model, AgentID: "fixture", OwnerType: "admin",
			}
			hourly[key] = bucket
		}
		bucket.RequestCount++
		bucket.SuccessCount++
		bucket.PromptTokens += 10
		bucket.CompletionTokens += 5
		bucket.TotalCost += fact.cost
	}
	for _, bucket := range hourly {
		require.NoError(t, db.Create(bucket).Error)
	}
	return q, base
}

func TestBillingHourlyHybridMatchesRawFactsAtExactWindowBoundaries(t *testing.T) {
	facts := []exactBillingFact{
		{offset: 0, userID: 1, model: "gpt-5", cost: 10},
		{offset: 1, userID: 2, model: "gpt-5", cost: 20},
		{offset: 1200, userID: 1, model: "other", cost: 15},
		{offset: 3598, userID: 2, model: "gpt-5", cost: 30},
		{offset: 3599, userID: 3, model: "gpt-5", cost: 40},
		{offset: 3600, userID: 1, model: "gpt-5", cost: 50},
		{offset: 3601, userID: 2, model: "gpt-5", cost: 60},
		{offset: 5400, userID: 3, model: "other", cost: 25},
		{offset: 7198, userID: 1, model: "gpt-5", cost: 70},
		{offset: 7199, userID: 2, model: "gpt-5", cost: 80},
		{offset: 7200, userID: 3, model: "gpt-5", cost: 90},
	}
	q, base := seedExactBillingFacts(t, facts)
	cases := []struct {
		name       string
		start, end int64
	}{
		{name: "aligned hours", start: 0, end: 7200},
		{name: "start plus one second", start: 1, end: 7200},
		{name: "end at fifty nine fifty nine", start: 0, end: 7199},
		{name: "same partial hour", start: 1, end: 3599},
		{name: "cross hour by two seconds", start: 3599, end: 3601},
		{name: "no full interior at partial boundaries", start: 1200, end: 5400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ObsRange{Start: base + tc.start, End: base + tc.end, Gran: GranHour}
			activeUsers := map[uint]struct{}{}
			var wantCost int64
			wantLeaders := map[uint]LeaderRow{}
			for _, fact := range facts {
				if fact.offset < tc.start || fact.offset >= tc.end {
					continue
				}
				if fact.userID > 0 {
					activeUsers[fact.userID] = struct{}{}
				}
				wantCost += fact.cost
				if fact.model == "gpt-5" && fact.userID > 0 {
					row := wantLeaders[fact.userID]
					row.ID = fact.userID
					row.Name = fmt.Sprintf("user-%d", fact.userID)
					row.Cost += fact.cost
					row.Requests++
					row.Tokens += 15
					wantLeaders[fact.userID] = row
				}
			}

			logDB, requestLogModel, modelErr := q.ctx.RequestLogModel()
			require.NoError(t, modelErr)
			users, err := kpiUsers(q.ctx.GetCoreDB(), logDB, requestLogModel, r, ObsFilter{})
			require.NoError(t, err)
			require.Equal(t, int64(len(activeUsers)), users.Active)

			trend, err := q.CostTrendStackedByModel(r, Scope{IsAdmin: true}, 10, ObsFilter{})
			require.NoError(t, err)
			var gotCost int64
			for _, bucket := range trend.Buckets {
				for _, cost := range bucket.Series {
					gotCost += cost
				}
			}
			require.Equal(t, wantCost, gotCost)

			leaders, err := q.Leaderboard("user", "requests", 10, r, Scope{IsAdmin: true}, ObsFilter{ModelName: "gpt-5"})
			require.NoError(t, err)
			gotLeaders := make(map[uint]LeaderRow, len(leaders))
			for _, row := range leaders {
				gotLeaders[row.ID] = row
			}
			require.Equal(t, wantLeaders, gotLeaders)
		})
	}
	t.Run("day granularity merges full and boundary sources", func(t *testing.T) {
		r := ObsRange{Start: base + 1, End: base + 7199, Gran: GranDay}
		trend, err := q.CostTrendStackedByModel(r, Scope{IsAdmin: true}, 10, ObsFilter{})
		require.NoError(t, err)
		require.Len(t, trend.Buckets, 1)
		var total int64
		for _, cost := range trend.Buckets[0].Series {
			total += cost
		}
		require.Equal(t, int64(310), total)
	})
}
