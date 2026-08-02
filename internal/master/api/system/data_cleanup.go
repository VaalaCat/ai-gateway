package system

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type CleanupTablePreviewRequest struct {
	Database   string `form:"database" binding:"required,oneof=core log"`
	Table      string `form:"table" binding:"required"`
	CutoffDate string `form:"cutoff_date" binding:"required"`
}

type CleanupTablePreviewResponse struct {
	Database       string `json:"database"`
	Table          string `json:"table"`
	CutoffDate     string `json:"cutoff_date"`
	Total          int64  `json:"total"`
	ToDelete       int64  `json:"to_delete"`
	SnapshotMaxKey string `json:"snapshot_max_key"`
}

type CleanupTableBatchRequest struct {
	Database       string `json:"database" binding:"required,oneof=core log"`
	Table          string `json:"table" binding:"required"`
	CutoffDate     string `json:"cutoff_date" binding:"required"`
	SnapshotMaxKey string `json:"snapshot_max_key" binding:"required"`
}

type CleanupTableBatchResponse struct {
	Deleted int64 `json:"deleted"`
	HasMore bool  `json:"has_more"`
}

func (h *Handler) CleanupTablePreview(c *app.Context, req CleanupTablePreviewRequest) (CleanupTablePreviewResponse, error) {
	cutoff, err := parseCleanupCutoff(time.Now(), req.CutoffDate)
	if err != nil {
		return CleanupTablePreviewResponse{}, cleanupCutoffError(err)
	}
	preview, err := dao.PreviewCleanupTable(c.RequestContext(), c.App.GetCoreDB(), c.App.GetLogDB(), dao.CleanupTableRequest{
		Database: req.Database, Table: req.Table, Cutoff: cutoff,
	})
	if err != nil {
		return CleanupTablePreviewResponse{}, mapCleanupError(req.Database, err)
	}
	return CleanupTablePreviewResponse{
		Database: preview.Database, Table: preview.Table, CutoffDate: req.CutoffDate,
		Total: preview.Total, ToDelete: preview.ToDelete, SnapshotMaxKey: preview.SnapshotMaxKey,
	}, nil
}

func (h *Handler) CleanupTableBatch(c *app.Context, req CleanupTableBatchRequest) (CleanupTableBatchResponse, error) {
	cutoff, err := parseCleanupCutoff(time.Now(), req.CutoffDate)
	if err != nil {
		return CleanupTableBatchResponse{}, cleanupCutoffError(err)
	}
	result, err := dao.DeleteCleanupTableBatch(c.RequestContext(), c.App.GetCoreDB(), c.App.GetLogDB(), dao.CleanupTableRequest{
		Database: req.Database, Table: req.Table, Cutoff: cutoff, SnapshotMaxKey: req.SnapshotMaxKey,
	})
	if err != nil {
		return CleanupTableBatchResponse{}, mapCleanupError(req.Database, err)
	}
	if h.StatsCache != nil {
		h.StatsCache.Clear()
	}
	return CleanupTableBatchResponse{Deleted: result.Deleted, HasMore: result.HasMore}, nil
}

func parseCleanupCutoff(now time.Time, value string) (time.Time, error) {
	cutoff, err := time.Parse("2006-01-02", value)
	if err != nil || cutoff.Format("2006-01-02") != value {
		return time.Time{}, errors.New("cleanup cutoff must use YYYY-MM-DD")
	}
	today, err := time.Parse("2006-01-02", now.UTC().Format("2006-01-02"))
	if err != nil {
		return time.Time{}, err
	}
	if cutoff.After(today) {
		return time.Time{}, errors.New("cleanup cutoff cannot be in the future")
	}
	return cutoff.UTC(), nil
}

func cleanupCutoffError(err error) error {
	code := "InvalidCleanupRequest"
	if strings.Contains(err.Error(), "future") {
		code = "InvalidCleanupCutoff"
	}
	return api.ErrorWithCode(http.StatusBadRequest, code, err.Error(), nil)
}

func mapCleanupError(database string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, dao.ErrCleanupTableNotAllowed) {
		return api.ErrorWithCode(http.StatusBadRequest, "CleanupTableNotAllowed", "cleanup table is not allowed", nil)
	}
	if errors.Is(err, dao.ErrInvalidCleanupSnapshot) {
		return api.ErrorWithCode(http.StatusBadRequest, "InvalidCleanupRequest", "invalid cleanup snapshot", nil)
	}
	if database == "log" {
		err = dao.WrapLogDatabaseError(err)
	}
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return api.ErrorWithCode(http.StatusServiceUnavailable, "LogDatabaseUnavailable", "log database is temporarily unavailable", nil)
	}
	if isCleanupDatabaseBusy(err) {
		return api.ErrorWithCode(http.StatusServiceUnavailable, "CleanupDatabaseBusy", "cleanup database is busy", nil)
	}
	return api.ErrorWithCode(http.StatusInternalServerError, "CleanupFailed", "cleanup failed", nil)
}

func isCleanupDatabaseBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "sqlite_busy")
}
