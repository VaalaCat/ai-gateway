package stats

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	dbtestdata "github.com/VaalaCat/ai-gateway/internal/master/database/testdata"
	"github.com/VaalaCat/ai-gateway/internal/master/historybackfill"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const productionQueryBudget = 200 * time.Millisecond

func TestSplitFixtureRoutesStatsQueriesToHourlyIndexes(t *testing.T) {
	dir := t.TempDir()
	legacyPath, corePath, logPath := filepath.Join(dir, "master.db"), filepath.Join(dir, "core.db"), filepath.Join(dir, "log.db")
	fixture := dbtestdata.BuildLegacyProductionFixture(t, legacyPath, dbtestdata.ProductionFixtureOptions{
		Days: 1, Users: 4, TokensPerUser: 1, Models: 2, Channels: 2,
		RequestsPerHour: 4, WALUncheckpointedRows: 3,
	})
	require.NoError(t, fixture.Close())
	prepareOnlineBackfilledSplit(t, legacyPath, corePath, logPath, fixture.TableCounts["usage_logs"], fixture.Traces.Rows)

	trace := newSQLTraceLogger()
	core, err := masterdatabase.NewConnector().OpenExistingCorePath(corePath)
	require.NoError(t, err)
	core = core.Session(&gorm.Session{Logger: trace})
	defer closePerformanceDB(t, core)
	logs, err := masterdatabase.NewConnector().OpenExistingLogPath(logPath)
	require.NoError(t, err)
	logs = logs.Session(&gorm.Session{Logger: trace})
	defer closePerformanceDB(t, logs)
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logs)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	query := dao.NewAdminQuery(dao.NewContext(application)).Stats()
	range1d := dao.ObsRange{Start: fixture.MinCreatedAt, End: fixture.MaxCreatedAt + 1, Gran: dao.GranHour}

	statements := captureStatsQuery(t, trace, func() error {
		_, err := query.DashboardKpis(range1d, dao.Scope{IsAdmin: true}, dao.ObsFilter{})
		return err
	})
	assertCapturedHourlyQueryPlan(t, core, "split dashboard", "billing_hourly_buckets", "select distinct", "idx_bhb_window_user", statements)
	statements = captureStatsQuery(t, trace, func() error {
		_, err := query.MetricTrendGrouped("cost", "sum", "model", range1d, dao.Scope{IsAdmin: true}, 2, dao.ObsFilter{})
		return err
	})
	assertCapturedHourlyQueryPlan(t, logs, "split grouped trend", "usage_hourly_buckets", "display_name", "idx_uhb_bucket", statements)
}

func TestProductionFixtureSevenDayQueriesUnder200ms(t *testing.T) {
	if raceEnabled {
		t.Skip("production-scale SQLite performance fixture is verified without -race")
	}
	if os.Getenv("AI_GATEWAY_RUN_STATS_PERFORMANCE") != "1" {
		t.Skip("set AI_GATEWAY_RUN_STATS_PERFORMANCE=1 to run the production-scale fixture")
	}

	dir := t.TempDir()
	legacyPath, corePath, logPath := filepath.Join(dir, "master.db"), filepath.Join(dir, "core.db"), filepath.Join(dir, "log.db")
	fixture := dbtestdata.BuildLegacyProductionFixture(t, legacyPath, dbtestdata.ProductionFixtureOptions{
		Days: 7, Users: 64, TokensPerUser: 2, Models: 24, Channels: 8,
		RequestsPerHour: 128, WALUncheckpointedRows: 257,
	})
	require.NoError(t, fixture.Close())
	prepareOnlineBackfilledSplit(t, legacyPath, corePath, logPath, fixture.TableCounts["usage_logs"], fixture.Traces.Rows)

	trace := newSQLTraceLogger()
	core, err := masterdatabase.NewConnector().OpenExistingCorePath(corePath)
	require.NoError(t, err)
	core = core.Session(&gorm.Session{Logger: trace})
	defer closePerformanceDB(t, core)
	logs, err := masterdatabase.NewConnector().OpenExistingLogPath(logPath)
	require.NoError(t, err)
	logs = logs.Session(&gorm.Session{Logger: trace})
	defer closePerformanceDB(t, logs)
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logs)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	query := dao.NewAdminQuery(dao.NewContext(application)).Stats()
	range7d := dao.ObsRange{Start: fixture.MinCreatedAt, End: fixture.MinCreatedAt + 7*86400, Gran: dao.GranHour}
	model := "fixture-model-01-with-a-deliberately-long-name"

	statements := measureProductionQuery(t, trace, "active users", func() error {
		_, err := query.DashboardKpis(range7d, dao.Scope{IsAdmin: true}, dao.ObsFilter{})
		return err
	})
	assertCapturedHourlyQueryPlan(t, core, "active users", "billing_hourly_buckets", "select distinct", "idx_bhb_window_user", statements)

	statements = measureProductionQuery(t, trace, "billing trend", func() error {
		_, err := query.CostTrendStackedByModel(range7d, dao.Scope{IsAdmin: true}, 20, dao.ObsFilter{})
		return err
	})
	assertCapturedHourlyQueryPlan(t, core, "billing trend", "billing_hourly_buckets", "model_name as name", "idx_bhb_window_user", statements)

	statements = measureProductionQuery(t, trace, "model-filtered user leaderboard", func() error {
		_, err := query.Leaderboard("user", "tokens", 20, range7d, dao.Scope{IsAdmin: true}, dao.ObsFilter{ModelName: model})
		return err
	})
	assertCapturedHourlyQueryPlan(t, core, "model-filtered user leaderboard", "billing_hourly_buckets", "left join users", "idx_bhb_model_user", statements)

	statements = measureProductionQuery(t, trace, "top20 grouped trend", func() error {
		_, err := query.MetricTrendGrouped("cost", "sum", "model", range7d, dao.Scope{IsAdmin: true}, 20, dao.ObsFilter{})
		return err
	})
	assertCapturedHourlyQueryPlan(t, logs, "top20 grouped trend", "usage_hourly_buckets", "display_name", "idx_uhb_bucket", statements)
}

func prepareOnlineBackfilledSplit(t *testing.T, legacyPath, corePath, logPath string, expectedRequests, expectedTraces int64) {
	t.Helper()
	connector := masterdatabase.NewConnector()
	legacy, err := connector.OpenExistingLogPath(legacyPath)
	require.NoError(t, err)
	legacy = legacy.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	defer closePerformanceDB(t, legacy)
	core, err := connector.OpenCorePath(corePath)
	require.NoError(t, err)
	core = core.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	defer closePerformanceDB(t, core)
	require.NoError(t, models.MigrateCoreDB(core))
	logs, err := connector.OpenLogPath(logPath)
	require.NoError(t, err)
	logs = logs.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	defer closePerformanceDB(t, logs)
	require.NoError(t, models.MigrateLogDB(logs))

	layout := masterdatabase.LegacyLayoutInfo{Kind: masterdatabase.LegacyLayoutMonolith, Path: legacyPath}
	_, err = masterdatabase.BootstrapCore(t.Context(), legacy, core, layout, masterdatabase.BootstrapOptions{})
	require.NoError(t, err)
	backfiller := historybackfill.New(historybackfill.Options{
		Reader:       historybackfill.NewLegacyReader(legacy, layout.Kind),
		CoreDBFinder: func() *gorm.DB { return core },
		LogDBFinder:  func() *gorm.DB { return logs },
		BillingHistoryWriter: &historybackfill.BillingBatchWriter{
			DBFinder: func() *gorm.DB { return core },
		},
		RequestHistoryWriter: &masterlogqueue.HistoricalRequestBatchWriter{
			DBFinder: func() *gorm.DB { return logs },
		},
		BatchSize: 5000,
	})
	for {
		result, runErr := backfiller.RunPass(t.Context())
		require.NoError(t, runErr)
		if result.CaughtUp {
			break
		}
	}
	var requestCount, traceCount int64
	require.NoError(t, logs.Model(&models.RequestLog{}).Count(&requestCount).Error)
	require.NoError(t, logs.Model(&models.RequestTrace{}).Count(&traceCount).Error)
	require.Equal(t, expectedRequests, requestCount)
	require.Equal(t, expectedTraces, traceCount)
}

func measureProductionQuery(t *testing.T, trace *sqlTraceLogger, name string, run func() error) []string {
	t.Helper()
	trace.Reset()
	started := time.Now()
	require.NoError(t, run())
	elapsed := time.Since(started)
	t.Logf("%s invocation duration: %s", name, elapsed)
	require.Less(t, elapsed, productionQueryBudget, "%s exceeded the fixed production query budget", name)
	statements := trace.Statements()
	require.NotEmpty(t, statements, "%s must execute a real DAO query", name)
	return statements
}

func captureStatsQuery(t *testing.T, trace *sqlTraceLogger, run func() error) []string {
	t.Helper()
	trace.Reset()
	require.NoError(t, run())
	statements := trace.Statements()
	require.NotEmpty(t, statements)
	return statements
}

func assertCapturedHourlyQueryPlan(t *testing.T, db *gorm.DB, name, table, fragment, wantIndex string, statements []string) {
	t.Helper()
	var actualSQL string
	for _, statement := range statements {
		normalized := strings.ToLower(statement)
		if strings.Contains(normalized, table) && strings.Contains(normalized, fragment) {
			require.Empty(t, actualSQL, "%s matched more than one captured SQL statement", name)
			actualSQL = statement
		}
	}
	require.NotEmpty(t, actualSQL, "%s must capture its real %s DAO query", name, table)
	var rows []struct {
		Detail string `gorm:"column:detail"`
	}
	require.NoError(t, db.Raw("EXPLAIN QUERY PLAN "+actualSQL).Scan(&rows).Error)
	details := make([]string, 0, len(rows))
	for _, row := range rows {
		details = append(details, row.Detail)
	}
	plan := strings.Join(details, " | ")
	t.Logf("%s query plan: %s", name, plan)
	require.Contains(t, strings.ToLower(plan), strings.ToLower(wantIndex), fmt.Sprintf("%s must use its hourly index", name))
	require.NotContains(t, strings.ToLower(plan), "scan billing_logs")
	require.NotContains(t, strings.ToLower(plan), "scan request_logs")
}

type sqlTraceLogger struct {
	logger.Interface
	mu         sync.Mutex
	statements []string
}

func newSQLTraceLogger() *sqlTraceLogger {
	return &sqlTraceLogger{Interface: logger.Default.LogMode(logger.Silent)}
}

func (l *sqlTraceLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	if sql == "" {
		return
	}
	l.mu.Lock()
	l.statements = append(l.statements, sql)
	l.mu.Unlock()
}

func (l *sqlTraceLogger) Reset() {
	l.mu.Lock()
	l.statements = nil
	l.mu.Unlock()
}

func (l *sqlTraceLogger) Statements() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.statements...)
}

func closePerformanceDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}
