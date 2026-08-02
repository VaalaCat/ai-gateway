package dao

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCleanupTableRegistryIsExactAllowList(t *testing.T) {
	want := []string{
		"core:billing_logs", "log:channel_daily_billings", "log:token_daily_billings", "log:request_logs",
		"log:request_traces", "log:usage_duration_histograms", "log:usage_hourly_buckets",
		"log:usage_tps_histograms", "log:usage_ttft_histograms", "log:usage_user_tps_histograms",
		"log:usage_user_ttft_histograms",
	}
	sort.Strings(want)
	require.Equal(t, want, CleanupTableKeys())

	for _, key := range want {
		database, table, _ := splitCleanupKey(key)
		spec, ok := FindCleanupTable(database, table)
		require.True(t, ok, key)
		require.Equal(t, database, spec.Database)
		require.Equal(t, table, spec.Name)
	}

	for _, forbidden := range []string{
		"users", "tokens", "channels", "model_configs", "agents", "settings", "enrollment_tokens", "agent_routes",
		"request_limiters", "limiter_bindings", "token_templates", "user_groups", "oauth_providers", "oauth_identities",
		"model_routings", "private_channels", "private_channel_shares", "admin_scripts", "invite_codes",
		"invite_redemptions", "master_signing_keys", "billing_projection_baselines", "history_migrations",
		"history_cursors", "log_history_aggregate_merges", "database_layouts",
	} {
		_, ok := FindCleanupTable("core", forbidden)
		require.False(t, ok, forbidden)
	}
	for _, invalid := range [][2]string{
		{"Core", "billing_logs"}, {"core", "BillingLogs"}, {"log", "billing_logs"},
		{"core", "token_daily_billings"}, {"core", "channel_daily_billings"},
		{"core", "billing_hourly_buckets"}, {"core", "billing_projection_receipts"},
		{"other", "request_logs"},
	} {
		_, ok := FindCleanupTable(invalid[0], invalid[1])
		require.False(t, ok, invalid)
	}
}

func TestDeleteBillingLogsBeforeBatchUsesStrictKeysetWithoutCount(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	for index, createdAt := range []int64{cutoff.Unix() - 2, cutoff.Unix() - 1, cutoff.Unix()} {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("retention-%d", index), CreatedAt: createdAt}).Error)
	}
	recorder := &cleanupSQLRecorder{Interface: logger.Default.LogMode(logger.Info)}
	recordingCore := core.Session(&gorm.Session{Logger: recorder})

	first, err := DeleteBillingLogsBeforeBatch(t.Context(), recordingCore, cutoff, BillingLogDeleteCursor{}, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Deleted)
	require.True(t, first.HasMore)
	require.NotZero(t, first.Next.ID)

	second, err := DeleteBillingLogsBeforeBatch(t.Context(), recordingCore, cutoff, first.Next, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), second.Deleted)
	require.False(t, second.HasMore)
	require.NoError(t, core.First(&models.BillingLog{}, "request_id = ?", "retention-2").Error)
	for _, statement := range recorder.statementsSnapshot() {
		require.NotContains(t, strings.ToUpper(statement), "COUNT(")
	}
}

func TestPreviewCleanupTableUsesStrictCutoffsForAllTimeColumns(t *testing.T) {
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	require.NoError(t, log.Create(&models.TokenDailyBilling{Date: "2026-07-19"}).Error)
	require.NoError(t, log.Create(&models.TokenDailyBilling{Date: "2026-07-20"}).Error)
	datePreview, err := PreviewCleanupTable(t.Context(), core, log, CleanupTableRequest{Database: "log", Table: "token_daily_billings", Cutoff: cutoff})
	require.NoError(t, err)
	require.Equal(t, int64(2), datePreview.Total)
	require.Equal(t, int64(1), datePreview.ToDelete)
	require.NotEmpty(t, datePreview.SnapshotMaxKey)

	require.NoError(t, log.Create(&models.RequestLog{RequestID: "before-cutoff", CreatedAt: cutoff.Unix() - 1}).Error)
	require.NoError(t, log.Create(&models.RequestLog{RequestID: "at-cutoff", CreatedAt: cutoff.Unix()}).Error)
	createdPreview, err := PreviewCleanupTable(t.Context(), core, log, CleanupTableRequest{Database: "log", Table: "request_logs", Cutoff: cutoff})
	require.NoError(t, err)
	require.Equal(t, int64(2), createdPreview.Total)
	require.Equal(t, int64(1), createdPreview.ToDelete)
}

func TestCleanupTableRejectsUnknownDatabaseNilLogAndInvalidSnapshot(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	request := CleanupTableRequest{Database: "core", Table: "users", Cutoff: time.Now()}
	_, err := PreviewCleanupTable(t.Context(), core, nil, request)
	require.ErrorIs(t, err, ErrCleanupTableNotAllowed)

	_, err = PreviewCleanupTable(t.Context(), core, nil, CleanupTableRequest{Database: "log", Table: "request_logs", Cutoff: time.Now()})
	require.ErrorIs(t, err, ErrLogDatabaseUnavailable)

	_, err = DeleteCleanupTableBatch(t.Context(), core, nil, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: time.Now(), SnapshotMaxKey: "not-a-uint"})
	require.ErrorIs(t, err, ErrInvalidCleanupSnapshot)
}

func TestDeleteCleanupTableBatchUsesInjectedLimitAndPreservesPostPreviewRows(t *testing.T) {
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("previewed-%d", i), CreatedAt: cutoff.Unix() - 1}).Error)
	}
	request := CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff}
	preview, err := PreviewCleanupTable(t.Context(), core, log, request)
	require.NoError(t, err)
	require.Equal(t, int64(3), preview.ToDelete)
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "post-preview", CreatedAt: cutoff.Unix() - 1}).Error)
	request.SnapshotMaxKey = preview.SnapshotMaxKey

	result, err := deleteCleanupTableBatch(t.Context(), core, log, request, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Deleted)
	require.True(t, result.HasMore)

	var remaining []models.BillingLog
	require.NoError(t, core.Order("id ASC").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	require.Less(t, remaining[0].ID, remaining[1].ID)

	result, err = deleteCleanupTableBatch(t.Context(), core, log, request, 2)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Deleted)
	require.False(t, result.HasMore)

	var total int64
	require.NoError(t, core.Model(&models.BillingLog{}).Count(&total).Error)
	require.Equal(t, int64(1), total, "the old-time row added after preview must remain")
}

func TestCleanupTableBillingLogsUsesPlainPolicy(t *testing.T) {
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	fact := models.BillingLog{RequestID: "plain-policy", CreatedAt: cutoff.Unix() - 1}
	require.NoError(t, core.Create(&fact).Error)

	factPreview, err := PreviewCleanupTable(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff})
	require.NoError(t, err)
	require.Equal(t, int64(1), factPreview.ToDelete)
	result, err := DeleteCleanupTableBatch(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff, SnapshotMaxKey: factPreview.SnapshotMaxKey})
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Deleted)
	require.ErrorIs(t, core.First(&models.BillingLog{}, fact.ID).Error, gorm.ErrRecordNotFound)
}

func TestPreviewCleanupTableReadsEligibleCountAndWatermarkInOneAggregateQuery(t *testing.T) {
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for index, createdAt := range []int64{cutoff.Unix() - 2, cutoff.Unix() - 1, cutoff.Unix()} {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("aggregate-%d", index), CreatedAt: createdAt}).Error)
	}
	recorder := &cleanupSQLRecorder{Interface: logger.Default.LogMode(logger.Info)}
	recordingCore := core.Session(&gorm.Session{Logger: recorder})
	preview, err := PreviewCleanupTable(t.Context(), recordingCore, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff})
	require.NoError(t, err)
	require.Equal(t, int64(2), preview.ToDelete)
	require.Equal(t, "2", preview.SnapshotMaxKey)
	require.Len(t, recorder.eligibleAggregateQueries(), 1)

	recorder.reset()
	empty, err := PreviewCleanupTable(t.Context(), recordingCore, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: time.Unix(cutoff.Unix()-2, 0).UTC()})
	require.NoError(t, err)
	require.Zero(t, empty.ToDelete)
	require.Empty(t, empty.SnapshotMaxKey)
	require.Len(t, recorder.eligibleAggregateQueries(), 1)
}

func TestDeleteCleanupTableBatchRollsBackOnDeleteFailure(t *testing.T) {
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := range 2 {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("rollback-%d", i), CreatedAt: cutoff.Unix() - 1}).Error)
	}
	preview, err := PreviewCleanupTable(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff})
	require.NoError(t, err)
	require.NoError(t, core.Exec(`CREATE TRIGGER cleanup_fail BEFORE DELETE ON billing_logs BEGIN SELECT RAISE(FAIL, 'cleanup denied'); END`).Error)

	_, err = DeleteCleanupTableBatch(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff, SnapshotMaxKey: preview.SnapshotMaxKey})
	require.Error(t, err)
	var count int64
	require.NoError(t, core.Model(&models.BillingLog{}).Count(&count).Error)
	require.Equal(t, int64(2), count)
}

func TestDeleteCleanupTableBatchReturnsRealRowsAfterConcurrentDeletion(t *testing.T) {
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := range 2 {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("concurrent-%d", i), CreatedAt: cutoff.Unix() - 1}).Error)
	}
	preview, err := PreviewCleanupTable(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff})
	require.NoError(t, err)
	require.NoError(t, core.Where("id = ?", 1).Delete(&models.BillingLog{}).Error)
	result, err := DeleteCleanupTableBatch(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff, SnapshotMaxKey: preview.SnapshotMaxKey})
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Deleted)
	require.False(t, result.HasMore)

}

func TestDeleteCleanupTableBatchRollsBackWhenContextCancelsAfterKeySelection(t *testing.T) {
	core := setupCleanupFileCoreDB(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := range 2 {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("cancel-%d", i), CreatedAt: cutoff.Unix() - 1}).Error)
	}
	preview, err := PreviewCleanupTable(t.Context(), core, nil, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff})
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(t.Context())
	callbackName := "cleanup_cancel_after_select"
	registered := false
	require.NoError(t, core.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_logs" {
			registered = true
			cancel()
		}
	}))
	t.Cleanup(func() { _ = core.Callback().Delete().Remove(callbackName) })
	_, err = DeleteCleanupTableBatch(canceled, core, nil, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff, SnapshotMaxKey: preview.SnapshotMaxKey})
	require.True(t, registered)
	require.ErrorIs(t, err, context.Canceled)
	var count int64
	require.NoError(t, core.Model(&models.BillingLog{}).Count(&count).Error)
	require.Equal(t, int64(2), count)
}

func setupCleanupFileCoreDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cleanup.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.MigrateCoreDB(db))
	return db
}

func TestDeleteCleanupTableBatchEmptyResult(t *testing.T) {
	core, log := setupStrictSplitDBs(t)
	result, err := DeleteCleanupTableBatch(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: time.Now(), SnapshotMaxKey: "1"})
	require.NoError(t, err)
	require.Equal(t, CleanupBatchResult{}, result)
}

func splitCleanupKey(key string) (string, string, error) {
	for i := range key {
		if key[i] == ':' {
			return key[:i], key[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid cleanup key %q", key)
}

type cleanupSQLRecorder struct {
	logger.Interface
	mu         sync.Mutex
	statements []string
}

func (r *cleanupSQLRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	statement, _ := fc()
	r.mu.Lock()
	r.statements = append(r.statements, statement)
	r.mu.Unlock()
}

func (r *cleanupSQLRecorder) reset() {
	r.mu.Lock()
	r.statements = nil
	r.mu.Unlock()
}

func (r *cleanupSQLRecorder) statementsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.statements...)
}

func (r *cleanupSQLRecorder) eligibleAggregateQueries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var queries []string
	for _, statement := range r.statements {
		if strings.Contains(statement, "billing_logs") && strings.Contains(statement, "COUNT(*)") && strings.Contains(statement, "MAX(id)") {
			queries = append(queries, statement)
		}
	}
	return queries
}
