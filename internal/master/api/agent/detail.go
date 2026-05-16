package agent

import (
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
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
	Runtime                 *RuntimeInfo `json:"runtime"`
	ConfiguredHTTPAddresses string       `json:"configured_http_addresses,omitempty"`
	EffectiveHTTPAddresses  string       `json:"effective_http_addresses,omitempty"`
}

type RuntimeInfo struct {
	Uptime            int64                                `json:"uptime"`
	CachedTokens      int                                  `json:"cached_tokens"`
	CachedChannels    int                                  `json:"cached_channels"`
	CachedModels      int                                  `json:"cached_models"`
	ActiveConnections int                                  `json:"active_connections"`
	Version           int64                                `json:"version"`
	MasterVersion     int64                                `json:"master_version"`
	CacheStats        map[string]protocol.CacheEntityStats `json:"cache_stats,omitempty"`
}

func (h *Handler) Detail(c *app.Context, req DetailRequest) (AgentDetailResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	agentPtr, err := q.Agent().GetByID(uint(id))
	if err != nil {
		return AgentDetailResponse{}, api.NotFoundError("agent not found")
	}
	agent := *agentPtr
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
		ConfiguredHTTPAddresses: configured,
		EffectiveHTTPAddresses:  effective,
	}, nil
}

func fromAgentRuntime(rt *msync.AgentRuntime) *RuntimeInfo {
	return &RuntimeInfo{
		Uptime:            rt.Uptime,
		CachedTokens:      rt.CachedTokens,
		CachedChannels:    rt.CachedChannels,
		CachedModels:      rt.CachedModels,
		ActiveConnections: rt.ActiveConnections,
		Version:           rt.Version,
		MasterVersion:     rt.MasterVersion,
		CacheStats:        rt.CacheStats,
	}
}
