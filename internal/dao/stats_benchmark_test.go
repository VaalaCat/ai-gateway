//go:build !race

package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const productionStatsBudget = 200 * time.Millisecond

type billingHourlyStatsFixture struct {
	db       *gorm.DB
	rangeArg ObsRange
	rowCount int
}

func newBillingHourlyStatsFixture(tb testing.TB) billingHourlyStatsFixture {
	tb.Helper()
	dsn := fmt.Sprintf("file:stats-benchmark-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(tb, err)
	sqlDB, err := db.DB()
	require.NoError(tb, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	tb.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(tb, models.MigrateCoreDB(db))

	users := make([]models.User, 0, 100)
	for userID := 1; userID <= 100; userID++ {
		users = append(users, models.User{ID: uint(userID), Username: fmt.Sprintf("user-%03d", userID)})
	}
	require.NoError(tb, db.CreateInBatches(users, 100).Error)

	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	modelsUsed := []string{"gpt-5", "gpt-5-mini", "claude-4"}
	rows := make([]models.BillingHourlyBucket, 0, 1008)
	rowCount := 0
	flush := func() {
		tb.Helper()
		if len(rows) == 0 {
			return
		}
		require.NoError(tb, db.CreateInBatches(rows, 1000).Error)
		rows = rows[:0]
	}
	for hourOffset := 0; hourOffset < 7*24; hourOffset++ {
		bucketTime := start.Add(time.Duration(hourOffset) * time.Hour)
		for userID := 1; userID <= 100; userID++ {
			for modelIndex, modelName := range modelsUsed {
				for channelID := 1; channelID <= 2; channelID++ {
					requestCount := int64(1 + (userID+hourOffset+modelIndex+channelID)%5)
					rows = append(rows, models.BillingHourlyBucket{
						Date: bucketTime.Format("2006-01-02"), Hour: bucketTime.Hour(),
						UserID: uint(userID), TokenID: uint(userID*10 + modelIndex), ChannelID: uint(channelID),
						OwnerType: "admin", ModelName: modelName, RequestCount: requestCount,
						SuccessCount: requestCount, PromptTokens: requestCount * 100,
						CompletionTokens: requestCount * 50, TotalCost: requestCount * 25,
					})
					rowCount++
					if len(rows) == cap(rows) {
						flush()
					}
				}
			}
		}
	}
	// Add six months of history for the benchmarked user outside the 7-day
	// window. The user-window index must keep the query bounded by date/hour,
	// rather than scanning this user's entire history.
	for hourOffset := 180 * 24; hourOffset > 0; hourOffset-- {
		bucketTime := start.Add(-time.Duration(hourOffset) * time.Hour)
		for modelIndex, modelName := range modelsUsed {
			for channelID := 1; channelID <= 2; channelID++ {
				rows = append(rows, models.BillingHourlyBucket{
					Date: bucketTime.Format("2006-01-02"), Hour: bucketTime.Hour(),
					UserID: 50, TokenID: uint(500 + modelIndex), ChannelID: uint(channelID),
					OwnerType: "admin", ModelName: modelName, RequestCount: 3,
					SuccessCount: 3, PromptTokens: 300, CompletionTokens: 150, TotalCost: 75,
				})
				rowCount++
				if len(rows) == cap(rows) {
					flush()
				}
			}
		}
	}
	flush()

	return billingHourlyStatsFixture{
		db: db, rowCount: rowCount,
		rangeArg: ObsRange{Start: start.Unix(), End: start.Add(7 * 24 * time.Hour).Unix(), Gran: GranHour},
	}
}

func TestProductionScaleStatsUnderBudget(t *testing.T) {
	fixture := newBillingHourlyStatsFixture(t)
	type measurement struct {
		name string
		run  func() error
	}
	measurements := []measurement{
		{name: "active users", run: func() error {
			_, err := kpiUsers(fixture.db, fixture.rangeArg, ObsFilter{})
			return err
		}},
		{name: "cost trend", run: func() error {
			_, err := costStackRowsFromBillingHourly(fixture.db, fixture.rangeArg, 50, 0, "")
			return err
		}},
		{name: "model user leaderboard", run: func() error {
			_, err := leaderboardByUserFromBillingHourly(fixture.db, "cost", 20, fixture.rangeArg, "gpt-5")
			return err
		}},
	}
	for _, measurement := range measurements {
		started := time.Now()
		err := measurement.run()
		elapsed := time.Since(started)
		require.NoError(t, err, measurement.name)
		t.Logf("rows=%d query=%q elapsed=%s", fixture.rowCount, measurement.name, elapsed)
		require.Less(t, elapsed, productionStatsBudget, measurement.name)
	}
}

var billingHourlyStatsBenchmarkSink any

func BenchmarkBillingHourlyStats(b *testing.B) {
	fixture := newBillingHourlyStatsFixture(b)
	b.Logf("rows=%d", fixture.rowCount)
	b.Run("ActiveUsers", func(b *testing.B) {
		for range b.N {
			var err error
			billingHourlyStatsBenchmarkSink, err = kpiUsers(fixture.db, fixture.rangeArg, ObsFilter{})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("CostTrend", func(b *testing.B) {
		for range b.N {
			var err error
			billingHourlyStatsBenchmarkSink, err = costStackRowsFromBillingHourly(fixture.db, fixture.rangeArg, 50, 0, "")
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ModelUserLeaderboard", func(b *testing.B) {
		for range b.N {
			var err error
			billingHourlyStatsBenchmarkSink, err = leaderboardByUserFromBillingHourly(fixture.db, "cost", 20, fixture.rangeArg, "gpt-5")
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
