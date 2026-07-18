package observability

import (
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// AgentHealthRow 是某 agent 近窗健康原始信号（红黄绿由前端按 Settings 阈值算）。
type AgentHealthRow struct {
	AgentID    string  `json:"agent_id"`
	Requests   int64   `json:"requests"`
	Failed     int64   `json:"failed"`
	ErrorRate  float64 `json:"error_rate"`
	QPS        float64 `json:"qps"`
	WindowSecs int     `json:"window_secs"`
}

type RecentHealthResponse struct {
	Agents     []AgentHealthRow `json:"agents"`
	WindowSecs int              `json:"window_secs"`
}

// GetRecentHealth 返回各 agent 近窗错误率/QPS。窗口取 health_window_seconds 设置（默认 300）。
func (h *Handler) GetRecentHealth(c *app.Context, _ api.EmptyRequest) (RecentHealthResponse, error) {
	win := h.healthWindowSecs(c)
	since := time.Now().Unix() - int64(win)
	rows, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Stats().RecentAgentHealth(since)
	if err != nil {
		return RecentHealthResponse{}, api.InternalError("recent health query failed", err)
	}
	resp := RecentHealthResponse{Agents: make([]AgentHealthRow, 0, len(rows)), WindowSecs: win}
	for _, r := range rows {
		hr := AgentHealthRow{AgentID: r.AgentID, Requests: r.Requests, Failed: r.Failed, WindowSecs: win}
		if r.Requests > 0 {
			hr.ErrorRate = float64(r.Failed) / float64(r.Requests)
		}
		hr.QPS = float64(r.Requests) / float64(win)
		resp.Agents = append(resp.Agents, hr)
	}
	return resp, nil
}

// healthWindowSecs 读 health_window_seconds 有效值（DB 覆盖 > Defaults），缺省/异常回 300。
func (h *Handler) healthWindowSecs(c *app.Context) int {
	win := 300
	if d, ok := settings.Defaults()["health_window_seconds"]; ok {
		if v, err := strconv.Atoi(d); err == nil && v > 0 {
			win = v
		}
	}
	records, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Setting().GetAll()
	if err == nil {
		for _, r := range records {
			if r.Key == "health_window_seconds" {
				if v, err := strconv.Atoi(r.Value); err == nil && v > 0 {
					win = v
				}
			}
		}
	}
	return win
}
