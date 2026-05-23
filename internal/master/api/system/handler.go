package system

import (
	"runtime"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

var startTime = time.Now()

type Handler struct {
	ConnectedCount func() int
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
	Tables []TableStats `json:"tables"`
	System SystemInfo   `json:"system"`
}

type StatsRequest struct{}

func (h *Handler) Stats(c *app.Context, _ StatsRequest) (StatsResponse, error) {
	tables := []string{"users", "tokens", "channels", "model_configs", "agents", "usage_logs", "usage_log_traces", "settings"}
	var tableStats []TableStats

	q := dao.NewAdminQuery(dao.NewContext(c.App))
	stats := q.Stats()
	for _, name := range tables {
		count, err := stats.GetTableCount(dao.KnownTable(name))
		if err != nil {
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

	return StatsResponse{Tables: tableStats, System: info}, nil
}

type CleanupPreviewRequest struct {
	Target     string `form:"target" binding:"required,oneof=traces logs hourly_buckets"`
	RetainDays int    `form:"retain_days" binding:"required,min=1"`
}

type CleanupPreviewResponse struct {
	Target     string `json:"target"`
	RetainDays int    `json:"retain_days"`
	Total      int64  `json:"total"`
	ToDelete   int64  `json:"to_delete"`
}

func (h *Handler) CleanupPreview(c *app.Context, req CleanupPreviewRequest) (CleanupPreviewResponse, error) {
	cutoff := time.Now().AddDate(0, 0, -req.RetainDays).Unix()
	tableName := targetTable(req.Target)

	q := dao.NewAdminQuery(dao.NewContext(c.App))
	stats := q.Stats()
	total, err := stats.GetTableCount(dao.KnownTable(tableName))
	if err != nil {
		return CleanupPreviewResponse{}, err
	}

	var toDelete int64
	daoCtx := dao.NewContext(c.App)
	q2 := daoCtx.GetDB().Table(tableName)
	if req.Target == "hourly_buckets" {
		// hourly bucket 按 date 字符串比较 (与 DeleteHourlyBucketsBefore 一致)
		cutoffDate := time.Unix(cutoff, 0).UTC().Format("2006-01-02")
		q2 = q2.Where("date < ?", cutoffDate)
	} else {
		q2 = q2.Where("created_at < ?", cutoff)
	}
	q2.Count(&toDelete)

	return CleanupPreviewResponse{
		Target:     req.Target,
		RetainDays: req.RetainDays,
		Total:      total,
		ToDelete:   toDelete,
	}, nil
}

type CleanupRequest struct {
	Target     string `json:"target" binding:"required,oneof=traces logs hourly_buckets"`
	RetainDays int    `json:"retain_days" binding:"required,min=1"`
}

type CleanupResponse struct {
	Deleted int64 `json:"deleted"`
}

func (h *Handler) Cleanup(c *app.Context, req CleanupRequest) (CleanupResponse, error) {
	cutoff := time.Now().AddDate(0, 0, -req.RetainDays)

	mut := dao.NewAdminMutation(dao.NewContext(c.App))
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
		return CleanupResponse{}, cleanupErr
	}

	return CleanupResponse{Deleted: deleted}, nil
}

func targetTable(target string) string {
	switch target {
	case "traces":
		return "usage_log_traces"
	case "logs":
		return "usage_logs"
	case "hourly_buckets":
		return "usage_hourly_buckets"
	default:
		return ""
	}
}
