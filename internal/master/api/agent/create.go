package agent

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.Agent], error) {
	relayMode, relayURI, err := normalizeRelayConfiguration(req.RelayMode, req.RelayURI, true)
	if err != nil {
		return api.Created[models.Agent]{}, api.BadRequestError(errInvalidRelayConfiguration.Error(), nil)
	}
	peerRouteMode, err := normalizePeerRouteMode(req.PeerRouteMode, true)
	if err != nil {
		return api.Created[models.Agent]{}, api.BadRequestError(errInvalidPeerRouteMode.Error(), nil)
	}
	if req.AgentID == "" {
		req.AgentID = GenerateRandomID("agent-")
	}
	if req.Secret == "" {
		req.Secret = GenerateRandomID("sec-")
	}
	agent := models.Agent{
		AgentID:       req.AgentID,
		Secret:        req.Secret,
		Name:          req.Name,
		Status:        1,
		HTTPAddresses: req.HTTPAddresses,
		Tags:          req.Tags,
		ProxyURL:      req.ProxyURL,
		RelayMode:     relayMode,
		RelayURI:      relayURI,
		PeerRouteMode: peerRouteMode,
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	m := dao.NewAdminMutation(daoCtx)

	if err := m.Agent().Create(&agent); err != nil {
		return api.Created[models.Agent]{}, api.ConflictError("create agent failed", err)
	}
	events.PublishAgentCreate(c.RequestContext(), c.GetBus(), agent)
	return api.Created[models.Agent]{Value: agent}, nil
}
