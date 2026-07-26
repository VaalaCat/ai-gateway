package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterhistorybackfill "github.com/VaalaCat/ai-gateway/internal/master/historybackfill"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

const (
	databaseAvailable   = "available"
	databaseUnavailable = "unavailable"
)

type DatabaseStatus struct {
	Status          string `json:"status"`
	Path            string `json:"path"`
	SchemaVersion   string `json:"schema_version"`
	LastError       string `json:"last_error"`
	SizeBytes       int64  `json:"size_bytes"`
	OpenConnections int    `json:"open_connections"`
}

type LogQueueStatus struct {
	Pending       int    `json:"pending"`
	Retry         int    `json:"retry"`
	Inflight      int    `json:"inflight"`
	Bytes         int64  `json:"bytes"`
	OldestSeconds int64  `json:"oldest_seconds"`
	Dropped       uint64 `json:"dropped"`
	LastError     string `json:"last_error"`
}

type StorageStatus struct {
	CoreDB          DatabaseStatus                `json:"core_db"`
	LogDB           DatabaseStatus                `json:"log_db"`
	LegacyDB        DatabaseStatus                `json:"legacy_db"`
	LegacyArtifact  masterdatabase.LegacyArtifact `json:"legacy_artifact"`
	LogQueue        LogQueueStatus                `json:"log_delivery_queue"`
	HistoryBackfill masterhistorybackfill.Status  `json:"history_backfill"`
}

type RetryLogQueueRequest struct{}

type RetryLogQueueResponse struct {
	Woken bool `json:"woken"`
}

type RetryHistoryBackfillRequest struct{}

type RetryHistoryBackfillResponse struct {
	Woken bool `json:"woken"`
}

type SkipHistoryBackfillRequest struct {
	Confirm bool `form:"confirm"`
}

type SkipHistoryBackfillResponse struct {
	Skipped bool `json:"skipped"`
}

type CompleteHistoryBackfillRequest struct {
	Confirm bool `json:"confirm"`
}

type CompleteHistoryBackfillResponse struct {
	Completed bool `json:"completed"`
}

type DeleteLegacyFileRequest struct {
	Confirmation string `form:"confirmation"`
}

type DeleteLegacyFileResponse struct {
	Deleted bool `json:"deleted"`
}

type ClearLogBacklogRequest struct {
	Confirm bool `form:"confirm"`
}

type ClearLogBacklogResponse struct {
	Pending int   `json:"pending"`
	Retry   int   `json:"retry"`
	Dropped int   `json:"dropped"`
	Bytes   int64 `json:"bytes"`
}

func LogQueueStatusFrom(status masterlogqueue.DeliveryStatus) LogQueueStatus {
	bytes := int64(status.Queue.Bytes)
	if status.Queue.Bytes > uint64(^uint64(0)>>1) {
		bytes = int64(^uint64(0) >> 1)
	}
	lastError := status.LastError
	if lastError == "" {
		lastError = status.Queue.LastError
	}
	return LogQueueStatus{
		Pending: status.Queue.Pending, Retry: status.Queue.Retry, Inflight: status.Queue.Inflight,
		Bytes: bytes, OldestSeconds: int64(status.Queue.OldestAge.Seconds()), Dropped: status.Queue.Dropped, LastError: lastError,
	}
}

func (h *Handler) RetryLogQueueNow(c *app.Context, _ RetryLogQueueRequest) (RetryLogQueueResponse, error) {
	if err := requireAdmin(c); err != nil {
		return RetryLogQueueResponse{}, err
	}
	if h.RetryLogQueue == nil {
		return RetryLogQueueResponse{}, api.InternalError("log queue retry is unavailable", nil)
	}
	h.RetryLogQueue()
	return RetryLogQueueResponse{Woken: true}, nil
}

func (h *Handler) ClearLogBacklog(c *app.Context, req ClearLogBacklogRequest) (ClearLogBacklogResponse, error) {
	if err := requireAdmin(c); err != nil {
		return ClearLogBacklogResponse{}, err
	}
	if !req.Confirm {
		return ClearLogBacklogResponse{}, api.BadRequestError("confirm=true is required", nil)
	}
	if h.ClearLogQueueBacklog == nil {
		return ClearLogBacklogResponse{}, api.InternalError("log queue clear is unavailable", nil)
	}
	resp, err := h.ClearLogQueueBacklog()
	if err != nil {
		return ClearLogBacklogResponse{}, api.InternalError("persist cleared log queue", err)
	}
	resp.Dropped = resp.Pending + resp.Retry
	return resp, nil
}

func (h *Handler) RetryHistoryBackfillNow(c *app.Context, _ RetryHistoryBackfillRequest) (RetryHistoryBackfillResponse, error) {
	if err := requireAdmin(c); err != nil {
		return RetryHistoryBackfillResponse{}, err
	}
	if h.RetryHistoryBackfill == nil {
		return RetryHistoryBackfillResponse{}, api.InternalError("history backfill retry is unavailable", nil)
	}
	h.RetryHistoryBackfill()
	return RetryHistoryBackfillResponse{Woken: true}, nil
}

func (h *Handler) SkipHistoryBackfillRemaining(c *app.Context, req SkipHistoryBackfillRequest) (SkipHistoryBackfillResponse, error) {
	if err := requireAdmin(c); err != nil {
		return SkipHistoryBackfillResponse{}, err
	}
	if !req.Confirm {
		return SkipHistoryBackfillResponse{}, api.BadRequestError("confirm=true is required", nil)
	}
	if h.SkipHistoryBackfill == nil {
		return SkipHistoryBackfillResponse{}, api.InternalError("history backfill skip is unavailable", nil)
	}
	if err := h.SkipHistoryBackfill(c.RequestContext()); err != nil {
		return SkipHistoryBackfillResponse{}, api.InternalError("skip remaining history backfill", err)
	}
	return SkipHistoryBackfillResponse{Skipped: true}, nil
}

func (h *Handler) CompleteHistoryBackfillNow(c *app.Context, req CompleteHistoryBackfillRequest) (CompleteHistoryBackfillResponse, error) {
	if err := requireAdmin(c); err != nil {
		return CompleteHistoryBackfillResponse{}, err
	}
	if !req.Confirm {
		return CompleteHistoryBackfillResponse{}, api.BadRequestError("confirm=true is required", nil)
	}
	if h.CompleteHistoryBackfill == nil {
		return CompleteHistoryBackfillResponse{}, api.InternalError("history backfill completion is unavailable", nil)
	}
	if err := h.CompleteHistoryBackfill(c.RequestContext()); err != nil {
		return CompleteHistoryBackfillResponse{}, api.InternalError(fmt.Sprintf("complete history backfill: %v", err), err)
	}
	return CompleteHistoryBackfillResponse{Completed: true}, nil
}

func (h *Handler) DeleteHistoryBackfillSource(c *app.Context, req DeleteLegacyFileRequest) (DeleteLegacyFileResponse, error) {
	if err := requireAdmin(c); err != nil {
		return DeleteLegacyFileResponse{}, err
	}
	if req.Confirmation != "DELETE" {
		return DeleteLegacyFileResponse{}, api.BadRequestError("confirmation=DELETE is required", nil)
	}
	if h.DeleteLegacySource == nil || h.MarkHistorySourceDeleted == nil {
		return DeleteLegacyFileResponse{}, api.InternalError("legacy source deletion is unavailable", nil)
	}
	if err := h.DeleteLegacySource(req.Confirmation); err != nil {
		return DeleteLegacyFileResponse{}, api.InternalError("delete legacy source", err)
	}
	if err := h.MarkHistorySourceDeleted(c.RequestContext()); err != nil {
		return DeleteLegacyFileResponse{}, api.InternalError("mark legacy source deleted", err)
	}
	return DeleteLegacyFileResponse{Deleted: true}, nil
}

func (h *Handler) DeleteHistoryBackfillLegacyArtifact(c *app.Context, req DeleteLegacyFileRequest) (DeleteLegacyFileResponse, error) {
	if err := requireAdmin(c); err != nil {
		return DeleteLegacyFileResponse{}, err
	}
	if req.Confirmation != "DELETE" {
		return DeleteLegacyFileResponse{}, api.BadRequestError("confirmation=DELETE is required", nil)
	}
	if h.DeleteLegacyArtifact == nil {
		return DeleteLegacyFileResponse{}, api.InternalError("legacy artifact deletion is unavailable", nil)
	}
	if err := h.DeleteLegacyArtifact(req.Confirmation); err != nil {
		return DeleteLegacyFileResponse{}, api.InternalError("delete legacy artifact", err)
	}
	return DeleteLegacyFileResponse{Deleted: true}, nil
}

func requireAdmin(c *app.Context) error {
	if c == nil || middleware.GetScope(c.Context) == nil || !middleware.GetScope(c.Context).IsAdmin {
		return api.ForbiddenError("admin access is required")
	}
	return nil
}

func buildDatabaseStatus(ctx context.Context, rawPath string, db *gorm.DB, ready bool, lastError string) DatabaseStatus {
	status := DatabaseStatus{Status: databaseUnavailable, Path: safeDatabasePath(rawPath), LastError: lastError}
	if db == nil || !ready {
		return status
	}
	status.Status = databaseAvailable
	sqlDB, err := db.DB()
	if err != nil {
		status.Status = databaseUnavailable
		status.LastError = joinStatusError(status.LastError, err)
		return status
	}
	status.OpenConnections = sqlDB.Stats().OpenConnections
	status.SchemaVersion, err = databaseSchemaVersion(db.WithContext(ctx))
	if err != nil {
		status.Status = databaseUnavailable
		status.LastError = joinStatusError(status.LastError, err)
	}
	status.SizeBytes, err = databaseFilesSize(status.Path)
	if err != nil {
		status.LastError = joinStatusError(status.LastError, err)
	}
	return status
}

func buildLegacyDatabaseStatus(rawPath string) DatabaseStatus {
	path := safeDatabasePath(rawPath)
	status := DatabaseStatus{Status: databaseUnavailable, Path: path}
	if path == "" || path == ":memory:" {
		return status
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil {
		status.LastError = err.Error()
		return status
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		status.LastError = "legacy database is not a regular file"
		return status
	}
	status.Status = databaseAvailable
	status.SizeBytes, err = databaseFilesSize(path)
	if err != nil {
		status.LastError = err.Error()
	}
	return status
}

func MarkHistorySourceDeleted(ctx context.Context, core *gorm.DB, deletedAtUnix int64) error {
	if core == nil {
		return fmt.Errorf("core database is unavailable")
	}
	result := core.WithContext(ctx).Model(&models.HistoryMigration{}).
		Where("id = ? AND state IN ?", models.HistoryMigrationSingletonID,
			[]string{string(masterhistorybackfill.StateCompleted), string(masterhistorybackfill.StateSourceDeleted)}).
		Updates(map[string]any{
			"state": string(masterhistorybackfill.StateSourceDeleted), "source_deleted_at_unix": deletedAtUnix,
		})
	if result.Error != nil {
		return fmt.Errorf("mark history source deleted: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("history migration is not completed")
	}
	return nil
}

func ReconcileHistorySourceDeletion(ctx context.Context, path string, markDeleted func(context.Context) error) error {
	if markDeleted == nil {
		return fmt.Errorf("mark history source deleted callback is unavailable")
	}
	return masterdatabase.AfterLegacyDelete(path, func() error {
		_, err := os.Lstat(path)
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect legacy source for reconciliation: %w", err)
		}
		return markDeleted(ctx)
	})
}

func safeDatabasePath(raw string) string {
	parsed, err := masterdatabase.ParseSQLiteDSN(raw)
	if err != nil {
		return ""
	}
	if parsed.Memory {
		return ":memory:"
	}
	return filepath.Clean(parsed.FilesystemPath)
}

func databaseFilesSize(path string) (int64, error) {
	if path == "" || path == ":memory:" {
		return 0, nil
	}
	var total int64
	for i, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err != nil {
			if i > 0 && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return total, fmt.Errorf("stat database file: %w", err)
		}
		total += info.Size()
	}
	return total, nil
}

func databaseSchemaVersion(db *gorm.DB) (string, error) {
	if db.Migrator().HasTable(&models.DatabaseLayout{}) {
		var marker models.DatabaseLayout
		if err := db.First(&marker, models.DatabaseLayoutID).Error; err != nil {
			return "", err
		}
		return strconv.Itoa(marker.Version), nil
	}
	var version int
	if err := db.Raw("PRAGMA user_version").Scan(&version).Error; err != nil {
		return "", err
	}
	return strconv.Itoa(version), nil
}

func joinStatusError(current string, err error) string {
	if err == nil {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return err.Error()
	}
	return current + "; " + err.Error()
}
