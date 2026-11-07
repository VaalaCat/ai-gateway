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
		"announcements", "announcement_groups", "announcement_seens",
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

func TestDeleteCleanupTableBatchPreservesPostPreviewRows(t *testing.T) {
	// 1. 预览跨两批的历史记录，再插入旧时间的新记录。
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := range 501 {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("previewed-%d", i), CreatedAt: cutoff.Unix() - 1}).Error)
	}
	request := CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff}
	preview, err := PreviewCleanupTable(t.Context(), core, log, request)
	require.NoError(t, err)
	require.Equal(t, int64(501), preview.ToDelete)
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "post-preview", CreatedAt: cutoff.Unix() - 1}).Error)
	request.SnapshotMaxKey = preview.SnapshotMaxKey

	// 2. 分批删除预览范围内的记录，保留预览后的新 ID。
	result, err := DeleteCleanupTableBatch(t.Context(), core, log, request)
	require.NoError(t, err)
	require.Equal(t, int64(500), result.Deleted)
	require.True(t, result.HasMore)

	var remaining []models.BillingLog
	require.NoError(t, core.Order("id ASC").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	require.Less(t, remaining[0].ID, remaining[1].ID)

	result, err = DeleteCleanupTableBatch(t.Context(), core, log, request)
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
	// 1. 在第二条记录删除时触发失败。
	core, log := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := range 2 {
		require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("rollback-%d", i), CreatedAt: cutoff.Unix() - 1}).Error)
	}
	preview, err := PreviewCleanupTable(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff})
	require.NoError(t, err)
	require.NoError(t, core.Exec(`CREATE TRIGGER cleanup_fail BEFORE DELETE ON billing_logs WHEN OLD.id = 2 BEGIN SELECT RAISE(FAIL, 'cleanup denied'); END`).Error)

	// 2. 核对失败结果和整个批次的回滚。
	result, err := DeleteCleanupTableBatch(t.Context(), core, log, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff, SnapshotMaxKey: preview.SnapshotMaxKey})
	require.Error(t, err)
	require.Zero(t, result)
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

func TestDeleteCleanupTableBatchRollsBackWhenContextCancels(t *testing.T) {
	for _, test := range []struct {
		name    string
		before  string
		deleted int64
	}{
		{name: "before_delete", before: "gorm:delete"},
		{name: "before_commit", before: "gorm:commit_or_rollback_transaction", deleted: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 1. 准备记录，在指定事务阶段取消请求。
			core := setupCleanupFileCoreDB(t)
			cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
			for i := range 2 {
				require.NoError(t, core.Create(&models.BillingLog{RequestID: fmt.Sprintf("cancel-%d", i), CreatedAt: cutoff.Unix() - 1}).Error)
			}
			preview, err := PreviewCleanupTable(t.Context(), core, nil, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff})
			require.NoError(t, err)
			canceled, cancel := context.WithCancel(t.Context())
			defer cancel()
			callbackName := "cleanup_cancel_" + test.name
			registered := false
			require.NoError(t, core.Callback().Delete().Before(test.before).Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "billing_logs" {
					registered = true
					require.Equal(t, test.deleted, tx.RowsAffected)
					cancel()
				}
			}))
			t.Cleanup(func() { _ = core.Callback().Delete().Remove(callbackName) })

			// 2. 核对取消错误和整个批次的回滚。
			result, err := DeleteCleanupTableBatch(canceled, core, nil, CleanupTableRequest{Database: "core", Table: "billing_logs", Cutoff: cutoff, SnapshotMaxKey: preview.SnapshotMaxKey})
			require.True(t, registered)
			require.ErrorIs(t, canceled.Err(), context.Canceled)
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, result)
			var count int64
			require.NoError(t, core.Model(&models.BillingLog{}).Count(&count).Error)
			require.Equal(t, int64(2), count)
		})
	}
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

func TestDeleteCleanupTableBatchCountsAndSingleDelete(t *testing.T) {
	// 1. 按默认批次大小准备各数量的历史 Trace。
	for _, test := range []struct {
		rows int
		want []CleanupBatchResult
	}{
		{0, []CleanupBatchResult{{}}},
		{499, []CleanupBatchResult{{Deleted: 499}}},
		{500, []CleanupBatchResult{{Deleted: 500, HasMore: true}, {}}},
		{501, []CleanupBatchResult{{Deleted: 500, HasMore: true}, {Deleted: 1}}},
		{1000, []CleanupBatchResult{{Deleted: 500, HasMore: true}, {Deleted: 500, HasMore: true}, {}}},
	} {
		t.Run(fmt.Sprint(test.rows), func(t *testing.T) {
			cleanupDB, _ := setupCleanupWALDBs(t, 2000)
			cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
			seedCleanupTraces(t, cleanupDB, test.rows, cutoff)
			request := CleanupTableRequest{Database: "log", Table: "request_traces", Cutoff: cutoff, SnapshotMaxKey: fmt.Sprint(test.rows)}
			recorder := &cleanupSQLRecorder{Interface: logger.Default.LogMode(logger.Silent)}
			cleanupDB = cleanupDB.Session(&gorm.Session{Logger: recorder})

			// 2. 逐批核对结果和实际执行的单条 DELETE。
			for _, want := range test.want {
				recorder.reset()
				result, err := DeleteCleanupTableBatch(t.Context(), nil, cleanupDB, request)
				require.NoError(t, err)
				require.Equal(t, want, result)
				statements := recorder.statementsSnapshot()
				require.Len(t, statements, 1)
				require.True(t, strings.HasPrefix(strings.ToUpper(statements[0]), "DELETE "), statements[0])
				require.Contains(t, strings.ToUpper(statements[0]), "SELECT ")
			}
			var count int64
			require.NoError(t, cleanupDB.Model(&models.RequestTrace{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestDeleteCleanupTableBatchAllTablesKeepCutoffAndSnapshotBoundaries(t *testing.T) {
	// 1. 在各表所属数据库中写入截止时间前、等于截止时间和晚于截止时间的记录。
	core, logDB := setupStrictSplitDBs(t)
	cutoff := time.Date(2026, 7, 20, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	for _, spec := range cleanupTableRegistry {
		t.Run(cleanupTableKey(spec.Database, spec.Name), func(t *testing.T) {
			db := logDB
			if spec.Database == "core" {
				db = core
			}
			var before, equal, after any = cutoff.Unix() - 1, cutoff.Unix(), cutoff.Unix() + 1
			if spec.TimeColumn == "date" {
				before = cutoff.UTC().AddDate(0, 0, -1).Format("2006-01-02")
				equal = cutoff.UTC().Format("2006-01-02")
				after = cutoff.UTC().AddDate(0, 0, 1).Format("2006-01-02")
			}
			for index, value := range []any{before, equal, after, before} {
				require.NoError(t, db.Table(spec.Name).Create(map[string]any{"id": index + 1, spec.TimeColumn: value}).Error)
			}
			request := CleanupTableRequest{Database: spec.Database, Table: spec.Name, Cutoff: cutoff}
			preview, err := PreviewCleanupTable(t.Context(), core, logDB, request)
			require.NoError(t, err)
			require.Equal(t, int64(4), preview.Total)
			require.Equal(t, int64(2), preview.ToDelete)
			require.Equal(t, "4", preview.SnapshotMaxKey)

			// 2. 预览后插入旧时间新 ID，清理只删除预览范围内的旧记录。
			require.NoError(t, db.Table(spec.Name).Create(map[string]any{"id": 5, spec.TimeColumn: before}).Error)
			request.SnapshotMaxKey = preview.SnapshotMaxKey
			result, err := DeleteCleanupTableBatch(t.Context(), core, logDB, request)
			require.NoError(t, err)
			require.Equal(t, CleanupBatchResult{Deleted: 2}, result)
			var ids []uint
			require.NoError(t, db.Table(spec.Name).Order("id ASC").Pluck("id", &ids).Error)
			require.Equal(t, []uint{2, 3, 5}, ids)
		})
	}
}

func TestDeleteCleanupTableBatchWALConcurrentWrites(t *testing.T) {
	// 1. 预览历史记录后，在独立连接中持续提交新 Trace。
	cleanupDB, writerDB := setupCleanupWALDBs(t, 2000)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	seedCleanupTraces(t, cleanupDB, 2000, cutoff)
	request := CleanupTableRequest{Database: "log", Table: "request_traces", Cutoff: cutoff}
	preview, err := PreviewCleanupTable(t.Context(), nil, cleanupDB, request)
	require.NoError(t, err)
	request.SnapshotMaxKey = preview.SnapshotMaxKey
	stop := make(chan struct{})
	committed := make(chan struct{}, 1)
	type writeResult struct {
		count int64
		err   error
	}
	finished := make(chan writeResult, 1)
	go func() {
		var result writeResult
		timer := time.NewTimer(10 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				finished <- result
				return
			case <-timer.C:
			}
			createdAt := cutoff.Unix() - 1
			if result.count%2 != 0 {
				createdAt = cutoff.Unix()
			}
			result.err = writerDB.Create(&models.RequestTrace{RequestID: fmt.Sprintf("new-%d", result.count), CreatedAt: createdAt}).Error
			if result.err != nil {
				finished <- result
				return
			}
			result.count++
			timer.Reset(10 * time.Millisecond)
			select {
			case committed <- struct{}{}:
			default:
			}
		}
	}()
	defer func() {
		close(stop)
		result := <-finished
		require.NoError(t, result.err)
		require.Positive(t, result.count)
		var remaining, oldTimeNewRows int64
		require.NoError(t, cleanupDB.Model(&models.RequestTrace{}).Count(&remaining).Error)
		require.Equal(t, result.count, remaining)
		require.NoError(t, cleanupDB.Model(&models.RequestTrace{}).Where("id > ? AND created_at < ?", request.SnapshotMaxKey, cutoff.Unix()).Count(&oldTimeNewRows).Error)
		require.Equal(t, (result.count+1)/2, oldTimeNewRows)
	}()

	// 2. 每批开始前确认后台已提交，核对累计删除数量与保留的新记录。
	var deleted int64
	for batch := 0; batch < 5; batch++ {
		select {
		case <-committed:
		case <-time.After(5 * time.Second):
			t.Fatal("background writer did not commit")
		}
		result, err := DeleteCleanupTableBatch(t.Context(), nil, cleanupDB, request)
		require.NoError(t, err)
		require.LessOrEqual(t, result.Deleted, int64(500))
		deleted += result.Deleted
		if !result.HasMore {
			require.Equal(t, 4, batch)
			break
		}
		require.Less(t, batch, 4)
	}
	require.Equal(t, preview.ToDelete, deleted)
}

func TestDeleteCleanupTableBatchWALWriteLock(t *testing.T) {
	for _, test := range []struct {
		name        string
		busyTimeout int
		timeout     bool
	}{
		{name: "wait_then_commit", busyTimeout: 2000},
		{name: "timeout_preserves_previous_batch", busyTimeout: 100, timeout: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 1. 提交第一批删除，再由独立连接取得写锁。
			cleanupDB, writerDB := setupCleanupWALDBs(t, test.busyTimeout)
			cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
			seedCleanupTraces(t, cleanupDB, 501, cutoff)
			request := CleanupTableRequest{Database: "log", Table: "request_traces", Cutoff: cutoff, SnapshotMaxKey: "501"}
			first, err := DeleteCleanupTableBatch(t.Context(), nil, cleanupDB, request)
			require.NoError(t, err)
			require.Equal(t, CleanupBatchResult{Deleted: 500, HasMore: true}, first)
			writerTx := writerDB.Begin()
			require.NoError(t, writerTx.Error)
			defer writerTx.Rollback()
			require.NoError(t, writerTx.Create(&models.RequestTrace{RequestID: "lock-holder", CreatedAt: cutoff.Unix() - 1}).Error)

			// 2. 同步到下一批 DELETE，观察持锁期间等待或超时。
			started := make(chan struct{})
			callbackName := "cleanup_delete_started"
			require.NoError(t, cleanupDB.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
				close(started)
			}))
			t.Cleanup(func() { _ = cleanupDB.Callback().Delete().Remove(callbackName) })
			type batchResult struct {
				result CleanupBatchResult
				err    error
			}
			finished := make(chan batchResult, 1)
			go func() {
				result, err := DeleteCleanupTableBatch(t.Context(), nil, cleanupDB, request)
				finished <- batchResult{result: result, err: err}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup DELETE did not start")
			}
			if !test.timeout {
				select {
				case result := <-finished:
					t.Fatalf("cleanup finished while writer held the lock: %+v", result)
				case <-time.After(50 * time.Millisecond):
				}
				require.NoError(t, writerTx.Commit().Error)
			}
			var result batchResult
			select {
			case result = <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup DELETE did not finish")
			}
			require.NoError(t, cleanupDB.Callback().Delete().Remove(callbackName))

			// 3. 超时保留失败批次，释放写锁后继续；成功批次保留已提交的删除。
			if test.timeout {
				require.Error(t, result.err)
				require.True(t, isSQLiteBusyError(result.err), "%v", result.err)
				require.Zero(t, result.result)
				var ids []uint
				require.NoError(t, cleanupDB.Model(&models.RequestTrace{}).Pluck("id", &ids).Error)
				require.Equal(t, []uint{501}, ids)
				require.NoError(t, writerTx.Commit().Error)
				result.result, result.err = DeleteCleanupTableBatch(t.Context(), nil, cleanupDB, request)
			}
			require.NoError(t, result.err)
			require.Equal(t, CleanupBatchResult{Deleted: 1}, result.result)
			var remaining []models.RequestTrace
			require.NoError(t, cleanupDB.Find(&remaining).Error)
			require.Len(t, remaining, 1)
			require.Equal(t, "lock-holder", remaining[0].RequestID)
		})
	}
}

// setupCleanupWALDBs 创建临时 WAL 数据库及两个独立的单连接池。
func setupCleanupWALDBs(t *testing.T, busyTimeout int) (*gorm.DB, *gorm.DB) {
	t.Helper()
	// 1. 为两个独立连接配置测试锁等待时间。
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)", filepath.Join(t.TempDir(), "cleanup-wal.db"), busyTimeout)
	connections := make([]*gorm.DB, 2)
	for index := range connections {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		var journalMode string
		require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error)
		require.Equal(t, "wal", journalMode)
		connections[index] = db
	}
	// 2. 创建实际 Trace 表。
	require.NoError(t, connections[0].AutoMigrate(&models.RequestTrace{}))
	return connections[0], connections[1]
}

// seedCleanupTraces 批量写入截止时间前的 Trace。
func seedCleanupTraces(t *testing.T, db *gorm.DB, count int, cutoff time.Time) {
	t.Helper()
	// 1. 按顺序写入历史 Trace。
	traces := make([]models.RequestTrace, count)
	for index := range traces {
		traces[index] = models.RequestTrace{RequestID: fmt.Sprintf("old-%d", index), CreatedAt: cutoff.Unix() - 1}
	}
	if len(traces) > 0 {
		require.NoError(t, db.CreateInBatches(&traces, 100).Error)
	}
}

func TestDeleteCleanupTableBatchWALWriteCommittedBeforeDelete(t *testing.T) {
	// 1. 预览旧 Trace，并在清理事务执行 DELETE 前由独立连接提交旧时间新 ID。
	cleanupDB, writerDB := setupCleanupWALDBs(t, 2000)
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	seedCleanupTraces(t, cleanupDB, 1, cutoff)
	request := CleanupTableRequest{Database: "log", Table: "request_traces", Cutoff: cutoff}
	preview, err := PreviewCleanupTable(t.Context(), nil, cleanupDB, request)
	require.NoError(t, err)
	request.SnapshotMaxKey = preview.SnapshotMaxKey
	callbackName := "cleanup_concurrent_commit"
	wrote := false
	require.NoError(t, cleanupDB.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "request_traces" && !wrote {
			wrote = true
			tx.AddError(writerDB.Create(&models.RequestTrace{RequestID: "committed-before-delete", CreatedAt: cutoff.Unix() - 1}).Error)
		}
	}))
	t.Cleanup(func() { _ = cleanupDB.Callback().Delete().Remove(callbackName) })

	// 2. 清理事务提交后，仅保留另一连接写入的记录。
	result, err := DeleteCleanupTableBatch(t.Context(), nil, cleanupDB, request)
	require.NoError(t, err)
	require.True(t, wrote)
	require.Equal(t, CleanupBatchResult{Deleted: 1}, result)
	var remaining []models.RequestTrace
	require.NoError(t, cleanupDB.Find(&remaining).Error)
	require.Len(t, remaining, 1)
	require.Equal(t, "committed-before-delete", remaining[0].RequestID)
}
