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
	Token TokenCapabilities `json:"token"`
}

type TokenCapabilities struct {
	CanEditModelWhitelist bool `json:"can_edit_model_whitelist"`
}

func (h *Handler) Get(c *app.Context, _ api.EmptyRequest) (Response, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil {
		return Response{}, api.UnauthorizedError("not authenticated")
	}
	canEdit := scope.IsAdmin
	if !canEdit {
		q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
		canEdit = q.Setting().LookupBool(consts.SettingKeyTokenModelWhitelistSelfService, false)
	}
	return Response{Token: TokenCapabilities{CanEditModelWhitelist: canEdit}}, nil
}
