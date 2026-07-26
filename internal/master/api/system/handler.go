package system

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterhistorybackfill "github.com/VaalaCat/ai-gateway/internal/master/historybackfill"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

var startTime = time.Now()

type Handler struct {
	ConnectedCount                func() int
	RefreshProbeTimings           func(context.Context)
	CoreDatabasePath              string
	LogDatabasePath               string
	LegacyDatabasePath            string
	CoreDatabase                  func() *gorm.DB
	LogDatabase                   func() *gorm.DB
	LogDatabaseReady              func() bool
	LogDatabaseError              func() string
	LogQueueSnapshot              func() LogQueueStatus
	RetryLogQueue                 func()
	ClearLogQueueBacklog          func() (ClearLogBacklogResponse, error)
	LegacyArtifactSnapshot        func() (masterdatabase.LegacyArtifact, error)
	HistoryBackfillSnapshot       func() masterhistorybackfill.Status
	RetryHistoryBackfill          func()
	SkipHistoryBackfill           func(context.Context) error
	CompleteHistoryBackfill       func(context.Context) error
	DeleteLegacySource            func(string) error
	DeleteLegacyArtifact          func(string) error
	MarkHistorySourceDeleted      func(context.Context) error
	ReconcileHistorySourceDeleted func(context.Context, string) error

	settingsUpdateOnce   sync.Once
	settingsUpdatePermit chan struct{}
}

func (h *Handler) acquireSettingsUpdate(ctx context.Context) (func(), error) {
	h.settingsUpdateOnce.Do(func() {
		h.settingsUpdatePermit = make(chan struct{}, 1)
		h.settingsUpdatePermit <- struct{}{}
	})
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-h.settingsUpdatePermit:
		if err := context.Cause(ctx); err != nil {
			h.settingsUpdatePermit <- struct{}{}
			return nil, err
		}
		return func() { h.settingsUpdatePermit <- struct{}{} }, nil
	}
}

type TableStats struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type SystemInfo struct {
	Version      string `json:"version"`
	GoVersion    string `json:"go_version"`
	StartTime    int64  `json:"start_time"`
	UptimeSec    int64  `json:"uptime_sec"`
	MemoryAlloc  uint64 `json:"memory_alloc"`
	MemorySys    uint64 `json:"memory_sys"`
	NumGC        uint32 `json:"num_gc"`
	NumGoroutine int    `json:"num_goroutine"`
	OnlineAgents int    `json:"online_agents"`
}

type StatsResponse struct {
	Tables  []TableStats  `json:"tables"`
	System  SystemInfo    `json:"system"`
	Storage StorageStatus `json:"storage"`
}

type StatsRequest struct{}

func (h *Handler) Stats(c *app.Context, _ StatsRequest) (StatsResponse, error) {
	tables := []string{"users", "tokens", "channels", "model_configs", "agents", "usage_logs", "usage_log_traces", "settings"}
	var tableStats []TableStats

	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	stats := q.Stats()
	for _, name := range tables {
		if (name == "usage_logs" || name == "usage_log_traces") && h.LogDatabaseReady != nil && !h.LogDatabaseReady() {
			continue
		}
		count, err := stats.GetTableCount(dao.KnownTable(name))
		if err != nil {
			if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
				continue
			}
			return StatsResponse{}, err
		}
		tableStats = append(tableStats, TableStats{Name: name, Count: count})
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	onlineAgents := 0
	if h.ConnectedCount != nil {
		onlineAgents = h.ConnectedCount()
	}

	info := SystemInfo{
		Version:      "dev",
		GoVersion:    runtime.Version(),
		StartTime:    startTime.Unix(),
		UptimeSec:    int64(time.Since(startTime).Seconds()),
		MemoryAlloc:  m.Alloc,
		MemorySys:    m.Sys,
		NumGC:        m.NumGC,
		NumGoroutine: runtime.NumGoroutine(),
		OnlineAgents: onlineAgents,
	}

	coreDB := c.App.GetCoreDB()
	if h.CoreDatabase != nil {
		coreDB = h.CoreDatabase()
	}
	var logDB *gorm.DB
	if h.LogDatabase != nil {
		logDB = h.LogDatabase()
	}
	logReady := logDB != nil
	if h.LogDatabaseReady != nil {
		logReady = h.LogDatabaseReady()
	}
	var logError string
	if h.LogDatabaseError != nil {
		logError = h.LogDatabaseError()
	}
	storage := StorageStatus{
		CoreDB:   buildDatabaseStatus(c.RequestContext(), h.CoreDatabasePath, coreDB, coreDB != nil, ""),
		LogDB:    buildDatabaseStatus(c.RequestContext(), h.LogDatabasePath, logDB, logReady, logError),
		LegacyDB: buildLegacyDatabaseStatus(h.LegacyDatabasePath),
	}
	if h.LogQueueSnapshot != nil {
		storage.LogQueue = h.LogQueueSnapshot()
	}
	if h.HistoryBackfillSnapshot != nil {
		storage.HistoryBackfill = h.HistoryBackfillSnapshot()
		if storage.HistoryBackfill.State == masterhistorybackfill.StateCompleted &&
			h.ReconcileHistorySourceDeleted != nil {
			if err := h.ReconcileHistorySourceDeleted(c.RequestContext(), storage.HistoryBackfill.SourcePath); err == nil {
				storage.HistoryBackfill = h.HistoryBackfillSnapshot()
			}
		}
	}
	if h.LegacyArtifactSnapshot != nil {
		artifact, _ := h.LegacyArtifactSnapshot()
		storage.LegacyArtifact = (masterdatabase.LegacyArtifactDeletionValidator{
			CoreDB: coreDB, LogDB: logDB, LogDatabaseReady: logReady,
			CoreDatabaseDSN: h.CoreDatabasePath, LogDatabaseDSN: h.LogDatabasePath,
			ActiveLegacySources: ActiveLegacyDatabaseSources(h.LegacyDatabasePath, storage.HistoryBackfill),
		}).Check(artifact)
	}
	return StatsResponse{Tables: tableStats, System: info, Storage: storage}, nil
}

func ActiveLegacyDatabaseSources(configuredPath string, status masterhistorybackfill.Status) []string {
	if status.SourceKind == string(masterdatabase.LegacyLayoutNone) || status.State == masterhistorybackfill.StateSourceDeleted {
		return nil
	}
	var sources []string
	if configuredPath != "" {
		sources = append(sources, configuredPath)
	}
	if status.SourcePath == "" {
		return sources
	}
	for _, source := range sources {
		if source == status.SourcePath {
			return sources
		}
	}
	return append(sources, status.SourcePath)
}

type CleanupPreviewRequest struct {
	Target     string `form:"target" binding:"required,oneof=traces logs hourly_buckets"`
	RetainDays int    `form:"retain_days" binding:"required,min=1"`
}

type CleanupPreviewResponse struct {
	Target     string                `json:"target"`
	RetainDays int                   `json:"retain_days"`
	Total      int64                 `json:"total"`
	ToDelete   int64                 `json:"to_delete"`
	Tables     []CleanupTablePreview `json:"tables,omitempty"`
	CutoffUnix int64                 `json:"cutoff_unix"`
	CutoffDate string                `json:"cutoff_date"`
}

type CleanupTablePreview struct {
	Name     string `json:"name"`
	Total    int64  `json:"total"`
	ToDelete int64  `json:"to_delete"`
}

func (h *Handler) CleanupPreview(c *app.Context, req CleanupPreviewRequest) (CleanupPreviewResponse, error) {
	cutoffTime := time.Now().UTC().AddDate(0, 0, -req.RetainDays)
	cutoff := cutoffTime.Unix()
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	db, err := daoCtx.LogDB()
	if err != nil {
		return CleanupPreviewResponse{}, mapLogDatabaseError(err)
	}
	mode, err := daoCtx.DatabaseLayoutMode()
	if err != nil {
		return CleanupPreviewResponse{}, err
	}
	tables := dao.LogCleanupTables(req.Target, mode)
	previews := make([]CleanupTablePreview, 0, len(tables))
	var total, toDelete int64
	for _, table := range tables {
		var tableTotal, tableDelete int64
		if err := db.Table(table.Name).Count(&tableTotal).Error; err != nil {
			return CleanupPreviewResponse{}, mapLogDatabaseError(dao.WrapLogDatabaseError(err))
		}
		query := db.Table(table.Name)
		if table.TimeColumn == dao.LogCleanupDate {
			query = query.Where("date < ?", cutoffTime.Format("2006-01-02"))
		} else {
			query = query.Where("created_at < ?", cutoff)
		}
		if err := query.Count(&tableDelete).Error; err != nil {
			return CleanupPreviewResponse{}, mapLogDatabaseError(dao.WrapLogDatabaseError(err))
		}
		total += tableTotal
		toDelete += tableDelete
		previews = append(previews, CleanupTablePreview{Name: table.Name, Total: tableTotal, ToDelete: tableDelete})
	}

	return CleanupPreviewResponse{
		Target:     req.Target,
		RetainDays: req.RetainDays,
		Total:      total,
		ToDelete:   toDelete,
		Tables:     previews,
		CutoffUnix: cutoff,
		CutoffDate: cutoffTime.Format("2006-01-02"),
	}, nil
}

type CleanupRequest struct {
	Target     string `json:"target" binding:"required,oneof=traces logs hourly_buckets"`
	RetainDays int    `json:"retain_days" binding:"required,min=1"`
	CutoffUnix int64  `json:"cutoff_unix" binding:"required,min=1"`
}

type CleanupResponse struct {
	Deleted int64 `json:"deleted"`
}

func (h *Handler) Cleanup(c *app.Context, req CleanupRequest) (CleanupResponse, error) {
	cutoff, err := validateCleanupCutoff(time.Now().UTC(), req.RetainDays, req.CutoffUnix)
	if err != nil {
		return CleanupResponse{}, api.BadRequestError("invalid cleanup cutoff", err)
	}

	mut := dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext()))
	var deleted int64
	var cleanupErr error
	switch req.Target {
	case "logs":
		deleted, cleanupErr = mut.UsageLog().DeleteLogsBefore(cutoff)
	case "traces":
		deleted, cleanupErr = mut.UsageLog().DeleteTracesBefore(cutoff)
	case "hourly_buckets":
		deleted, cleanupErr = mut.Billing().DeleteHourlyBucketsBefore(cutoff)
	}
	if cleanupErr != nil {
		return CleanupResponse{}, mapLogDatabaseError(cleanupErr)
	}

	return CleanupResponse{Deleted: deleted}, nil
}

func validateCleanupCutoff(now time.Time, retainDays int, cutoffUnix int64) (time.Time, error) {
	if retainDays < 1 || cutoffUnix < 1 {
		return time.Time{}, errors.New("cleanup cutoff is required")
	}
	cutoff := time.Unix(cutoffUnix, 0).UTC()
	if cutoff.After(now) {
		return time.Time{}, errors.New("cleanup cutoff cannot be in the future")
	}
	expected := now.AddDate(0, 0, -retainDays)
	delta := cutoff.Sub(expected)
	if delta < -5*time.Minute || delta > 5*time.Minute {
		return time.Time{}, errors.New("cleanup cutoff does not match retain_days")
	}
	return cutoff, nil
}

func mapLogDatabaseError(err error) error {
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return api.ErrorWithCode(http.StatusServiceUnavailable, "LogDatabaseUnavailable", "log database is temporarily unavailable", nil)
	}
	return err
}
