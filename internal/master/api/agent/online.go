package agent

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type OnlineAgentInfo struct {
	AgentID                 string                         `json:"agent_id"`
	Name                    string                         `json:"name"`
	Tags                    string                         `json:"tags"`
	HTTPAddresses           string                         `json:"http_addresses,omitempty"`            // Legacy: effective addresses
	ConfiguredHTTPAddresses string                         `json:"configured_http_addresses,omitempty"` // DB-configured addresses
	EffectiveHTTPAddresses  string                         `json:"effective_http_addresses,omitempty"`  // Merged effective addresses
	LastSeen                int64                          `json:"last_seen"`
	PendingUsage            int                            `json:"pending_usage"` // admin-only,见 pendingFromRuntime
	Connection              connectivity.ConnectionSummary `json:"connection"`
}

// pendingFromRuntime 是 nil 安全 helper:getRuntime 为 nil 或该 agent 无运行时快照时返回 0,
// 供 Online 仅在 admin 分支填充 pending_usage(与 Detail 的 GetRuntime 用法同源)。
func pendingFromRuntime(getRuntime func(agentID string) *msync.AgentRuntime, agentID string) int {
	if getRuntime == nil {
		return 0
	}
	rt := getRuntime(agentID)
	if rt == nil {
		return 0
	}
	return rt.PendingUsage
}

func (h *Handler) Online(c *app.Context, _ api.EmptyRequest) ([]OnlineAgentInfo, error) {
	if h.Connections == nil || h.GetOnlineAgentIDs == nil {
		return nil, api.InternalError("connection service not available", nil)
	}
	ids := h.GetOnlineAgentIDs()
	if len(ids) == 0 {
		h.Connections.BuildMany(nil)
		return []OnlineAgentInfo{}, nil
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
		return nil, api.InternalError("list online agents failed", err)
	}

	h.enrichLastSeen(agents)
	batch := h.Connections.BuildMany(agents)
	isAdmin := c.UserInfo != nil && c.UserInfo.Role == 2

	result := make([]OnlineAgentInfo, 0, len(agents))
	for _, a := range agents {
		snapshot, ok := batch.Items[a.AgentID]
		if !ok {
			return nil, api.InternalError("connection snapshot unavailable", nil)
		}
		if snapshot.Control.State != "connected" {
			continue
		}
		info := OnlineAgentInfo{
			AgentID:                 a.AgentID,
			Name:                    a.Name,
			Tags:                    a.Tags,
			ConfiguredHTTPAddresses: a.HTTPAddresses,
			LastSeen:                a.LastSeen,
			Connection:              connectionSummary(snapshot),
		}

		if isAdmin {
			effective := a.HTTPAddresses
			if h.Hub != nil {
				addrs := h.Hub.GetAgentAddresses(a.AgentID, a.HTTPAddresses)
				if len(addrs) > 0 {
					addrJSON, _ := json.Marshal(addrs)
					effective = string(addrJSON)
				} else {
					effective = ""
				}
			}
			info.HTTPAddresses = effective
			info.EffectiveHTTPAddresses = effective
			info.PendingUsage = pendingFromRuntime(h.GetRuntime, a.AgentID)
		}
		result = append(result, info)
	}

	return result, nil
}
