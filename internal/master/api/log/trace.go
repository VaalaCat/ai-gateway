package log

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type TraceRequest struct {
	RequestID string `uri:"request_id" binding:"required"`
}

func (h *Handler) GetTrace(c *app.Context, req TraceRequest) (*models.UsageLogTrace, error) {
	scope := middleware.GetScope(c.Context)
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	// For normal users, verify ownership via usage_log
	if scope != nil && !scope.IsAdmin {
		usageLog, err := q.UsageLog().GetByRequestID(req.RequestID)
		if err != nil {
			return nil, api.NotFoundError("trace not found")
		}
		if usageLog.UserID != scope.UserID {
			return nil, api.NotFoundError("trace not found")
		}
	}

	trace, err := q.UsageLog().GetTraceByRequestID(req.RequestID)
	if err != nil {
		return nil, api.NotFoundError("trace not found")
	}
	return trace, nil
}
