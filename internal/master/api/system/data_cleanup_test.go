package system

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCleanupTablePreviewReturnsWireShape(t *testing.T) {
	h, c, core, _ := newCleanupHandlerFixture(t)
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "old", CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix()}).Error)
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "boundary", CreatedAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Unix()}).Error)

	response, err := h.CleanupTablePreview(c, CleanupTablePreviewRequest{
		Database: "core", Table: "billing_logs", CutoffDate: "2026-07-20",
	})
	require.NoError(t, err)
	require.Equal(t, "core", response.Database)
	require.Equal(t, "billing_logs", response.Table)
	require.Equal(t, "2026-07-20", response.CutoffDate)
	require.Equal(t, int64(2), response.Total)
	require.Equal(t, int64(1), response.ToDelete)
	require.NotEmpty(t, response.SnapshotMaxKey)
}

func TestCleanupTableBatchDeletesPreviewedRowsAndClearsStatsCache(t *testing.T) {
	h, c, core, _ := newCleanupHandlerFixture(t)
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "old", CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix()}).Error)
	preview, err := h.CleanupTablePreview(c, CleanupTablePreviewRequest{
		Database: "core", Table: "billing_logs", CutoffDate: "2026-07-20",
	})
	require.NoError(t, err)

	key := dao.QueryKey{Name: "cleanup-cache-success"}
	_, err = h.StatsCache.Get(t.Context(), key, func(context.Context) (any, error) { return int64(7), nil })
	require.NoError(t, err)
	response, err := h.CleanupTableBatch(c, CleanupTableBatchRequest{
		Database: "core", Table: "billing_logs", CutoffDate: "2026-07-20", SnapshotMaxKey: preview.SnapshotMaxKey,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), response.Deleted)
	require.False(t, response.HasMore)

	loads := 0
	value, err := h.StatsCache.Get(t.Context(), key, func(context.Context) (any, error) {
		loads++
		return int64(9), nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(9), value)
	require.Equal(t, 1, loads)
}

func TestCleanupTableBatchFailureDoesNotClearStatsCache(t *testing.T) {
	h, c, _, _ := newCleanupHandlerFixture(t)
	key := dao.QueryKey{Name: "cleanup-cache-failure"}
	_, err := h.StatsCache.Get(t.Context(), key, func(context.Context) (any, error) { return int64(7), nil })
	require.NoError(t, err)

	_, err = h.CleanupTableBatch(c, CleanupTableBatchRequest{
		Database: "core", Table: "billing_logs", CutoffDate: "2026-07-20", SnapshotMaxKey: "invalid",
	})
	requireCleanupAPIError(t, err, http.StatusBadRequest, "InvalidCleanupRequest")
	loads := 0
	value, err := h.StatsCache.Get(t.Context(), key, func(context.Context) (any, error) {
		loads++
		return int64(9), nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(7), value)
	require.Zero(t, loads)
}

func TestCleanupTablePreviewRejectsBusinessTable(t *testing.T) {
	h, c, _, _ := newCleanupHandlerFixture(t)
	_, err := h.CleanupTablePreview(c, CleanupTablePreviewRequest{
		Database: "core", Table: "users", CutoffDate: "2026-07-20",
	})
	requireCleanupAPIError(t, err, http.StatusBadRequest, "CleanupTableNotAllowed")
}

func TestCleanupTablePreviewRejectsFutureCutoff(t *testing.T) {
	h, c, _, _ := newCleanupHandlerFixture(t)
	_, err := h.CleanupTablePreview(c, CleanupTablePreviewRequest{
		Database: "core", Table: "billing_logs", CutoffDate: time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"),
	})
	requireCleanupAPIError(t, err, http.StatusBadRequest, "InvalidCleanupCutoff")
}

func TestCleanupTablePreviewMapsUnavailableLogDatabase(t *testing.T) {
	h, c, _, logDB := newCleanupHandlerFixture(t)
	c.App.SetLogDB(nil)
	require.NoError(t, closeCleanupDB(logDB))

	_, err := h.CleanupTablePreview(c, CleanupTablePreviewRequest{
		Database: "log", Table: "request_logs", CutoffDate: "2026-07-20",
	})
	requireCleanupAPIError(t, err, http.StatusServiceUnavailable, "LogDatabaseUnavailable")
}

func TestMapCleanupErrorMapsSQLiteBusy(t *testing.T) {
	for _, message := range []string{"database is locked", "database table is locked", "SQLITE_BUSY"} {
		t.Run(message, func(t *testing.T) {
			err := mapCleanupError("core", errors.New(message))
			requireCleanupAPIError(t, err, http.StatusServiceUnavailable, "CleanupDatabaseBusy")
		})
	}
}

func newCleanupHandlerFixture(t *testing.T) (*Handler, *app.Context, *gorm.DB, *gorm.DB) {
	t.Helper()
	core := openCleanupTestDB(t, models.MigrateCoreDB)
	logDB := openCleanupTestDB(t, models.MigrateLogDB)
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	return &Handler{StatsCache: dao.NewStatsCache()}, &app.Context{
		Context: ginContext, App: application, OwnerContext: t.Context(),
	}, core, logDB
}

func openCleanupTestDB(t *testing.T, migrate func(*gorm.DB) error) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrate(db))
	return db
}

func closeCleanupDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func requireCleanupAPIError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, status, apiErr.Status)
	require.Equal(t, code, apiErr.Code)
	require.False(t, errors.Is(err, context.Canceled))
}
