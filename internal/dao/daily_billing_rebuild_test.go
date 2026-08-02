package dao_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	dailyDateA = "2026-05-01"
	dailyDateB = "2026-05-03"
)

type dailyRebuildApp struct {
	core *gorm.DB
	log  *gorm.DB
}

func (a *dailyRebuildApp) GetCoreDB() *gorm.DB { return a.core }
func (a *dailyRebuildApp) GetLogDB() *gorm.DB  { return a.log }
func (a *dailyRebuildApp) GetDatabaseLayoutMode() app.DatabaseLayoutMode {
	return app.DatabaseLayoutSplit
}

func TestDailyBillingBackfillFindsBoundsAndSkipsEmptyDates(t *testing.T) {
	_, logDB, application := openDailyRebuildDBs(t)
	seedRequestLogs(t, logDB,
		requestLog("bounds-a", dailyDateA, 1, 2, 3, 0, 11),
		requestLog("bounds-b", dailyDateB, 1, 2, 3, 0, 13),
	)
	rebuilder := dao.NewLogDailyBillingRebuilder(application)

	bounds, err := rebuilder.FindRequestLogDateBounds(t.Context())
	require.NoError(t, err)
	require.Equal(t, dao.RequestLogDateBounds{StartDate: dailyDateA, EndDate: dailyDateB}, bounds)

	next, ok, err := rebuilder.FindNextRequestLogDate(t.Context(), dailyDateA, dailyDateB)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, dailyDateB, next)

	_, ok, err = rebuilder.FindNextRequestLogDate(t.Context(), dailyDateB, dailyDateB)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestDailyBillingBackfillEmptyBounds(t *testing.T) {
	_, _, application := openDailyRebuildDBs(t)

	bounds, err := dao.NewLogDailyBillingRebuilder(application).FindRequestLogDateBounds(t.Context())

	require.NoError(t, err)
	require.Equal(t, dao.RequestLogDateBounds{Empty: true}, bounds)
}

func TestRebuildLogDailyGroupsTokenAndChannelFromRequestLogs(t *testing.T) {
	coreDB, logDB, application := openDailyRebuildDBs(t)
	rawInput, rawOutput := int64(17), int64(19)
	logs := []models.RequestLog{
		requestLog("group-a1", dailyDateA, 1, 2, 3, 0, 11),
		requestLog("group-a2", dailyDateA, 1, 2, 3, 0, 13),
		requestLog("group-private", dailyDateA, 1, 4, 0, 5, 7),
		requestLog("group-b", dailyDateB, 9, 9, 9, 0, 99),
	}
	logs[0].Status = 1
	logs[0].TokenName = "token-old"
	logs[0].ChannelName = "channel-old"
	logs[1].Status = 0
	logs[1].TokenName = "token-new"
	logs[1].ChannelName = "channel-new"
	logs[1].RawInputCost = &rawInput
	logs[1].RawOutputCost = &rawOutput
	logs[2].OwnerType = "private"
	logs[2].ChannelName = "private-channel"
	seedRequestLogs(t, logDB, logs...)
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
		Version: 1, State: models.DailyBillingBackfillRunning, StartDate: dailyDateA, EndDate: dailyDateB,
	}).Error)
	var requestLogQueries atomic.Int64
	countRequestLogQuery := func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "request_logs") {
			requestLogQueries.Add(1)
		}
	}
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:count_request_log_queries", countRequestLogQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:count_request_log_rows", countRequestLogQuery))

	result, err := dao.NewLogDailyBillingRebuilder(application).RebuildLogDailyDate(t.Context(), dailyDateA, 1, bothDailyTargets())

	require.NoError(t, err)
	require.Equal(t, int64(3), requestLogQueries.Load(), "daily rebuild must use a fixed aggregate, count, aggregate query plan")
	require.Equal(t, int64(3), result.ReplayedLogs)
	require.False(t, coreDB.Migrator().HasTable(&models.TokenDailyBilling{}), "core DB must not own token daily billing")
	require.False(t, coreDB.Migrator().HasTable(&models.ChannelDailyBilling{}), "core DB must not own channel daily billing")
	var tokens []models.TokenDailyBilling
	require.NoError(t, logDB.Order("token_id").Find(&tokens).Error)
	require.Len(t, tokens, 2)
	require.Equal(t, models.TokenDailyBilling{
		Date: dailyDateA, UserID: 1, TokenID: 2, TokenName: "token-new",
		RequestCount: 2, SuccessCount: 1, FailedCount: 1,
		PromptTokens: 24, CompletionTokens: 48, CacheReadTokens: 5, CacheWriteTokens: 7,
		InputCost: 240, OutputCost: 480, TotalCost: 720, LastUsedAt: unixAt(dailyDateA, 13),
	}, withoutTokenDailyStorageFields(tokens[0]))
	var channels []models.ChannelDailyBilling
	require.NoError(t, logDB.Order("private_channel_id, channel_id").Find(&channels).Error)
	require.Len(t, channels, 2)
	require.Equal(t, int64(330+36), channels[0].RawCost, "legacy raw falls back to total; explicit raw uses four raw buckets")
	require.Equal(t, int64(2), channels[0].RequestCount)
	require.Equal(t, "channel-new", channels[0].ChannelName)
	require.Equal(t, "admin", channels[0].OwnerType)
	require.Equal(t, uint(5), channels[1].PrivateChannelID)
	require.Equal(t, "private", channels[1].OwnerType)
	var marker models.DailyBillingBackfill
	require.NoError(t, logDB.First(&marker, "version = ?", 1).Error)
	require.Equal(t, dailyDateA, marker.LastCompletedDate)
}

func TestRebuildLogDailyRollsBackBothTablesAndCheckpoint(t *testing.T) {
	_, logDB, application := openDailyRebuildDBs(t)
	seedRequestLogs(t, logDB, requestLog("rollback", dailyDateA, 1, 2, 3, 0, 11))
	require.NoError(t, logDB.Create(&models.TokenDailyBilling{Date: dailyDateA, UserID: 7, TokenID: 8, RequestCount: 41}).Error)
	require.NoError(t, logDB.Create(&models.ChannelDailyBilling{Date: dailyDateA, ChannelID: 9, RequestCount: 43}).Error)
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
		Version: 1, State: models.DailyBillingBackfillRunning, StartDate: dailyDateA, EndDate: dailyDateB,
		LastCompletedDate: "",
	}).Error)
	require.NoError(t, logDB.Exec(`CREATE TRIGGER fail_rebuild_channel BEFORE INSERT ON channel_daily_billings BEGIN SELECT RAISE(ABORT, 'forced channel failure'); END`).Error)

	_, err := dao.NewLogDailyBillingRebuilder(application).RebuildLogDailyDate(t.Context(), dailyDateA, 1, bothDailyTargets())

	require.ErrorContains(t, err, "forced channel failure")
	var token models.TokenDailyBilling
	require.NoError(t, logDB.First(&token).Error)
	require.Equal(t, int64(41), token.RequestCount)
	var channel models.ChannelDailyBilling
	require.NoError(t, logDB.First(&channel).Error)
	require.Equal(t, int64(43), channel.RequestCount)
	var marker models.DailyBillingBackfill
	require.NoError(t, logDB.First(&marker, "version = ?", 1).Error)
	require.Empty(t, marker.LastCompletedDate)
}

func TestRebuildLogDailyCurrentDatePreservesRealtimeWriteAfterRebuild(t *testing.T) {
	_, logDB, application := openDailyRebuildFileDBs(t)
	seedRequestLogs(t, logDB, requestLog("current-existing", dailyDateA, 1, 2, 3, 0, 11))
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{Version: 1, State: models.DailyBillingBackfillRunning}).Error)

	deleteEntered := make(chan struct{})
	releaseDelete := make(chan struct{})
	require.NoError(t, logDB.Callback().Delete().Before("gorm:delete").Register("test:pause_daily_delete", func(tx *gorm.DB) {
		if tx.Statement.Table != "token_daily_billings" {
			return
		}
		close(deleteEntered)
		<-releaseDelete
	}))
	rebuildDone := make(chan error, 1)
	go func() {
		_, err := dao.NewLogDailyBillingRebuilder(application).RebuildLogDailyDate(context.Background(), dailyDateA, 1, bothDailyTargets())
		rebuildDone <- err
	}()
	<-deleteEntered

	realtimeDone := make(chan error, 1)
	go func() {
		realtime := requestLog("current-realtime", dailyDateA, 1, 2, 3, 0, 13)
		realtime.Status = 0
		realtime.TokenName = "token-realtime"
		realtime.ChannelName = "channel-realtime"
		realtime.OwnerType = "private"
		rawInput, rawOutput, rawRead, rawWrite := int64(17), int64(19), int64(23), int64(29)
		realtime.RawInputCost = &rawInput
		realtime.RawOutputCost = &rawOutput
		realtime.RawCacheReadCost = &rawRead
		realtime.RawCacheWriteCost = &rawWrite
		writer := masterlogqueue.LogBatchWriter{DBFinder: func() *gorm.DB { return logDB }}
		realtimeDone <- writer.Write(context.Background(), []masterlogqueue.LogBatch{
			masterlogqueue.BuildRequestAggregateBatch(models.UsageLog(realtime)),
		})
	}()
	close(releaseDelete)
	require.NoError(t, <-rebuildDone)
	require.NoError(t, <-realtimeDone)

	var token models.TokenDailyBilling
	require.NoError(t, logDB.First(&token, "date = ? AND user_id = ? AND token_id = ?", dailyDateA, 1, 2).Error)
	require.Equal(t, int64(2), token.RequestCount)
	require.Equal(t, int64(1), token.SuccessCount)
	require.Equal(t, int64(1), token.FailedCount)
	require.Equal(t, int64(24), token.PromptTokens)
	require.Equal(t, int64(48), token.CompletionTokens)
	require.Equal(t, int64(5), token.CacheReadTokens)
	require.Equal(t, int64(7), token.CacheWriteTokens)
	require.Equal(t, int64(240), token.InputCost)
	require.Equal(t, int64(480), token.OutputCost)
	require.Equal(t, int64(720), token.TotalCost)
	require.Equal(t, "token-realtime", token.TokenName)
	require.Equal(t, unixAt(dailyDateA, 13), token.LastUsedAt)
	var channel models.ChannelDailyBilling
	require.NoError(t, logDB.First(&channel, "date = ? AND channel_id = ?", dailyDateA, 3).Error)
	require.Equal(t, int64(2), channel.RequestCount)
	require.Equal(t, int64(1), channel.SuccessCount)
	require.Equal(t, int64(1), channel.FailedCount)
	require.Equal(t, int64(24), channel.PromptTokens)
	require.Equal(t, int64(48), channel.CompletionTokens)
	require.Equal(t, int64(5), channel.CacheReadTokens)
	require.Equal(t, int64(7), channel.CacheWriteTokens)
	require.Equal(t, int64(240), channel.InputCost)
	require.Equal(t, int64(480), channel.OutputCost)
	require.Equal(t, int64(720), channel.TotalCost)
	require.Equal(t, int64(330+17+19+23+29), channel.RawCost)
	require.Equal(t, "channel-realtime", channel.ChannelName)
	require.Equal(t, "private", channel.OwnerType)
	require.Equal(t, unixAt(dailyDateA, 13), channel.LastUsedAt)
}

func TestDailyBillingBackfillWrapsUnavailableLogDatabase(t *testing.T) {
	application := &dailyRebuildApp{}
	rebuilder := dao.NewLogDailyBillingRebuilder(application)

	_, err := rebuilder.FindRequestLogDateBounds(t.Context())
	require.ErrorIs(t, err, dao.ErrLogDatabaseUnavailable)
	_, _, err = rebuilder.FindNextRequestLogDate(t.Context(), dailyDateA, dailyDateB)
	require.ErrorIs(t, err, dao.ErrLogDatabaseUnavailable)
	_, err = rebuilder.RebuildLogDailyDate(t.Context(), dailyDateA, 1, bothDailyTargets())
	require.ErrorIs(t, err, dao.ErrLogDatabaseUnavailable)
}

func bothDailyTargets() dao.DailyBillingRebuildTargets {
	return dao.DailyBillingRebuildTargets{TokenDaily: true, ChannelDaily: true}
}

func openDailyRebuildDBs(t *testing.T) (*gorm.DB, *gorm.DB, *dailyRebuildApp) {
	t.Helper()
	core := openDailySQLite(t, ":memory:", models.MigrateCoreDB)
	logDB := openDailySQLite(t, ":memory:", models.MigrateLogDB)
	return core, logDB, &dailyRebuildApp{core: core, log: logDB}
}

func openDailyRebuildFileDBs(t *testing.T) (*gorm.DB, *gorm.DB, *dailyRebuildApp) {
	t.Helper()
	core := openDailySQLite(t, ":memory:", models.MigrateCoreDB)
	path := filepath.Join(t.TempDir(), "daily-rebuild.db")
	logDB := openDailySQLite(t, path+"?_pragma=busy_timeout(5000)", models.MigrateLogDB)
	return core, logDB, &dailyRebuildApp{core: core, log: logDB}
}

func openDailySQLite(t *testing.T, dsn string, migrate func(*gorm.DB) error) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if dsn == ":memory:" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxOpenConns(4)
	}
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, migrate(db))
	return db
}

func requestLog(id, date string, userID, tokenID, channelID, privateChannelID uint, prompt int) models.RequestLog {
	ts := unixAt(date, prompt)
	return models.RequestLog{
		RequestID: id, UserID: userID, TokenID: tokenID, TokenName: fmt.Sprintf("token-%d", tokenID),
		ChannelID: channelID, PrivateChannelID: privateChannelID, OwnerType: "admin",
		ChannelName: fmt.Sprintf("channel-%d", channelID), ChannelType: int(channelID),
		PromptTokens: prompt, CompletionTokens: prompt * 2, CacheReadTokens: prompt / 4, CacheWriteTokens: prompt / 3,
		InputCost: int64(prompt * 10), OutputCost: int64(prompt * 20), TotalCost: int64(prompt * 30),
		Status: 1, CreatedAt: ts,
	}
}

func unixAt(date string, second int) int64 {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return parsed.Add(time.Duration(second) * time.Second).Unix()
}

func seedRequestLogs(t *testing.T, db *gorm.DB, logs ...models.RequestLog) {
	t.Helper()
	require.NoError(t, db.Create(&logs).Error)
}

func countDailyRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(model).Count(&count).Error)
	return count
}

func withoutTokenDailyStorageFields(row models.TokenDailyBilling) models.TokenDailyBilling {
	row.ID = 0
	row.CreatedAt = 0
	row.UpdatedAt = 0
	return row
}
