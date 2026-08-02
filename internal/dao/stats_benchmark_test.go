//go:build !race

package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const productionStatsBudget = 200 * time.Millisecond

type statsRoutingFixture struct {
	query    AdminStatsQuery
	rangeArg ObsRange
	rowCount int
}

func newStatsRoutingFixture(tb testing.TB) statsRoutingFixture {
	tb.Helper()
	open := func(role string, migrate func(*gorm.DB) error) *gorm.DB {
		dsn := fmt.Sprintf("file:stats-routing-%s-%d?mode=memory&cache=shared", role, time.Now().UnixNano())
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(tb, err)
		sqlDB, err := db.DB()
		require.NoError(tb, err)
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		tb.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(tb, migrate(db))
		return db
	}
	core := open("core", models.MigrateCoreDB)
	logs := open("log", models.MigrateLogDB)
	require.NoError(tb, core.Create(&models.User{ID: 7, Username: "fixture-user"}).Error)

	start := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	hourly := []models.UsageHourlyBucket{
		{Date: "2026-07-16", Hour: 10, ChannelID: 1, ModelName: "wanted", AgentID: "a", RequestCount: 2, SuccessCount: 2, PromptTokens: 20, TotalCost: 4},
		{Date: "2026-07-16", Hour: 11, ChannelID: 1, ModelName: "wanted", AgentID: "a", RequestCount: 3, SuccessCount: 3, PromptTokens: 30, TotalCost: 6},
		{Date: "2026-07-16", Hour: 11, ChannelID: 2, ModelName: "other", AgentID: "a", RequestCount: 1, SuccessCount: 1, PromptTokens: 10, TotalCost: 2},
	}
	require.NoError(tb, logs.Create(&hourly).Error)
	requests := []models.RequestLog{
		models.RequestLog(models.UsageLog{RequestID: "left", UserID: 7, ModelName: "wanted", Status: 1, PromptTokens: 2, TotalCost: 1, CreatedAt: start.Unix() + 10}),
		models.RequestLog(models.UsageLog{RequestID: "full-1", UserID: 7, ModelName: "wanted", Status: 1, PromptTokens: 3, TotalCost: 2, CreatedAt: start.Unix() + 3600}),
		models.RequestLog(models.UsageLog{RequestID: "full-2", UserID: 8, ModelName: "other", Status: 1, PromptTokens: 4, TotalCost: 3, CreatedAt: start.Unix() + 3601}),
		models.RequestLog(models.UsageLog{RequestID: "right", UserID: 7, ModelName: "wanted", Status: 1, PromptTokens: 5, TotalCost: 4, CreatedAt: start.Unix() + 7200}),
	}
	require.NoError(tb, logs.Create(&requests).Error)

	provider := &testApp{db: core, logDB: logs, layoutMode: app.DatabaseLayoutSplit}
	return statsRoutingFixture{
		query:    NewAdminQuery(NewContext(provider)).Stats(),
		rangeArg: ObsRange{Start: start.Unix() + 10, End: start.Unix() + 7210, Gran: GranHour},
		rowCount: len(hourly) + len(requests),
	}
}

func statsRoutingMeasurements(fixture statsRoutingFixture) []struct {
	name string
	run  func() error
} {
	return []struct {
		name string
		run  func() error
	}{
		{name: "dashboard kpis", run: func() error {
			_, err := fixture.query.DashboardKpis(fixture.rangeArg, Scope{IsAdmin: true}, ObsFilter{})
			return err
		}},
		{name: "global cost trend", run: func() error {
			_, err := fixture.query.CostTrendStackedByModel(fixture.rangeArg, Scope{IsAdmin: true}, 5, ObsFilter{})
			return err
		}},
		{name: "model user leaderboard", run: func() error {
			_, err := fixture.query.Leaderboard("user", "cost", 5, fixture.rangeArg, Scope{IsAdmin: true}, ObsFilter{ModelName: "wanted"})
			return err
		}},
	}
}

func TestProductionScaleStatsUnderBudget(t *testing.T) {
	fixture := newStatsRoutingFixture(t)
	for _, measurement := range statsRoutingMeasurements(fixture) {
		started := time.Now()
		err := measurement.run()
		elapsed := time.Since(started)
		require.NoError(t, err, measurement.name)
		t.Logf("rows=%d query=%q elapsed=%s", fixture.rowCount, measurement.name, elapsed)
		require.Less(t, elapsed, productionStatsBudget, measurement.name)
	}
}

var statsRoutingBenchmarkSink any

func BenchmarkStatsRouting(b *testing.B) {
	fixture := newStatsRoutingFixture(b)
	b.Logf("rows=%d", fixture.rowCount)
	for _, measurement := range statsRoutingMeasurements(fixture) {
		b.Run(measurement.name, func(b *testing.B) {
			for range b.N {
				if err := measurement.run(); err != nil {
					b.Fatal(err)
				}
				statsRoutingBenchmarkSink = measurement.name
			}
		})
	}
}
