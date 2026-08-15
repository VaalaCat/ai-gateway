package api_route

import (
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	api_backend "github.com/VaalaCat/ai-gateway/internal/master/api/api_backend"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

// RouteTargetCommand selects an existing backend or atomically creates a new
// backend with its first upstream before attaching the route to it.
type RouteTargetCommand struct {
	Mode          string                    `json:"mode" binding:"required,oneof=existing create"`
	BackendID     uint                      `json:"backend_id"`
	Backend       *api_backend.CreateInput  `json:"backend"`
	FirstUpstream *api_upstream.CreateInput `json:"first_upstream"`
}

type targetWriteResult struct {
	BackendID uint
	Upstream  *models.APIUpstream
}

type targetWriter func(*Handler, dao.Context, uint, RouteTargetCommand) (targetWriteResult, error)

var targetWriters = map[string]targetWriter{
	"existing": writeExistingTarget,
	"create":   writeCreatedTarget,
}

func (h *Handler) applyTargetInTx(ctx dao.Context, serviceID uint, command RouteTargetCommand) (targetWriteResult, error) {
	writer, ok := targetWriters[command.Mode]
	if !ok {
		return targetWriteResult{}, api.BadRequestError("invalid route target mode", nil)
	}
	return writer(h, ctx, serviceID, command)
}

func writeExistingTarget(_ *Handler, ctx dao.Context, serviceID uint, command RouteTargetCommand) (targetWriteResult, error) {
	if command.BackendID == 0 || command.Backend != nil || command.FirstUpstream != nil {
		return targetWriteResult{}, api.BadRequestError("existing route target requires backend_id only", nil)
	}
	backend, err := dao.NewAdminQuery(ctx).APIBackend().LockByID(command.BackendID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return targetWriteResult{}, api.ErrorWithCode(404, "backend_not_found", "backend not found", nil)
	}
	if err != nil {
		return targetWriteResult{}, err
	}
	if backend.APIServiceID != serviceID {
		return targetWriteResult{}, api.ErrorWithCode(400, "backend_service_mismatch", "route target backend belongs to another API service", nil)
	}
	return targetWriteResult{BackendID: backend.ID}, nil
}

func writeCreatedTarget(h *Handler, ctx dao.Context, serviceID uint, command RouteTargetCommand) (targetWriteResult, error) {
	if command.BackendID != 0 || command.Backend == nil || command.FirstUpstream == nil {
		return targetWriteResult{}, api.BadRequestError("created route target requires backend and first_upstream", nil)
	}
	backend, err := api_backend.CreateInTx(ctx, serviceID, *command.Backend)
	if err != nil {
		return targetWriteResult{}, err
	}
	upstream, err := h.UpstreamCreator.CreateInTx(ctx, backend.ID, *command.FirstUpstream)
	if err != nil {
		return targetWriteResult{}, err
	}
	return targetWriteResult{BackendID: backend.ID, Upstream: &upstream}, nil
}

func parseRouteTarget(value any) (RouteTargetCommand, error) {
	command, ok := value.(RouteTargetCommand)
	if !ok {
		return RouteTargetCommand{}, fmt.Errorf("invalid route target")
	}
	if command.Mode != "existing" && command.Mode != "create" {
		return RouteTargetCommand{}, fmt.Errorf("invalid route target mode")
	}
	return command, nil
}
