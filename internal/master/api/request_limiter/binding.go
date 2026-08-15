package request_limiter

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) ListBindings(c *app.Context, req ListBindingsRequest) ([]models.LimiterBinding, error) {
	limiterID, err := strconv.ParseUint(req.LimiterID, 10, 64)
	if err != nil {
		return nil, api.BadRequestError("invalid limiter_id", err)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	bindings, err := q.LimiterBinding().ListByLimiter(uint(limiterID))
	if err != nil {
		return nil, api.InternalError("list limiter bindings failed", err)
	}
	if bindings == nil {
		bindings = []models.LimiterBinding{}
	}
	return bindings, nil
}

func (h *Handler) CreateBinding(c *app.Context, req CreateBindingRequest) (api.Created[models.LimiterBinding], error) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	var binding models.LimiterBinding
	err := dao.RunInCoreTx[dao.Context](daoCtx, func(txCtx dao.Context) error {
		limiter, err := dao.NewAdminQuery(txCtx).RequestLimiter().GetByID(req.LimiterID)
		if err != nil {
			return api.BadRequestError(fmt.Sprintf("request limiter %d not found", req.LimiterID), err)
		}
		targetID, err := validateCreateBinding(*limiter, req)
		if err != nil {
			return err
		}
		binding = models.LimiterBinding{
			LimiterID: req.LimiterID, TargetType: req.TargetType, TargetID: targetID, Enabled: req.Enabled,
		}
		if err := dao.NewAdminMutation(txCtx).LimiterBinding().Create(&binding); err != nil {
			return api.ConflictError("binding already exists or creation failed: "+err.Error(), err)
		}
		return nil
	})
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return api.Created[models.LimiterBinding]{}, apiErr
		}
		return api.Created[models.LimiterBinding]{}, api.InternalError("create limiter binding failed", err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.LimiterBindingCreateTopic, binding)
	return api.Created[models.LimiterBinding]{Value: binding}, nil
}

func validateCreateBinding(limiter models.RequestLimiter, req CreateBindingRequest) (uint, error) {
	valid := models.ValidBindingTarget(limiter.KeyBy, req.TargetType)
	if models.IsAPILimiterTarget(req.TargetType) {
		valid = models.ValidAPILimiterBinding(limiter, req.TargetType)
	}
	if !valid {
		return 0, api.BadRequestError(
			fmt.Sprintf("limiter key_by=%s cannot bind target_type=%s", limiter.KeyBy, req.TargetType), nil)
	}
	if req.TargetType == models.LimiterTargetGlobal {
		return 0, nil
	}
	if req.TargetID == 0 {
		return 0, api.BadRequestError("target_id is required for non-global binding", nil)
	}
	return req.TargetID, nil
}

func (h *Handler) DeleteBinding(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()

	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError(consts.ErrNotFound, err)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	m := dao.NewAdminMutation(daoCtx)

	if err := m.LimiterBinding().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete limiter binding failed", err)
	}

	// 下游 LimiterIndex.DeleteBinding 只按 ID 摘除，故 delete 事件只需带上 ID。
	_ = events.Publish(context.Background(), c.GetBus(), events.LimiterBindingDeleteTopic, models.LimiterBinding{ID: uint(id)})
	return api.StatusResponse{Status: "deleted"}, nil
}
