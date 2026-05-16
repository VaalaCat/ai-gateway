package model_routing

import (
	"context"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

// Handler 是 model-routings 的 admin 路由处理器。
type Handler struct {
	Bus app.EventBus
}

// validateErrorToAPI 把 dao.ValidateError 转成 api.APIError（含 code/details，
// 由 DefaultErrorMapper.Map 渲染成 spec §4.2 结构化响应）。
func validateErrorToAPI(ve *dao.ValidateError) error {
	status := http.StatusBadRequest
	switch ve.Code() {
	case dao.ErrCodeReferencedBy:
		status = http.StatusConflict
	case dao.ErrCodeNotFound:
		status = http.StatusNotFound
	}
	return api.ErrorWithCode(status, ve.Code(), ve.Error(), ve.Details())
}

// publishEvent 发送 ModelRouting 事件到 EventBus。
func (h *Handler) publishEvent(action string, r *models.ModelRouting) {
	if h.Bus == nil {
		return
	}
	ctx := context.Background()
	switch action {
	case events.ActionCreate:
		_ = events.Publish(ctx, h.Bus, events.ModelRoutingCreateTopic, *r)
	case events.ActionUpdate:
		_ = events.Publish(ctx, h.Bus, events.ModelRoutingUpdateTopic, *r)
	case events.ActionDelete:
		_ = events.Publish(ctx, h.Bus, events.ModelRoutingDeleteTopic, *r)
	}
}
