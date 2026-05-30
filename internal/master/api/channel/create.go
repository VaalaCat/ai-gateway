package channel

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"gorm.io/datatypes"
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
	if req.Resilience != nil {
		if err := req.Resilience.Validate(); err != nil {
			return api.Created[models.Channel]{}, api.BadRequestError(err.Error(), err)
		}
		channel.Resilience = datatypes.NewJSONType(*req.Resilience)
	}
	channel.PriceRatio = 1
	if req.PriceRatio != nil {
		if err := validatePriceRatio(*req.PriceRatio); err != nil {
			return api.Created[models.Channel]{}, api.BadRequestError(err.Error(), err)
		}
		channel.PriceRatio = *req.PriceRatio
	}
	if req.Free != nil {
		channel.Free = *req.Free
	}
	if req.Limit != nil {
		if err := req.Limit.Validate(); err != nil {
			return api.Created[models.Channel]{}, api.BadRequestError(err.Error(), err)
		}
		channel.Limit = datatypes.NewJSONType(*req.Limit)
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
