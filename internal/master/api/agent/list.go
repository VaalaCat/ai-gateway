package agent

import (
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type AgentResponse struct {
	ID                      uint                           `json:"id"`
	AgentID                 string                         `json:"agent_id"`
	Name                    string                         `json:"name"`
	Status                  int                            `json:"status"`
	LastSeen                int64                          `json:"last_seen"`
	CreatedAt               int64                          `json:"created_at"`
	HTTPAddresses           string                         `json:"http_addresses,omitempty"`            // Legacy: effective addresses
	ConfiguredHTTPAddresses string                         `json:"configured_http_addresses,omitempty"` // DB-configured addresses
	EffectiveHTTPAddresses  string                         `json:"effective_http_addresses,omitempty"`  // Merged effective addresses
	Tags                    string                         `json:"tags"`
	ProxyURL                string                         `json:"proxy_url"`
	RelayMode               string                         `json:"relay_mode"`
	PeerRouteMode           string                         `json:"peer_route_mode"`
	Connection              connectivity.ConnectionSummary `json:"connection"`
}

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[AgentResponse], error) {
	if h.Connections == nil {
		return api.PaginatedResponse[AgentResponse]{}, api.InternalError("connection service not available", nil)
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	agents, total, err := q.Agent().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.AgentListFilter{Search: req.Search, Status: statusFilter},
	)
	if err != nil {
		return api.PaginatedResponse[AgentResponse]{}, api.InternalError("list agents failed", err)
	}

	h.enrichLastSeen(agents)
	batch := h.Connections.BuildMany(agents)
	isAdmin := c.UserInfo != nil && c.UserInfo.Role == 2

	items := make([]AgentResponse, len(agents))
	for i, a := range agents {
		snapshot, ok := batch.Items[a.AgentID]
		if !ok {
			return api.PaginatedResponse[AgentResponse]{}, api.InternalError("connection snapshot unavailable", nil)
		}
		items[i] = AgentResponse{
			ID:                      a.ID,
			AgentID:                 a.AgentID,
			Name:                    a.Name,
			Status:                  a.Status,
			LastSeen:                a.LastSeen,
			CreatedAt:               a.CreatedAt,
			ConfiguredHTTPAddresses: a.HTTPAddresses,
			Tags:                    a.Tags,
			ProxyURL:                a.ProxyURL,
			RelayMode:               a.RelayMode,
			PeerRouteMode:           a.PeerRouteMode,
			Connection:              connectionSummary(snapshot),
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
		items[i].HTTPAddresses = effective
		items[i].EffectiveHTTPAddresses = effective
	}

	return api.PaginatedResponse[AgentResponse]{Data: items, Total: total, Page: page, PageSize: pageSize}, nil
}
