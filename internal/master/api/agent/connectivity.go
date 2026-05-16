package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	gosync "sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type ConnectivityRequest struct {
	ID string `uri:"id" binding:"required"`
}

type AddressProbeResult struct {
	URL       string `json:"url"`
	Tag       string `json:"tag"`
	Reachable bool   `json:"reachable"`
	LatencyMs int    `json:"latency_ms"`
	Error     string `json:"error"`
}

type ConnectivityResult struct {
	TargetAgentID string               `json:"target_agent_id"`
	TargetName    string               `json:"target_name"`
	Results       []AddressProbeResult `json:"results"`
}

type ConnectivityReport struct {
	AgentID   string               `json:"agent_id"`
	CheckedAt int64                `json:"checked_at"`
	Results   []ConnectivityResult `json:"results"`
}

// In-memory cache for connectivity reports
var (
	connectivityCache   = make(map[string]*ConnectivityReport)
	connectivityCacheMu gosync.RWMutex
)

func (h *Handler) CheckConnectivity(c *app.Context, req ConnectivityRequest) (ConnectivityReport, error) {
	if h.HubCall == nil {
		return ConnectivityReport{}, api.InternalError("hub not available", nil)
	}

	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	sourceAgent, err := q.Agent().GetByID(uint(id))
	if err != nil {
		return ConnectivityReport{}, api.NotFoundError("agent not found")
	}

	// Gather all other agents' addresses
	agents, err := q.Agent().ListActive(sourceAgent.AgentID)
	if err != nil {
		return ConnectivityReport{}, api.InternalError("list active agents failed", err)
	}

	type targetInfo struct {
		AgentID       string          `json:"agent_id"`
		Name          string          `json:"name"`
		HTTPAddresses json.RawMessage `json:"http_addresses"`
	}
	targets := make([]targetInfo, 0, len(agents))
	for _, a := range agents {
		var mergedAddrs []agentproxy.Address
		if h.Hub != nil {
			mergedAddrs = h.Hub.GetAgentAddresses(a.AgentID, a.HTTPAddresses)
		} else {
			mergedAddrs = agentproxy.ParseAddresses(a.HTTPAddresses)
		}
		if len(mergedAddrs) == 0 {
			continue
		}
		addrJSON, err := json.Marshal(mergedAddrs)
		if err != nil {
			continue
		}
		targets = append(targets, targetInfo{
			AgentID:       a.AgentID,
			Name:          a.Name,
			HTTPAddresses: addrJSON,
		})
	}

	result, err := h.HubCall(sourceAgent.AgentID, consts.RPCAgentCheckConnectivity, targets, 30*time.Second)
	if err != nil {
		return ConnectivityReport{}, api.BadRequestError(fmt.Sprintf("connectivity check failed: %v", err), nil)
	}

	var report ConnectivityReport
	report.AgentID = sourceAgent.AgentID
	report.CheckedAt = time.Now().Unix()
	if err := json.Unmarshal(result, &report.Results); err != nil {
		return ConnectivityReport{}, api.InternalError("invalid agent response", err)
	}

	// Cache result
	connectivityCacheMu.Lock()
	connectivityCache[sourceAgent.AgentID] = &report
	connectivityCacheMu.Unlock()

	return report, nil
}

func (h *Handler) GetConnectivity(c *app.Context, req ConnectivityRequest) (*ConnectivityReport, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	agent, err := q.Agent().GetByID(uint(id))
	if err != nil {
		return nil, api.NotFoundError("agent not found")
	}
	connectivityCacheMu.RLock()
	report := connectivityCache[agent.AgentID]
	connectivityCacheMu.RUnlock()
	if report == nil {
		return &ConnectivityReport{AgentID: agent.AgentID}, nil
	}
	return report, nil
}
