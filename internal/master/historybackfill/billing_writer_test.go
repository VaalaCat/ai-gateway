package historybackfill

import (
	"errors"
	"path/filepath"
	"testing"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func openBillingWriterCore(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := masterdatabase.NewConnector().OpenCore(filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	t.Cleanup(func() { closeDB(db) })
	return db
}

func TestBillingWriterCommitsFactsAndCursorWithoutCoreProjections(t *testing.T) {
	db := openBillingWriterCore(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}

	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 51, []models.BillingLog{{RequestID: "req-51", TotalCost: 11}}))

	var facts, cursors int64
	require.NoError(t, db.Model(&models.BillingLog{}).Count(&facts).Error)
	require.NoError(t, db.Model(&models.HistoryCursor{}).Count(&cursors).Error)
	require.Equal(t, int64(1), facts)
	require.Equal(t, int64(1), cursors)
	for _, table := range []string{"token_daily_billings", "channel_daily_billings", "billing_hourly_buckets", "billing_projection_receipts", "billing_projection_baselines"} {
		require.Falsef(t, db.Migrator().HasTable(table), "history writer must not recreate %s", table)
	}
}

func TestBillingWriterRollsBackFactWhenCursorUpdateFails(t *testing.T) {
	db := openBillingWriterCore(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	err := writer.writeHistoryWithHook(t.Context(), "billing", 51,
		[]models.BillingLog{{RequestID: "req-51", TotalCost: 11}},
		func() error { return errors.New("before cursor") })
	require.ErrorContains(t, err, "before cursor")

	var facts int64
	require.NoError(t, db.Model(&models.BillingLog{}).Count(&facts).Error)
	require.Zero(t, facts)
}
