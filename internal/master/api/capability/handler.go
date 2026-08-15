package capability

import (
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type Handler struct{ App app.Application }

type Response struct {
	Token            TokenCapabilities      `json:"token"`
	ModelMarketplace *bool                  `json:"model_marketplace,omitempty"`
	GenericAPI       GenericAPICapabilities `json:"generic_api"`
}

type GenericAPICapabilities struct {
	Services       bool                     `json:"services"`
	Access         bool                     `json:"access"`
	Logs           bool                     `json:"logs"`
	WebSocket      bool                     `json:"websocket"`
	ServiceActions GenericAPIServiceActions `json:"service_actions"`
}

type GenericAPIServiceActions struct {
	Create    bool   `json:"create"`
	ManageAll bool   `json:"manage_all"`
	ManageIDs []uint `json:"manage_ids"`
}

type TokenCapabilities struct {
	CanEditModelWhitelist bool `json:"can_edit_model_whitelist"`
}

func (h *Handler) Get(c *app.Context, _ api.EmptyRequest) (Response, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil {
		return Response{}, api.UnauthorizedError("not authenticated")
	}
	if scope.IsAdmin {
		return Response{
			Token:            TokenCapabilities{CanEditModelWhitelist: true},
			ModelMarketplace: truePointer(),
			GenericAPI: GenericAPICapabilities{
				Services: true, Access: true, Logs: true, WebSocket: true,
				ServiceActions: GenericAPIServiceActions{Create: true, ManageAll: true, ManageIDs: []uint{}},
			},
		}, nil
	}

	settings := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Setting()
	response := Response{Token: TokenCapabilities{
		CanEditModelWhitelist: settings.LookupBool(consts.SettingKeyTokenModelWhitelistSelfService, false),
	}, GenericAPI: GenericAPICapabilities{
		Logs:           true,
		ServiceActions: GenericAPIServiceActions{ManageIDs: []uint{}},
	}}
	if settings.LookupBool(consts.SettingKeyModelMarketplaceEnabled, consts.ModelMarketplaceDefaultEnabled) {
		response.ModelMarketplace = truePointer()
	}
	return response, nil
}

func truePointer() *bool {
	value := true
	return &value
}
