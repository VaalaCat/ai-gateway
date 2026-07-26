package dao

import "github.com/VaalaCat/ai-gateway/internal/pkg/app"

type LogCleanupTimeColumn uint8

const (
	LogCleanupCreatedAt LogCleanupTimeColumn = iota
	LogCleanupDate
)

type LogCleanupTable struct {
	Name       string
	TimeColumn LogCleanupTimeColumn
}

var logHourlyCleanupTables = []LogCleanupTable{
	{Name: "usage_hourly_buckets", TimeColumn: LogCleanupDate},
	{Name: "usage_duration_histograms", TimeColumn: LogCleanupDate},
	{Name: "usage_ttft_histograms", TimeColumn: LogCleanupDate},
	{Name: "usage_tps_histograms", TimeColumn: LogCleanupDate},
	{Name: "usage_user_ttft_histograms", TimeColumn: LogCleanupDate},
	{Name: "usage_user_tps_histograms", TimeColumn: LogCleanupDate},
}

func LogCleanupTables(target string, mode app.DatabaseLayoutMode) []LogCleanupTable {
	switch target {
	case "logs":
		if mode == app.DatabaseLayoutSplit {
			return []LogCleanupTable{{Name: "request_logs"}}
		}
		return []LogCleanupTable{{Name: "usage_logs"}}
	case "traces":
		if mode == app.DatabaseLayoutSplit {
			return []LogCleanupTable{{Name: "request_traces"}}
		}
		return []LogCleanupTable{{Name: "usage_log_traces"}}
	case "hourly_buckets":
		if mode == app.DatabaseLayoutSplit {
			return append([]LogCleanupTable(nil), logHourlyCleanupTables...)
		}
		return append([]LogCleanupTable(nil), logHourlyCleanupTables[:4]...)
	default:
		return nil
	}
}
