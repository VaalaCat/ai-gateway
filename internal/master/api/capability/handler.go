package capability

import (
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type Handler struct{}

type Response struct {
	Token            TokenCapabilities `json:"token"`
	ModelMarketplace *bool             `json:"model_marketplace,omitempty"`
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
		}, nil
	}

	settings := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Setting()
	response := Response{Token: TokenCapabilities{
		CanEditModelWhitelist: settings.LookupBool(consts.SettingKeyTokenModelWhitelistSelfService, false),
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
