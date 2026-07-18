package request_limiter

import (
	"context"
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
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	limiter, err := q.RequestLimiter().GetByID(req.LimiterID)
	if err != nil {
		return api.Created[models.LimiterBinding]{}, api.BadRequestError(fmt.Sprintf("request limiter %d not found", req.LimiterID), err)
	}

	// §5.1 硬约束：KeyBy 决定可绑 TargetType，非法组合直接拒绝。
	if !models.ValidBindingTarget(limiter.KeyBy, req.TargetType) {
		return api.Created[models.LimiterBinding]{}, api.BadRequestError(
			fmt.Sprintf("limiter key_by=%s cannot bind target_type=%s", limiter.KeyBy, req.TargetType), nil)
	}

	// global 必为 0，其余 target 必须给 target_id。
	targetID := req.TargetID
	if req.TargetType == models.LimiterTargetGlobal {
		targetID = 0
	} else if targetID == 0 {
		return api.Created[models.LimiterBinding]{}, api.BadRequestError("target_id is required for non-global binding", nil)
	}

	binding := models.LimiterBinding{
		LimiterID:  req.LimiterID,
		TargetType: req.TargetType,
		TargetID:   targetID,
		Enabled:    req.Enabled,
	}
	if err := m.LimiterBinding().Create(&binding); err != nil {
		return api.Created[models.LimiterBinding]{}, api.ConflictError("binding already exists or creation failed: "+err.Error(), err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.LimiterBindingCreateTopic, binding)
	return api.Created[models.LimiterBinding]{Value: binding}, nil
}

func (h *Handler) DeleteBinding(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
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
