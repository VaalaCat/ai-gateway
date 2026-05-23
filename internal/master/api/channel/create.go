package channel

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.Channel], error) {
	channel := models.Channel{
		ChannelCore: models.ChannelCore{
			Name: req.Name, Type: req.Type, BaseURL: req.BaseURL,
			Weight:   req.Weight,
			Priority: req.Priority, Status: 1, UseLegacyAdaptor: req.UseLegacyAdaptor,
			SupportedAPITypes:  req.SupportedAPITypes,
			Endpoints:          req.Endpoints,
			PassthroughEnabled: req.PassthroughEnabled,
			SystemPrompt:       req.SystemPrompt,
			ParamOverride:      req.ParamOverride,
			Remark:             req.Remark,
			Setting:            req.Setting, Organization: req.Organization, ApiVersion: req.ApiVersion,
			TestModel: req.TestModel, AutoBan: req.AutoBan,
			StatusCodeMapping: req.StatusCodeMapping, OtherSettings: req.OtherSettings,
		},
		Key:            req.Key,
		Models:         req.Models,
		ModelMapping:   req.ModelMapping,
		ProxyURL:       req.ProxyURL,
		HeaderOverride: req.HeaderOverride,
		Tag:            req.Tag,
	}
	if channel.Weight == 0 {
		channel.Weight = 1
	}

	daoCtx := dao.NewContext(c.App)
	m := dao.NewAdminMutation(daoCtx)

	if err := m.Channel().Create(&channel); err != nil {
		return api.Created[models.Channel]{}, api.ConflictError(err.Error(), err)
	}
	if err := events.PublishChannelCreate(context.Background(), c.GetBus(), channel); err != nil {
		return api.Created[models.Channel]{}, api.InternalError("publish channel.create failed", err)
	}
	return api.Created[models.Channel]{Value: channel}, nil
}
