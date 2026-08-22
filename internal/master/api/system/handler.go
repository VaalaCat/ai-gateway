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
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/version"
	"gorm.io/gorm"
)

var startTime = time.Now()

type Handler struct {
	StatsCache                    *dao.StatsCache
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
	Database string `json:"database"`
	Name     string `json:"name"`
	Count    int64  `json:"count"`
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

	tableStats, err := listDatabaseTableStats(c.RequestContext(), models.DatabaseRoleCore, coreDB)
	if err != nil {
		return StatsResponse{}, err
	}
	if c.App.GetDatabaseLayoutMode() == app.DatabaseLayoutSplit && logReady {
		logTableStats, logErr := listDatabaseTableStats(c.RequestContext(), models.DatabaseRoleLog, logDB)
		if logErr != nil {
			if !errors.Is(dao.WrapLogDatabaseError(logErr), dao.ErrLogDatabaseUnavailable) {
				return StatsResponse{}, logErr
			}
		} else {
			tableStats = append(tableStats, logTableStats...)
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	onlineAgents := 0
	if h.ConnectedCount != nil {
		onlineAgents = h.ConnectedCount()
	}

	info := SystemInfo{
		Version:      version.Version,
		GoVersion:    runtime.Version(),
		StartTime:    startTime.Unix(),
		UptimeSec:    int64(time.Since(startTime).Seconds()),
		MemoryAlloc:  m.Alloc,
		MemorySys:    m.Sys,
		NumGC:        m.NumGC,
		NumGoroutine: runtime.NumGoroutine(),
		OnlineAgents: onlineAgents,
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

func listDatabaseTableStats(ctx context.Context, database string, db *gorm.DB) ([]TableStats, error) {
	if db == nil {
		return nil, errors.New("database is unavailable")
	}
	scoped := db.WithContext(ctx)
	var tableNames []string
	if err := scoped.Raw(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`).Scan(&tableNames).Error; err != nil {
		return nil, err
	}

	tables := make([]TableStats, 0, len(tableNames))
	for _, name := range tableNames {
		var count int64
		quotedName := (&gorm.Statement{DB: scoped}).Quote(name)
		if err := scoped.Raw("SELECT COUNT(*) FROM " + quotedName).Scan(&count).Error; err != nil {
			return nil, err
		}
		tables = append(tables, TableStats{Database: database, Name: name, Count: count})
	}
	return tables, nil
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

func mapLogDatabaseError(err error) error {
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return api.ErrorWithCode(http.StatusServiceUnavailable, "LogDatabaseUnavailable", "log database is temporarily unavailable", nil)
	}
	return err
}
