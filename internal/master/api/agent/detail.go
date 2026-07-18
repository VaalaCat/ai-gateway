package agent

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type DetailRequest struct {
	ID string `uri:"id" binding:"required"`
}

type AgentDetailResponse struct {
	models.Agent
	Runtime                 *RuntimeInfo                    `json:"runtime"`
	Connection              connectivity.ConnectionSnapshot `json:"connection"`
	RouteTargets            connectivity.RouteTargetsPage   `json:"route_targets"`
	ConfiguredHTTPAddresses string                          `json:"configured_http_addresses,omitempty"`
	EffectiveHTTPAddresses  string                          `json:"effective_http_addresses,omitempty"`
}

type RuntimeInfo struct {
	Uptime            int64                                `json:"uptime"`
	CachedTokens      int                                  `json:"cached_tokens"`
	CachedChannels    int                                  `json:"cached_channels"`
	CachedModels      int                                  `json:"cached_models"`
	ActiveConnections int                                  `json:"active_connections"`
	Version           int64                                `json:"version"`
	MasterVersion     int64                                `json:"master_version"`
	PendingUsage      int                                  `json:"pending_usage"`
	CacheStats        map[string]protocol.CacheEntityStats `json:"cache_stats,omitempty"`
}

func (h *Handler) Detail(c *app.Context, req DetailRequest) (AgentDetailResponse, error) {
	if h.Connections == nil {
		return AgentDetailResponse{}, api.InternalError("connection service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return AgentDetailResponse{}, apiErr
	}
	agent, err := findAgentByID(c, req.ID)
	if err != nil {
		return AgentDetailResponse{}, err
	}
	agents := []models.Agent{agent}
	h.enrichLastSeen(agents)
	agent = agents[0]
	snapshot := h.Connections.Build(agent)
	routeTargets, err := h.routeTargetsPage(snapshot, "", defaultRouteTargetsLimit)
	if err != nil {
		return AgentDetailResponse{}, err
	}
	agent.Secret = "" // redact

	configured := agent.HTTPAddresses
	effective := configured
	if h.Hub != nil {
		addrs := h.Hub.GetAgentAddresses(agent.AgentID, agent.HTTPAddresses)
		if len(addrs) > 0 {
			addrJSON, _ := json.Marshal(addrs)
			effective = string(addrJSON)
		} else {
			effective = ""
		}
	}
	agent.HTTPAddresses = effective

	var runtime *RuntimeInfo
	if h.GetRuntime != nil {
		if rt := h.GetRuntime(agent.AgentID); rt != nil {
			runtime = fromAgentRuntime(rt)
		}
	}

	return AgentDetailResponse{
		Agent:                   agent,
		Runtime:                 runtime,
		Connection:              snapshot,
		RouteTargets:            routeTargets,
		ConfiguredHTTPAddresses: configured,
		EffectiveHTTPAddresses:  effective,
	}, nil
}

func fromAgentRuntime(rt *msync.AgentRuntime) *RuntimeInfo {
	var cacheStats map[string]protocol.CacheEntityStats
	if rt.CacheStats != nil {
		cacheStats = make(map[string]protocol.CacheEntityStats, len(rt.CacheStats))
		for key, value := range rt.CacheStats {
			value.Extra = cloneInt64Map(value.Extra)
			cacheStats[key] = value
		}
	}
	return &RuntimeInfo{
		Uptime:            rt.Uptime,
		CachedTokens:      rt.CachedTokens,
		CachedChannels:    rt.CachedChannels,
		CachedModels:      rt.CachedModels,
		ActiveConnections: rt.ActiveConnections,
		Version:           rt.Version,
		MasterVersion:     rt.MasterVersion,
		PendingUsage:      rt.PendingUsage,
		CacheStats:        cacheStats,
	}
}

func cloneInt64Map(source map[string]int64) map[string]int64 {
	if source == nil {
		return nil
	}
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
