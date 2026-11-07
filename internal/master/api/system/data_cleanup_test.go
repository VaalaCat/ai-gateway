package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

// TestCleanupTableBatchHTTPBusy 验证持锁失败、已提交批次和释放锁后的继续清理。
func TestCleanupTableBatchHTTPBusy(t *testing.T) {
	for _, target := range []struct {
		database string
		table    string
	}{
		{database: "core", table: "billing_logs"},
		{database: "log", table: "request_traces"},
	} {
		t.Run(target.database, func(t *testing.T) {
			// 1. 在文件数据库中写入两批旧记录，挂载生产请求适配器。
			path := filepath.Join(t.TempDir(), "cleanup.db")
			db := openCleanupHTTPFileDB(t, path, 50)
			if target.database == "core" {
				require.NoError(t, db.AutoMigrate(&models.BillingLog{}))
			} else {
				require.NoError(t, db.AutoMigrate(&models.RequestTrace{}))
			}
			rows := make([]map[string]any, 1000)
			for index := range rows {
				rows[index] = map[string]any{"id": index + 1, "request_id": fmt.Sprintf("old-%d", index), "created_at": int64(1)}
			}
			require.NoError(t, db.Table(target.table).CreateInBatches(rows, 100).Error)
			application := app.NewApplication()
			application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
			if target.database == "core" {
				application.SetCoreDB(db)
			} else {
				application.SetLogDB(db)
			}
			h := &Handler{StatsCache: dao.NewStatsCache()}
			server := newCleanupHTTPServer(t, application, h)
			request := CleanupTableBatchRequest{Database: target.database, Table: target.table, CutoffDate: "2026-07-20", SnapshotMaxKey: "1000"}
			status, body := cleanupHTTPRequest(t, server, http.MethodPost, "/api/system/cleanup/batch", request)
			require.Equal(t, http.StatusOK, status, string(body))
			require.JSONEq(t, `{"deleted":500,"has_more":true}`, string(body))

			// 2. 独立连接取得写锁，第二批返回 busy 并保留缓存和未删除记录。
			writerDB := openCleanupHTTPFileDB(t, path, 50)
			transaction := writerDB.Begin()
			require.NoError(t, transaction.Error)
			t.Cleanup(func() { transaction.Rollback() })
			require.NoError(t, transaction.Table(target.table).Where("id = ?", 501).Update("created_at", int64(2)).Error)
			key := dao.QueryKey{Name: "cleanup-http-busy"}
			_, err := h.StatsCache.Get(t.Context(), key, func(context.Context) (any, error) { return int64(7), nil })
			require.NoError(t, err)
			status, body = cleanupHTTPRequest(t, server, http.MethodPost, "/api/system/cleanup/batch", request)
			require.Equal(t, http.StatusServiceUnavailable, status, string(body))
			require.JSONEq(t, `{"code":"CleanupDatabaseBusy","message":"cleanup database is busy"}`, string(body))
			loads := 0
			value, err := h.StatsCache.Get(t.Context(), key, func(context.Context) (any, error) {
				loads++
				return int64(9), nil
			})
			require.NoError(t, err)
			require.Equal(t, int64(7), value)
			require.Zero(t, loads)
			var remaining int64
			require.NoError(t, db.Table(target.table).Count(&remaining).Error)
			require.Equal(t, int64(500), remaining)
			var committedRows int64
			require.NoError(t, db.Table(target.table).Where("id <= ?", 500).Count(&committedRows).Error)
			require.Zero(t, committedRows)

			// 3. 释放写锁后提交第二批和空批次，累计删除一千条。
			require.NoError(t, transaction.Rollback().Error)
			for _, expected := range []string{`{"deleted":500,"has_more":true}`, `{"deleted":0,"has_more":false}`} {
				status, body = cleanupHTTPRequest(t, server, http.MethodPost, "/api/system/cleanup/batch", request)
				require.Equal(t, http.StatusOK, status, string(body))
				require.JSONEq(t, expected, string(body))
			}
			require.NoError(t, db.Table(target.table).Count(&remaining).Error)
			require.Zero(t, remaining)
		})
	}
}

// TestCleanupTableBatchHTTPWithConcurrentTraceWrites 验证持续写入时的预览范围和完整批次响应。
func TestCleanupTableBatchHTTPWithConcurrentTraceWrites(t *testing.T) {
	// 1. 写入一千条旧 Trace 和截止时间等值记录，通过 HTTP 取得预览上限。
	path := filepath.Join(t.TempDir(), "traces.db")
	logDB := openCleanupHTTPFileDB(t, path, 3000)
	require.NoError(t, logDB.AutoMigrate(&models.RequestTrace{}))
	cutoff := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Unix()
	boundaryTrace := models.RequestTrace{RequestID: "boundary", CreatedAt: cutoff}
	require.NoError(t, logDB.Create(&boundaryTrace).Error)
	traces := make([]models.RequestTrace, 1000)
	for index := range traces {
		traces[index] = models.RequestTrace{RequestID: fmt.Sprintf("old-%d", index), CreatedAt: cutoff - 1}
	}
	require.NoError(t, logDB.CreateInBatches(traces, 100).Error)
	application := app.NewApplication()
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	application.SetLogDB(logDB)
	server := newCleanupHTTPServer(t, application, &Handler{StatsCache: dao.NewStatsCache()})
	status, body := cleanupHTTPRequest(t, server, http.MethodGet, "/api/system/cleanup/preview?database=log&table=request_traces&cutoff_date=2026-07-20", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var preview CleanupTablePreviewResponse
	require.NoError(t, json.Unmarshal(body, &preview))
	require.Equal(t, int64(1001), preview.Total)
	require.Equal(t, int64(1000), preview.ToDelete)
	require.Equal(t, fmt.Sprint(traces[len(traces)-1].ID), preview.SnapshotMaxKey)

	// 2. 独立连接持续提交旧时间戳的新 ID，通过通道确认每批前已有写入提交。
	writerDB := openCleanupHTTPFileDB(t, path, 3000)
	writerContext, cancelWriter := context.WithCancel(t.Context())
	writerDone := make(chan struct{})
	writeCommitted := make(chan struct{}, 1)
	var written int64
	var writerError error
	go func() {
		defer close(writerDone)
		writeInterval := time.NewTimer(10 * time.Millisecond)
		defer writeInterval.Stop()
		for {
			select {
			case <-writerContext.Done():
				return
			case <-writeInterval.C:
				trace := models.RequestTrace{RequestID: fmt.Sprintf("new-%d", written), CreatedAt: cutoff - 1}
				if err := writerDB.Create(&trace).Error; err != nil {
					writerError = err
					return
				}
				written++
				select {
				case writeCommitted <- struct{}{}:
				default:
				}
				writeInterval.Reset(10 * time.Millisecond)
			}
		}
	}()
	t.Cleanup(func() {
		cancelWriter()
		<-writerDone
	})

	// 3. 连续调用真实批次入口，收到 500、500、0 后核对累计数和保留范围。
	request := CleanupTableBatchRequest{Database: "log", Table: "request_traces", CutoffDate: "2026-07-20", SnapshotMaxKey: preview.SnapshotMaxKey}
	var totalDeleted int64
	for _, expected := range []CleanupTableBatchResponse{{Deleted: 500, HasMore: true}, {Deleted: 500, HasMore: true}, {Deleted: 0, HasMore: false}} {
		select {
		case <-writeCommitted:
		case <-writerDone:
			t.Fatalf("后台写入提前结束：%v", writerError)
		case <-time.After(10 * time.Second):
			t.Fatal("等待后台写入提交超时")
		}
		status, body = cleanupHTTPRequest(t, server, http.MethodPost, "/api/system/cleanup/batch", request)
		require.Equal(t, http.StatusOK, status, string(body))
		var response CleanupTableBatchResponse
		require.NoError(t, json.Unmarshal(body, &response))
		require.Equal(t, expected, response)
		totalDeleted += response.Deleted
	}
	cancelWriter()
	<-writerDone
	require.NoError(t, writerError)
	require.GreaterOrEqual(t, written, int64(3))
	require.Equal(t, preview.ToDelete, totalDeleted)
	var remaining int64
	require.NoError(t, logDB.Model(&models.RequestTrace{}).Count(&remaining).Error)
	require.Equal(t, written+1, remaining)
	require.NoError(t, logDB.Model(&models.RequestTrace{}).Where("id > ? AND created_at < ?", preview.SnapshotMaxKey, cutoff).Count(&remaining).Error)
	require.Equal(t, written, remaining)
	var retainedTrace models.RequestTrace
	require.NoError(t, logDB.First(&retainedTrace, boundaryTrace.ID).Error)
	require.Equal(t, boundaryTrace, retainedTrace)
}

// TestCleanupTableBatchHTTPUnavailableLogDatabase 验证日志库缺失或关闭时返回既有错误。
func TestCleanupTableBatchHTTPUnavailableLogDatabase(t *testing.T) {
	for _, unavailable := range []string{"missing", "closed"} {
		t.Run(unavailable, func(t *testing.T) {
			// 1. 挂载缺失或已经关闭的日志库。
			application := app.NewApplication()
			application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
			if unavailable == "closed" {
				logDB := openCleanupHTTPFileDB(t, filepath.Join(t.TempDir(), "closed.db"), 50)
				application.SetLogDB(logDB)
				require.NoError(t, closeCleanupDB(logDB))
			}
			server := newCleanupHTTPServer(t, application, &Handler{StatsCache: dao.NewStatsCache()})

			// 2. 批次失败只返回日志库不可用错误。
			status, body := cleanupHTTPRequest(t, server, http.MethodPost, "/api/system/cleanup/batch", CleanupTableBatchRequest{
				Database: "log", Table: "request_traces", CutoffDate: "2026-07-20", SnapshotMaxKey: "1000",
			})
			require.Equal(t, http.StatusServiceUnavailable, status, string(body))
			require.JSONEq(t, `{"code":"LogDatabaseUnavailable","message":"log database is temporarily unavailable"}`, string(body))
		})
	}
}

// openCleanupHTTPFileDB 打开独立 SQLite 连接并检查 WAL 和测试锁等待时间。
func openCleanupHTTPFileDB(t *testing.T, path string, busyTimeout int) *gorm.DB {
	t.Helper()
	// 1. 打开文件数据库并限定单连接。
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)", path, busyTimeout)), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 2. 检查当前连接实际使用的日志模式和等待时间。
	var journalMode string
	require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error)
	require.Equal(t, "wal", journalMode)
	var actualBusyTimeout int
	require.NoError(t, db.Raw("PRAGMA busy_timeout").Scan(&actualBusyTimeout).Error)
	require.Equal(t, busyTimeout, actualBusyTimeout)
	return db
}

// newCleanupHTTPServer 按生产绑定方式挂载预览和批次入口。
func newCleanupHTTPServer(t *testing.T, application app.Application, h *Handler) *httptest.Server {
	t.Helper()
	// 1. 注册真实适配器并启动 HTTP 服务。
	adapter := api.NewAdapter(nil, zap.NewNop(), application)
	router := gin.New()
	router.GET("/api/system/cleanup/preview", api.Adapt(adapter, api.BindQuery, h.CleanupTablePreview))
	router.POST("/api/system/cleanup/batch", api.Adapt(adapter, api.BindJSON, h.CleanupTableBatch))
	server := httptest.NewServer(router)
	server.Client().Timeout = 10 * time.Second
	t.Cleanup(server.Close)
	return server
}

// cleanupHTTPRequest 发送真实 HTTP 请求并读取状态码和响应正文。
func cleanupHTTPRequest(t *testing.T, server *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	// 1. 编码请求并通过 HTTP 客户端发送。
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()

	// 2. 读取完整响应正文。
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, responseBody
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
