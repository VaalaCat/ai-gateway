package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPrepareLegacyMigrationFixtureUsesOnlineSplitPaths(t *testing.T) {
	root := newFixtureTestRoot(t, "online-legacy-paths")
	require.NoError(t, prepareLegacyMigrationFixture(
		root, ":8340", "http://localhost:8341", "http://127.0.0.1:8342", 1, 20, 10,
	))

	cfg, err := config.LoadMaster(filepath.Join(root, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "core.db"), cfg.Master.DBPath)
	require.Equal(t, filepath.Join(root, "log.db"), cfg.Master.LogDBPath)
	require.Equal(t, filepath.Join(root, "master.db"), cfg.Master.LegacyDBPath)
	require.FileExists(t, filepath.Join(root, "master.db"))
	var summary legacyHistorySummary
	encoded, err := os.ReadFile(filepath.Join(root, "legacy-summary.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &summary))
	require.Equal(t, int64(160016), summary.Seed)
	require.Positive(t, summary.SeedDurationMS)
	_, err = os.Stat(filepath.Join(root, "core.db"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(root, "log.db"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAppendLegacyTailAddsSixPostReadyScenarios(t *testing.T) {
	root := newFixtureTestRoot(t, "append-legacy-tail")
	require.NoError(t, prepareLegacyMigrationFixture(
		root, ":8340", "http://localhost:8341", "http://127.0.0.1:8342", 1, 20, 10,
	))

	requestIDs, err := appendLegacyTail(root)
	require.NoError(t, err)
	require.Equal(t, []string{
		"large-tail-normal", "large-tail-stream", "large-tail-429",
		"large-tail-500", "large-tail-timeout", "large-tail-connection",
	}, requestIDs)

	db, err := gorm.Open(sqlite.Open(filepath.Join(root, "master.db")), &gorm.Config{})
	require.NoError(t, err)
	var usage []models.UsageLog
	require.NoError(t, db.Where("request_id IN ?", requestIDs).Order("id").Find(&usage).Error)
	require.Len(t, usage, 6)
	require.Equal(t, uint(21), usage[0].ID)
	require.Equal(t, uint(26), usage[5].ID)
	require.Equal(t, []bool{false, true, false, false, false, false}, []bool{
		usage[0].IsStream, usage[1].IsStream, usage[2].IsStream,
		usage[3].IsStream, usage[4].IsStream, usage[5].IsStream,
	})
	for _, row := range usage {
		require.True(t, row.HasTrace)
	}
	var traces int64
	require.NoError(t, db.Model(&models.UsageLogTrace{}).Where("request_id IN ?", requestIDs).Count(&traces).Error)
	require.EqualValues(t, 6, traces)
}

func TestAppendLegacyTailRejectsDuplicateScenarioKeys(t *testing.T) {
	root := newFixtureTestRoot(t, "append-legacy-tail-duplicate")
	require.NoError(t, prepareLegacyMigrationFixture(
		root, ":8340", "http://localhost:8341", "http://127.0.0.1:8342", 1, 20, 10,
	))
	_, err := appendLegacyTail(root)
	require.NoError(t, err)
	_, err = appendLegacyTail(root)
	require.ErrorContains(t, err, "already exists")
}

func TestAppendLegacyTailRejectsMissingLegacyDatabase(t *testing.T) {
	root := newFixtureTestRoot(t, "append-legacy-tail-missing")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte(fixtureMarkerContents), 0o600))

	_, err := appendLegacyTail(root)
	require.ErrorContains(t, err, "master.db")
}

func TestAppendLegacyTailRejectsHardlinkedLegacyDatabase(t *testing.T) {
	root := newFixtureTestRoot(t, "append-legacy-tail-hardlink")
	require.NoError(t, prepareLegacyMigrationFixture(
		root, ":8340", "http://localhost:8341", "http://127.0.0.1:8342", 1, 20, 10,
	))
	outsideLink := filepath.Join(t.TempDir(), "master.db")
	require.NoError(t, os.Link(filepath.Join(root, "master.db"), outsideLink))

	_, err := appendLegacyTail(root)
	require.ErrorContains(t, err, "hardlink")
}

func TestAppendLegacyTailRejectsSymlinkedLegacyDatabase(t *testing.T) {
	root := newFixtureTestRoot(t, "append-legacy-tail-symlink")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte(fixtureMarkerContents), 0o600))
	target := filepath.Join(t.TempDir(), "master.db")
	require.NoError(t, os.WriteFile(target, []byte("outside"), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(root, "master.db")))

	_, err := appendLegacyTail(root)
	require.ErrorContains(t, err, "regular file")
}

func TestAppendLegacyTailRejectsInvalidFixtureMarker(t *testing.T) {
	root := newFixtureTestRoot(t, "append-legacy-tail-marker")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte("not-owned\n"), 0o600))

	_, err := appendLegacyTail(root)
	require.ErrorContains(t, err, "valid temporary fixture marker")
}

func TestSeedLegacyHistoryKeepsExactDailyAndTraceRatios(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))

	start := time.Date(2026, time.April, 26, 0, 0, 0, 0, time.UTC)
	summary, err := seedLegacyHistory(db, legacyHistoryOptions{
		Days: 2, RequestsPerDay: 20, TraceEvery: 10, BatchSize: 7,
		Start: start, MockUpstreamURL: "http://127.0.0.1:8342",
	})
	require.NoError(t, err)
	require.EqualValues(t, 40, summary.Requests)
	require.EqualValues(t, 4, summary.Traces)
	require.Equal(t, int64(160016), summary.Seed)

	var channel models.Channel
	require.NoError(t, db.First(&channel, 1).Error)
	require.NotNil(t, channel.Resilience.Data().BreakerEnabled)
	require.False(t, *channel.Resilience.Data().BreakerEnabled)

	var daily []struct {
		Day  string
		Rows int64
	}
	require.NoError(t, db.Model(&models.UsageLog{}).
		Select("date(created_at, 'unixepoch') AS day, COUNT(*) AS rows").
		Group("day").Order("day").Scan(&daily).Error)
	require.Equal(t, []struct {
		Day  string
		Rows int64
	}{{"2026-04-26", 20}, {"2026-04-27", 20}}, daily)

	var traceCount, tracedRequests int64
	require.NoError(t, db.Model(&models.UsageLogTrace{}).Count(&traceCount).Error)
	require.NoError(t, db.Model(&models.UsageLog{}).Where("has_trace = ?", true).Count(&tracedRequests).Error)
	require.EqualValues(t, 4, traceCount)
	require.EqualValues(t, 4, tracedRequests)

	var orphanTraces int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM usage_log_traces t LEFT JOIN usage_logs u ON u.request_id = t.request_id WHERE u.id IS NULL`).Scan(&orphanTraces).Error)
	require.Zero(t, orphanTraces)

	var hourlyTotals struct {
		PromptTokens     int64
		CompletionTokens int64
		TotalCost        int64
	}
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).
		Select("COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(total_cost), 0) AS total_cost").
		Scan(&hourlyTotals).Error)
	require.Equal(t, summary.PromptTokens, hourlyTotals.PromptTokens)
	require.Equal(t, summary.OutputTokens, hourlyTotals.CompletionTokens)
	require.Equal(t, summary.TotalCost, hourlyTotals.TotalCost)

	for _, model := range []any{&models.TokenDailyBilling{}, &models.ChannelDailyBilling{}} {
		var dailyTotals struct {
			Rows             int64
			Requests         int64
			PromptTokens     int64
			CompletionTokens int64
			TotalCost        int64
		}
		require.NoError(t, db.Model(model).
			Select("COUNT(*) AS rows, COALESCE(SUM(request_count), 0) AS requests, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(total_cost), 0) AS total_cost").
			Scan(&dailyTotals).Error)
		require.EqualValues(t, 2, dailyTotals.Rows)
		require.Equal(t, summary.Requests, dailyTotals.Requests)
		require.Equal(t, summary.PromptTokens, dailyTotals.PromptTokens)
		require.Equal(t, summary.OutputTokens, dailyTotals.CompletionTokens)
		require.Equal(t, summary.TotalCost, dailyTotals.TotalCost)
	}
	require.True(t, db.Migrator().HasTable(&models.UsageLog{}))
	require.False(t, db.Migrator().HasTable("database_layouts"))
}

func TestValidateLegacyHistoryOptionsRejectsInexactTraceRatio(t *testing.T) {
	for _, opts := range []legacyHistoryOptions{
		{},
		{Days: 1, RequestsPerDay: 9, TraceEvery: 10, BatchSize: 10, Start: time.Now()},
		{Days: 1, RequestsPerDay: 10, TraceEvery: 0, BatchSize: 10, Start: time.Now()},
	} {
		require.Error(t, opts.validate())
	}
}

func TestSeedLegacyHistorySupportsLargeRequestedBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))

	summary, err := seedLegacyHistory(db, legacyHistoryOptions{
		Days: 1, RequestsPerDay: 600, TraceEvery: 100, BatchSize: 1000,
		Start:           time.Date(2026, time.April, 26, 0, 0, 0, 0, time.UTC),
		MockUpstreamURL: "http://127.0.0.1:8342",
	})
	require.NoError(t, err)
	require.EqualValues(t, 600, summary.Requests)
}
