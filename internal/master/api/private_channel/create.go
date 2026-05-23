package private_channel

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[DetailResponse], error) {
	if c.UserInfo == nil {
		return api.Created[DetailResponse]{}, api.UnauthorizedError("not authenticated")
	}
	daoQ := dao.NewAdminQuery(dao.NewContext(c.App))
	if err := RunValidators(ValidatorCtx{
		Query:   daoQ,
		Req:     createRequestToMap(&req),
		Dirty:   nil, // Create path: all fields dirty
		OwnerID: c.UserInfo.UserID,
		GroupID: c.UserInfo.GroupID,
	}); err != nil {
		return api.Created[DetailResponse]{}, err
	}

	cipher := h.Provider.GetCipher()
	if cipher == nil {
		return api.Created[DetailResponse]{}, api.InternalError("byok cipher not configured", nil)
	}
	ct, err := cipher.Seal(req.Key, c.UserInfo.UserID)
	if err != nil {
		return api.Created[DetailResponse]{}, api.InternalError("seal key", err)
	}

	pc := models.PrivateChannel{
		ChannelCore: models.ChannelCore{
			Type:                req.Type,
			BaseURL:             req.BaseURL,
			Weight:              nonZeroWeight(req.Weight),
			Priority:            req.Priority,
			SupportedAPITypes:   req.SupportedAPITypes,
			Endpoints:           req.Endpoints,
			PassthroughEnabled:  req.PassthroughEnabled,
			UseLegacyAdaptor:    req.UseLegacyAdaptor,
			Organization:        req.Organization,
			ApiVersion:          req.ApiVersion,
			SystemPrompt:        req.SystemPrompt,
			SystemPromptInInput: req.SystemPromptInInput,
			RoleMapping:         req.RoleMapping,
			ParamOverride:       req.ParamOverride,
			Setting:             req.Setting,
			Tag:                 req.Tag,
			Remark:              req.Remark,
			TestModel:           req.TestModel,
			AutoBan:             req.AutoBan,
			StatusCodeMapping:   req.StatusCodeMapping,
			OtherSettings:       req.OtherSettings,
		},
		OwnerID:      c.UserInfo.UserID,
		KeyCipher:    ct,
		KeyLast4:     byokcrypto.Last4(req.Key),
		Models:       datatypes.JSONSlice[string](req.Models),
		ModelMapping: datatypes.NewJSONType(req.ModelMapping),
		Name:         req.Name,
		Status:       1,
	}

	daoCtx := dao.NewContext(c.App)
	m := dao.NewAdminMutation(daoCtx)
	if err := m.PrivateChannel().Create(&pc); err != nil {
		return api.Created[DetailResponse]{}, api.ConflictError(err.Error(), err)
	}

	// Publish invalidate so agents drop stale visiblePrivateChannels cache.
	q := dao.NewAdminQuery(daoCtx)
	if err := sync.PublishPrivateChannelMutation(context.Background(), q, c.GetBus(), pc.ID, pc.OwnerID); err != nil {
		if c.Logger != nil {
			c.Logger.Warn("publish private_channel invalidate failed",
				zap.Uint("channel_id", pc.ID), zap.Error(err))
		}
	}

	return api.Created[DetailResponse]{Value: toDetailResponse(&pc)}, nil
}

func nonZeroWeight(w uint) uint {
	if w == 0 {
		return 1
	}
	return w
}
