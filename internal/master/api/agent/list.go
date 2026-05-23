package agent

import (
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type AgentResponse struct {
	ID                      uint   `json:"id"`
	AgentID                 string `json:"agent_id"`
	Name                    string `json:"name"`
	Status                  int    `json:"status"`
	LastSeen                int64  `json:"last_seen"`
	CreatedAt               int64  `json:"created_at"`
	HTTPAddresses           string `json:"http_addresses,omitempty"`            // Legacy: effective addresses
	ConfiguredHTTPAddresses string `json:"configured_http_addresses,omitempty"` // DB-configured addresses
	EffectiveHTTPAddresses  string `json:"effective_http_addresses,omitempty"`  // Merged effective addresses
	Tags                    string `json:"tags"`
}

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[AgentResponse], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	agents, total, err := q.Agent().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.AgentListFilter{Search: req.Search, Status: statusFilter},
	)
	if err != nil {
		return api.PaginatedResponse[AgentResponse]{}, api.InternalError("list agents failed", err)
	}

	isAdmin := c.UserInfo != nil && c.UserInfo.Role == 2

	items := make([]AgentResponse, len(agents))
	for i, a := range agents {
		items[i] = AgentResponse{
			ID:                      a.ID,
			AgentID:                 a.AgentID,
			Name:                    a.Name,
			Status:                  a.Status,
			LastSeen:                a.LastSeen,
			CreatedAt:               a.CreatedAt,
			ConfiguredHTTPAddresses: a.HTTPAddresses,
			Tags:                    a.Tags,
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

	if h.Hub != nil && h.Hub.Heartbeat != nil {
		msync.EnrichLastSeen(h.Hub.Heartbeat, items,
			func(it AgentResponse) string { return it.AgentID },
			func(it AgentResponse) int64 { return it.LastSeen },
			func(it *AgentResponse, ts int64) { it.LastSeen = ts },
		)
	}

	return api.PaginatedResponse[AgentResponse]{Data: items, Total: total, Page: page, PageSize: pageSize}, nil
}
