package api_request_log

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Get(c *app.Context, req RequestIDRequest) (models.APIRequestLog, error) {
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRequestLog()
	entry, err := query.GetByRequestID(req.RequestID)
	if err != nil {
		return models.APIRequestLog{}, mapLogReadError(err)
	}
	return *entry, nil
}

func (h *Handler) PortalGet(c *app.Context, req RequestIDRequest) (PortalLogResponse, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil || scope.UserID == 0 {
		return PortalLogResponse{}, api.UnauthorizedError("not authenticated")
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRequestLog()
	entry, err := query.GetByRequestIDAndUserID(req.RequestID, scope.UserID)
	if err != nil {
		return PortalLogResponse{}, mapLogReadError(err)
	}
	return newPortalLogResponse(*entry), nil
}
