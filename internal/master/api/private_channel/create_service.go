package private_channel

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	mastersync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

type PrivateChannelOwner struct {
	UserID  uint
	GroupID uint
}

type PrivateChannelCreateInput struct {
	Name                string
	Status              int
	Type                int
	Key                 string
	BaseURL             string
	Models              []string
	ModelMapping        map[string]string
	Weight              uint
	Priority            int
	SupportedAPITypes   string
	Endpoints           string
	Organization        string
	APIVersion          string
	SystemPrompt        string
	RoleMapping         string
	ParamOverride       string
	Setting             string
	Tag                 string
	Remark              string
	TestModel           string
	AutoBan             int
	StatusCodeMapping   string
	OtherSettings       string
	PassthroughEnabled  bool
	UseLegacyAdaptor    bool
	SystemPromptInInput bool
	Affinity            models.ChannelAffinity
}

func buildPrivateChannelCreateInput(req CreateRequest) PrivateChannelCreateInput {
	input := PrivateChannelCreateInput{
		Name: req.Name, Status: 1, Type: req.Type, Key: req.Key, BaseURL: req.BaseURL,
		Models: append([]string(nil), req.Models...), ModelMapping: cloneMapping(req.ModelMapping),
		Weight: req.Weight, Priority: req.Priority, SupportedAPITypes: req.SupportedAPITypes,
		Endpoints: req.Endpoints, Organization: req.Organization, APIVersion: req.ApiVersion,
		SystemPrompt: req.SystemPrompt, RoleMapping: req.RoleMapping, ParamOverride: req.ParamOverride,
		Setting: req.Setting, Tag: req.Tag, Remark: req.Remark, TestModel: req.TestModel,
		AutoBan: req.AutoBan, StatusCodeMapping: req.StatusCodeMapping, OtherSettings: req.OtherSettings,
		PassthroughEnabled: req.PassthroughEnabled, UseLegacyAdaptor: req.UseLegacyAdaptor,
		SystemPromptInInput: req.SystemPromptInInput,
	}
	if req.Affinity != nil {
		input.Affinity = *req.Affinity
	}
	return input
}

func createPrivateChannels(
	c *app.Context,
	provider byokcrypto.Provider,
	owner PrivateChannelOwner,
	inputs []PrivateChannelCreateInput,
) ([]models.PrivateChannel, error) {
	if owner.UserID == 0 {
		return nil, api.UnauthorizedError("not authenticated")
	}
	if len(inputs) == 0 {
		return nil, api.BadRequestError("channels cannot be empty", nil)
	}
	cipher := provider.GetCipher()
	if cipher == nil {
		return nil, api.InternalError("byok cipher not configured", nil)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	rows := make([]models.PrivateChannel, 0, len(inputs))
	seenNames := make(map[string]struct{}, len(inputs))
	for i := range inputs {
		input := inputs[i]
		if _, duplicate := seenNames[input.Name]; duplicate {
			return nil, api.ConflictError("duplicate name in batch: "+input.Name, nil)
		}
		seenNames[input.Name] = struct{}{}
		if err := validatePrivateChannelInput(q, owner, input, len(inputs), i > 0); err != nil {
			return nil, err
		}
		sealed, err := cipher.Seal(input.Key, owner.UserID)
		if err != nil {
			return nil, api.InternalError("seal key", err)
		}
		rows = append(rows, buildPrivateChannel(owner.UserID, input, sealed))
	}

	if err := dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		m := dao.NewAdminMutation(txCtx)
		for i := range rows {
			if err := m.PrivateChannel().Create(&rows[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, api.ConflictError("create private channels failed", err)
	}

	committedQ := dao.NewAdminQuery(daoCtx)
	for i := range rows {
		if err := mastersync.PublishPrivateChannelMutation(
			c.RequestContext(), committedQ, c.GetBus(), rows[i].ID, owner.UserID,
		); err != nil && c.Logger != nil {
			c.Logger.Warn("publish private_channel invalidate failed after commit",
				zap.Uint("channel_id", rows[i].ID), zap.Error(err))
		}
	}
	return rows, nil
}

func validatePrivateChannelInput(
	q dao.AdminQuery,
	owner PrivateChannelOwner,
	input PrivateChannelCreateInput,
	createCount int,
	skipQuota bool,
) error {
	if input.Name == "" {
		return api.BadRequestError("name is required", nil)
	}
	if input.Type == 0 {
		return api.BadRequestError("type is required", nil)
	}
	if input.Key == "" {
		return api.BadRequestError("key is required", nil)
	}
	if len(input.Models) == 0 {
		return api.BadRequestError("models must not be empty", nil)
	}
	if input.Status != 0 && input.Status != 1 {
		return api.BadRequestError("status must be 0 or 1", nil)
	}
	if err := input.Affinity.Validate(); err != nil {
		return api.BadRequestError(err.Error(), err)
	}
	return RunValidators(ValidatorCtx{
		Query: q, Req: privateInputToMap(input), Dirty: nil,
		OwnerID: owner.UserID, GroupID: owner.GroupID,
		CreateCount: createCount, SkipCreateQuota: skipQuota,
	})
}

func privateInputToMap(input PrivateChannelCreateInput) map[string]any {
	return map[string]any{
		"name": input.Name, "key": input.Key, "base_url": input.BaseURL,
		"supported_api_types": input.SupportedAPITypes, "endpoints": input.Endpoints,
		"organization": input.Organization, "api_version": input.APIVersion,
		"system_prompt": input.SystemPrompt, "role_mapping": input.RoleMapping,
		"param_override": input.ParamOverride, "setting": input.Setting,
		"tag": input.Tag, "remark": input.Remark, "test_model": input.TestModel,
		"status_code_mapping": input.StatusCodeMapping, "other_settings": input.OtherSettings,
		"models": input.Models, "model_mapping": input.ModelMapping,
		"weight": input.Weight, "priority": input.Priority,
	}
}

func buildPrivateChannel(ownerID uint, input PrivateChannelCreateInput, sealed []byte) models.PrivateChannel {
	return models.PrivateChannel{
		ChannelCore: models.ChannelCore{
			Type: input.Type, BaseURL: input.BaseURL, Weight: nonZeroWeight(input.Weight),
			Priority: input.Priority, SupportedAPITypes: input.SupportedAPITypes,
			Endpoints: input.Endpoints, PassthroughEnabled: input.PassthroughEnabled,
			UseLegacyAdaptor: input.UseLegacyAdaptor, Organization: input.Organization,
			ApiVersion: input.APIVersion, SystemPrompt: input.SystemPrompt,
			SystemPromptInInput: input.SystemPromptInInput, RoleMapping: input.RoleMapping,
			ParamOverride: input.ParamOverride, Setting: input.Setting, Tag: input.Tag,
			Remark: input.Remark, TestModel: input.TestModel, AutoBan: input.AutoBan,
			StatusCodeMapping: input.StatusCodeMapping, OtherSettings: input.OtherSettings,
			Affinity: datatypes.NewJSONType(input.Affinity),
		},
		OwnerID: ownerID, KeyCipher: sealed, KeyLast4: byokcrypto.Last4(input.Key),
		Models:       datatypes.JSONSlice[string](append([]string(nil), input.Models...)),
		ModelMapping: datatypes.NewJSONType(cloneMapping(input.ModelMapping)),
		Name:         input.Name, Status: input.Status,
	}
}

func cloneMapping(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(value))
	for key, mapped := range value {
		result[key] = mapped
	}
	return result
}

func nonZeroWeight(w uint) uint {
	if w == 0 {
		return 1
	}
	return w
}
