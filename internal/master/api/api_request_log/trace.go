package api_request_log

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

type RequestIDRequest struct {
	RequestID string `uri:"request_id" form:"request_id" binding:"required"`
}

func (h *Handler) GetTrace(c *app.Context, req RequestIDRequest) (models.APIRequestTrace, error) {
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRequestLog()
	_, err := query.GetByRequestID(req.RequestID)
	if err != nil {
		return models.APIRequestTrace{}, mapTraceReadError(err)
	}
	trace, err := query.GetTraceByRequestID(req.RequestID)
	if err != nil {
		return models.APIRequestTrace{}, mapTraceReadError(err)
	}
	return *trace, nil
}

func (h *Handler) PortalGetTrace(c *app.Context, req RequestIDRequest) (models.APIRequestTrace, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil || scope.UserID == 0 {
		return models.APIRequestTrace{}, api.UnauthorizedError("not authenticated")
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRequestLog()
	if _, err := query.GetByRequestIDAndUserID(req.RequestID, scope.UserID); err != nil {
		return models.APIRequestTrace{}, mapTraceReadError(err)
	}
	trace, err := query.GetTraceByRequestID(req.RequestID)
	if err != nil {
		return models.APIRequestTrace{}, mapTraceReadError(err)
	}
	return *trace, nil
}

func mapLogReadError(err error) error {
	switch {
	case errors.Is(err, dao.ErrLogDatabaseUnavailable):
		return logDatabaseUnavailableError()
	case errors.Is(err, gorm.ErrRecordNotFound):
		return api.NotFoundError("API request log not found")
	default:
		return api.InternalError("read API request log failed", err)
	}
}

func mapTraceReadError(err error) error {
	switch {
	case errors.Is(err, dao.ErrLogDatabaseUnavailable):
		return logDatabaseUnavailableError()
	case errors.Is(err, gorm.ErrRecordNotFound):
		return api.NotFoundError("API request trace not found")
	default:
		return api.InternalError("read API request trace failed", err)
	}
}
