package script

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/dop251/goja"
	"gorm.io/datatypes"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.AdminScript], error) {
	if _, err := goja.Compile(req.Name, req.Code, true); err != nil {
		return api.Created[models.AdminScript]{}, api.BadRequestError("script compile error: "+err.Error(), err)
	}
	// 未显式传 enabled 时默认启用；显式 false 必须被尊重（见 dao 创建停用脚本测试）。
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	s := models.AdminScript{
		Name:     req.Name,
		Code:     req.Code,
		Enabled:  enabled,
		Priority: req.Priority,
		Scope:    datatypes.NewJSONType(req.Scope),
	}
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	if err := dao.NewAdminMutation(daoCtx).AdminScript().Create(&s); err != nil {
		return api.Created[models.AdminScript]{}, api.ConflictError(err.Error(), err)
	}
	if err := events.PublishScriptCreate(context.Background(), c.GetBus(), s); err != nil {
		return api.Created[models.AdminScript]{}, api.InternalError("publish script.create failed", err)
	}
	return api.Created[models.AdminScript]{Value: s}, nil
}
