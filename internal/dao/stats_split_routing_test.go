package dao

import (
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func strictSplitStats(t *testing.T) (*adminStatsQuery, *testApp, *gorm.DB, *gorm.DB) {
	t.Helper()
	core, logDB := setupStrictSplitDBs(t)
	provider := &testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}
	query := NewAdminQuery(NewContext(provider)).Stats().(*adminStatsQuery)
	return query, provider, core, logDB
}

func TestAgentMetricsSplitReadsMetadataFromCore(t *testing.T) {
	q, _, core, logDB := strictSplitStats(t)
	start := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	require.NoError(t, core.Create(&models.Agent{AgentID: "active", Name: "Core Agent", Status: 1, LastSeen: 1234}).Error)
	require.NoError(t, logDB.Create(&models.UsageHourlyBucket{Date: "2026-07-23", Hour: 1, AgentID: "active", ModelName: "m", RequestCount: 3}).Error)
	require.NoError(t, logDB.Create(&models.UsageHourlyBucket{Date: "2026-07-23", Hour: 2, AgentID: "deleted", ModelName: "m", RequestCount: 2}).Error)

	got, err := q.AgentMetrics(ObsRange{Start: start.Unix(), End: start.Add(24 * time.Hour).Unix(), Gran: GranHour})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, AgentMetric{ID: "active", Name: "Core Agent", Online: true, LastSeen: 1234, Requests: 3, Spark24h: got[0].Spark24h}, got[0])
	require.Equal(t, "deleted", got[1].ID)
	require.Empty(t, got[1].Name)
	require.False(t, got[1].Online)
	require.Zero(t, got[1].LastSeen)

	empty, err := q.AgentMetrics(ObsRange{Start: start.Add(48 * time.Hour).Unix(), End: start.Add(72 * time.Hour).Unix(), Gran: GranHour})
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestErrorDistributionUsesLayoutRequestTableAndCoreChannelMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		strict bool
	}{
		{name: "legacy"},
		{name: "split", strict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var core, logDB *gorm.DB
			layout := app.DatabaseLayoutLegacySingle
			if tc.strict {
				core, logDB = setupStrictSplitDBs(t)
				layout = app.DatabaseLayoutSplit
			} else {
				core = setupTestDB(t)
				logDB = core
			}
			require.NoError(t, core.Exec("INSERT INTO channels (id, name, type) VALUES (5, 'core-channel', 1)").Error)
			table := "usage_logs"
			if tc.strict {
				table = "request_logs"
			}
			ts := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).Unix()
			for _, row := range []models.UsageLog{
				{RequestID: tc.name + "-1", Status: 0, ErrorStage: "dispatch", ChannelID: 5, CreatedAt: ts},
				{RequestID: tc.name + "-2", Status: 0, ErrorStage: "dispatch", ChannelID: 99, CreatedAt: ts},
				{RequestID: tc.name + "-3", Status: 0, ErrorStage: "decode", ChannelID: 0, CreatedAt: ts},
			} {
				require.NoError(t, logDB.Table(table).Select("*").Create(&row).Error)
			}
			q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: layout})).Stats()
			r := ObsRange{Start: ts - 1, End: ts + 1, Gran: GranHour}
			stages, err := q.ErrorDistribution("stage", r, Scope{IsAdmin: true})
			require.NoError(t, err)
			require.Equal(t, []ErrBucket{{Stage: "dispatch", Count: 2, Ratio: 2.0 / 3.0}, {Stage: "decode", Count: 1, Ratio: 1.0 / 3.0}}, stages)
			channels, err := q.ErrorDistribution("channel", r, Scope{IsAdmin: true})
			require.NoError(t, err)
			require.Len(t, channels, 3)
			byID := map[uint]ErrBucket{}
			for _, bucket := range channels {
				byID[bucket.ID] = bucket
			}
			require.Equal(t, "core-channel", byID[5].Name)
			require.Empty(t, byID[99].Name)
			require.Empty(t, byID[0].Name)
			empty, err := q.ErrorDistribution("stage", ObsRange{Start: ts + 10, End: ts + 20}, Scope{IsAdmin: true})
			require.NoError(t, err)
			require.Empty(t, empty)
		})
	}
}

func TestStatsLogQueriesWrapOnlyConnectionFailuresAndRecover(t *testing.T) {
	q, provider, _, logDB := strictSplitStats(t)
	r := ObsRange{Start: 1, End: 2, Gran: GranHour}
	sqlDB, err := logDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, bucketErr := q.HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{})
	_, histogramErr := q.ChannelMetrics(r)
	_, requestErr := q.ErrorDistribution("stage", r, Scope{IsAdmin: true})
	for _, queryErr := range []error{bucketErr, histogramErr, requestErr} {
		require.Error(t, queryErr)
		require.ErrorIs(t, queryErr, ErrLogDatabaseUnavailable)
	}

	_, replacement := setupStrictSplitDBs(t)
	provider.logDB = replacement
	got, err := q.HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Empty(t, got)

	badLog := setupTestDB(t)
	require.NoError(t, badLog.Migrator().DropTable(&models.UsageHourlyBucket{}))
	provider.logDB = badLog
	_, err = q.HourlyTrend(r, Scope{IsAdmin: true}, ObsFilter{})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrLogDatabaseUnavailable))
}
