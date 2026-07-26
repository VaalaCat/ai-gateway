package historybackfill

import (
	"testing"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBackfillerRunPassCopiesMonolithHistoryThenReportsCaughtUp(t *testing.T) {
	source := openLegacyReaderDB(t)
	require.NoError(t, source.AutoMigrate(&models.UsageLog{}, &models.UsageLogTrace{}))
	require.NoError(t, source.Create(&models.UsageLog{ID: 41, RequestID: "req-41", Status: 1, CreatedAt: 100}).Error)
	require.NoError(t, source.Create(&models.UsageLogTrace{ID: 7, RequestID: "req-41", AttemptIndex: 0}).Error)
	core := openBackfillCoreTarget(t)
	logDB := openBackfillLogTarget(t)
	backfiller := New(Options{
		Reader: NewLegacyReader(source, masterdatabase.LegacyLayoutMonolith), CoreDBFinder: func() *gorm.DB { return core },
		LogDBFinder: func() *gorm.DB { return logDB }, BillingHistoryWriter: &BillingBatchWriter{DBFinder: func() *gorm.DB { return core }},
		RequestHistoryWriter: &masterlogqueue.HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return logDB }}, BatchSize: 100,
	})
	first, err := backfiller.RunPass(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 3, first.CopiedRows)
	require.False(t, first.CaughtUp)
	second, err := backfiller.RunPass(t.Context())
	require.NoError(t, err)
	require.Zero(t, second.CopiedRows)
	require.True(t, second.CaughtUp)
}

func TestBackfillerRunPassV5OnlyCopiesBilling(t *testing.T) {
	source := openLegacyReaderDB(t)
	require.NoError(t, source.AutoMigrate(&models.BillingLog{}))
	require.NoError(t, source.Create(&models.BillingLog{ID: 51, RequestID: "req-51"}).Error)
	core := openBackfillCoreTarget(t)
	backfiller := New(Options{Reader: NewLegacyReader(source, masterdatabase.LegacyLayoutV5Core), CoreDBFinder: func() *gorm.DB { return core }, BillingHistoryWriter: &BillingBatchWriter{DBFinder: func() *gorm.DB { return core }}})
	result, err := backfiller.RunPass(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 1, result.CopiedRows)
	require.False(t, result.CaughtUp)
}

func TestBackfillerRunPassKeepsBillingWhenLogUnavailable(t *testing.T) {
	source := openLegacyReaderDB(t)
	require.NoError(t, source.AutoMigrate(&models.UsageLog{}, &models.UsageLogTrace{}))
	require.NoError(t, source.Create(&models.UsageLog{ID: 41, RequestID: "req-41"}).Error)
	core := openBackfillCoreTarget(t)
	backfiller := New(Options{Reader: NewLegacyReader(source, masterdatabase.LegacyLayoutMonolith), CoreDBFinder: func() *gorm.DB { return core }, BillingHistoryWriter: &BillingBatchWriter{DBFinder: func() *gorm.DB { return core }}})
	_, err := backfiller.RunPass(t.Context())
	require.ErrorContains(t, err, "request history writer")
	var cursor models.HistoryCursor
	require.NoError(t, core.Where("key = ?", "billing").First(&cursor).Error)
	require.Equal(t, uint(41), cursor.LastSourceID)
}

func TestNewUsesLargerDefaultBatch(t *testing.T) {
	require.Equal(t, 5_000, New(Options{}).options.BatchSize)
}

func openBackfillCoreTarget(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	return db
}

func openBackfillLogTarget(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))
	return db
}
