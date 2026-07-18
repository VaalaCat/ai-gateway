package channel

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

type AdminChannelCreateInput struct {
	Name                string
	Status              int
	Type                int
	Key                 string
	BaseURL             string
	Models              []string
	ModelMapping        map[string]string
	Weight              uint
	Priority            int
	UseLegacyAdaptor    bool
	SupportedAPITypes   string
	Endpoints           string
	PassthroughEnabled  bool
	SystemPrompt        string
	SystemPromptInInput bool
	RoleMapping         string
	ProxyURL            string
	ParamOverride       string
	HeaderOverride      string
	Tag                 string
	Remark              string
	Setting             string
	Organization        string
	APIVersion          string
	TestModel           string
	AutoBan             int
	StatusCodeMapping   string
	OtherSettings       string
	DisableKeepalive    bool
	Resilience          models.ChannelResilience
	PriceRatio          float64
	Free                bool
	Limit               models.ChannelLimit
	Affinity            models.ChannelAffinity
}

func buildAdminChannelCreateInput(req CreateRequest) (AdminChannelCreateInput, error) {
	modelsList := splitModels(req.Models)
	mapping := map[string]string{}
	if req.ModelMapping != "" {
		if err := json.Unmarshal([]byte(req.ModelMapping), &mapping); err != nil {
			return AdminChannelCreateInput{}, api.BadRequestError("invalid model_mapping", err)
		}
	}
	input := AdminChannelCreateInput{
		Name: req.Name, Status: 1, Type: req.Type, Key: req.Key, BaseURL: req.BaseURL,
		Models: modelsList, ModelMapping: mapping, Weight: req.Weight, Priority: req.Priority,
		UseLegacyAdaptor: req.UseLegacyAdaptor, SupportedAPITypes: req.SupportedAPITypes,
		Endpoints: req.Endpoints, PassthroughEnabled: req.PassthroughEnabled,
		SystemPrompt: req.SystemPrompt, SystemPromptInInput: req.SystemPromptInInput,
		RoleMapping: req.RoleMapping, ProxyURL: req.ProxyURL, ParamOverride: req.ParamOverride,
		HeaderOverride: req.HeaderOverride, Tag: req.Tag, Remark: req.Remark, Setting: req.Setting,
		Organization: req.Organization, APIVersion: req.ApiVersion, TestModel: req.TestModel,
		AutoBan: req.AutoBan, StatusCodeMapping: req.StatusCodeMapping, OtherSettings: req.OtherSettings,
		DisableKeepalive: req.DisableKeepalive, PriceRatio: 1,
	}
	if req.Resilience != nil {
		input.Resilience = *req.Resilience
	}
	if req.PriceRatio != nil {
		input.PriceRatio = *req.PriceRatio
	}
	if req.Free != nil {
		input.Free = *req.Free
	}
	if req.Limit != nil {
		input.Limit = *req.Limit
	}
	if req.Affinity != nil {
		input.Affinity = *req.Affinity
	}
	return input, nil
}

func splitModels(raw string) []string {
	if raw == "" {
		return []string{}
	}
	return strings.Split(raw, ",")
}

func prepareAdminChannels(inputs []AdminChannelCreateInput) ([]models.Channel, error) {
	if len(inputs) == 0 {
		return nil, api.BadRequestError("channels cannot be empty", nil)
	}
	channels := make([]models.Channel, 0, len(inputs))
	for i := range inputs {
		channel, err := buildAdminChannel(inputs[i])
		if err != nil {
			return nil, api.BadRequestError(fmt.Sprintf("channel %d: %v", i, err), err)
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

func buildAdminChannel(input AdminChannelCreateInput) (models.Channel, error) {
	if input.Name == "" {
		return models.Channel{}, fmt.Errorf("name is required")
	}
	if len(input.Name) > 64 {
		return models.Channel{}, fmt.Errorf("name exceeds 64 bytes")
	}
	if input.Status != 0 && input.Status != 1 {
		return models.Channel{}, fmt.Errorf("status must be 0 or 1")
	}
	if err := input.Resilience.Validate(); err != nil {
		return models.Channel{}, err
	}
	if err := validatePriceRatio(input.PriceRatio); err != nil {
		return models.Channel{}, err
	}
	if err := input.Limit.Validate(); err != nil {
		return models.Channel{}, err
	}
	if err := input.Affinity.Validate(); err != nil {
		return models.Channel{}, err
	}
	mapping, err := json.Marshal(nonNilMap(input.ModelMapping))
	if err != nil {
		return models.Channel{}, fmt.Errorf("marshal model_mapping: %w", err)
	}
	weight := input.Weight
	if weight == 0 {
		weight = 1
	}
	return models.Channel{
		ChannelCore: models.ChannelCore{
			Name: input.Name, Status: input.Status, Type: input.Type, BaseURL: input.BaseURL,
			Weight: weight, Priority: input.Priority, SupportedAPITypes: input.SupportedAPITypes,
			Endpoints: input.Endpoints, PassthroughEnabled: input.PassthroughEnabled,
			UseLegacyAdaptor: input.UseLegacyAdaptor, Organization: input.Organization,
			ApiVersion: input.APIVersion, SystemPrompt: input.SystemPrompt,
			SystemPromptInInput: input.SystemPromptInInput, RoleMapping: input.RoleMapping,
			ParamOverride: input.ParamOverride, Setting: input.Setting, Tag: input.Tag,
			Remark: input.Remark, TestModel: input.TestModel, AutoBan: input.AutoBan,
			StatusCodeMapping: input.StatusCodeMapping, OtherSettings: input.OtherSettings,
			Affinity: datatypes.NewJSONType(input.Affinity),
		},
		Key: input.Key, Models: strings.Join(input.Models, ","), ModelMapping: string(mapping),
		ProxyURL: input.ProxyURL, HeaderOverride: input.HeaderOverride,
		DisableKeepalive: input.DisableKeepalive,
		Resilience:       datatypes.NewJSONType(input.Resilience), PriceRatio: input.PriceRatio,
		Free: input.Free, Limit: datatypes.NewJSONType(input.Limit),
	}, nil
}

func createAdminChannels(c *app.Context, inputs []AdminChannelCreateInput) ([]models.Channel, error) {
	channels, err := prepareAdminChannels(inputs)
	if err != nil {
		return nil, err
	}
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	if err := dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		m := dao.NewAdminMutation(txCtx)
		for i := range channels {
			if err := m.Channel().Create(&channels[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, api.ConflictError("create channels failed", err)
	}
	for i := range channels {
		if err := events.PublishChannelCreate(c.RequestContext(), c.GetBus(), channels[i]); err != nil && c.Logger != nil {
			// behavior change: a committed create remains successful when cache notification is delayed.
			c.Logger.Warn("publish channel.create failed after commit", zap.Uint("channel_id", channels[i].ID), zap.Error(err))
		}
	}
	return channels, nil
}

func nonNilMap(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	return value
}
