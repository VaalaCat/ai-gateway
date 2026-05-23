package agent

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type OnlineAgentInfo struct {
	AgentID                 string `json:"agent_id"`
	Name                    string `json:"name"`
	Tags                    string `json:"tags"`
	HTTPAddresses           string `json:"http_addresses,omitempty"`            // Legacy: effective addresses
	ConfiguredHTTPAddresses string `json:"configured_http_addresses,omitempty"` // DB-configured addresses
	EffectiveHTTPAddresses  string `json:"effective_http_addresses,omitempty"`  // Merged effective addresses
	LastSeen                int64  `json:"last_seen"`
}

func (h *Handler) Online(c *app.Context, _ api.EmptyRequest) ([]OnlineAgentInfo, error) {
	ids := h.GetOnlineAgentIDs()
	if len(ids) == 0 {
		return []OnlineAgentInfo{}, nil
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
		return nil, api.InternalError("list online agents failed", err)
	}

	isAdmin := c.UserInfo != nil && c.UserInfo.Role == 2

	result := make([]OnlineAgentInfo, len(agents))
	for i, a := range agents {
		result[i] = OnlineAgentInfo{
			AgentID:                 a.AgentID,
			Name:                    a.Name,
			Tags:                    a.Tags,
			ConfiguredHTTPAddresses: a.HTTPAddresses,
			LastSeen:                a.LastSeen,
		}

		if !isAdmin {
			continue
		}

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
		result[i].HTTPAddresses = effective
		result[i].EffectiveHTTPAddresses = effective
	}

	if h.Hub != nil && h.Hub.Heartbeat != nil {
		msync.EnrichLastSeen(h.Hub.Heartbeat, result,
			func(it OnlineAgentInfo) string { return it.AgentID },
			func(it OnlineAgentInfo) int64 { return it.LastSeen },
			func(it *OnlineAgentInfo, ts int64) { it.LastSeen = ts },
		)
	}

	return result, nil
}
