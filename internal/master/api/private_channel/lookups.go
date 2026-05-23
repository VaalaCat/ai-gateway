package private_channel

import (
	"sort"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/channel"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// AvailableModelsResponse lists all model names registered in admin's ModelConfig.
// BYOK channels can only declare Models from this set (validateModelsSubsetOfModelConfigs).
type AvailableModelsResponse struct {
	Models []string `json:"models"`
}

func (h *Handler) PortalAvailableModels(c *app.Context, _ api.EmptyRequest) (AvailableModelsResponse, error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	cfgs, err := q.ModelConfig().ListAll()
	if err != nil {
		return AvailableModelsResponse{}, api.InternalError("load model configs", err)
	}
	out := make([]string, 0, len(cfgs))
	for _, mc := range cfgs {
		out = append(out, mc.ModelName)
	}
	sort.Strings(out)
	return AvailableModelsResponse{Models: out}, nil
}

// ProviderType is one supported BYOK provider option for the create UI.
type ProviderType struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	DefaultURL string `json:"default_url"`
}

type SupportedTypesResponse struct {
	Types []ProviderType `json:"types"`
}

// PortalSupportedTypes returns the supported BYOK provider types with their default URLs.
// Mirrors admin channel/types.go semantics (skip Unknown / Dummy; sort by ID for stable UI).
func (h *Handler) PortalSupportedTypes(_ *app.Context, _ api.EmptyRequest) (SupportedTypesResponse, error) {
	ids := channel.ListProviderTypes()

	out := make([]ProviderType, 0, len(ids))
	for _, id := range ids {
		name := newAPIConstant.GetChannelTypeName(id)
		out = append(out, ProviderType{
			ID:         id,
			Name:       name,
			DefaultURL: channelDefaultURLForType(id),
		})
	}
	return SupportedTypesResponse{Types: out}, nil
}

// channelDefaultURLForType reads from newAPIConstant.ChannelBaseURLs (indexed by type ID).
// Returns empty string for type IDs out of range (consistent with models.Channel.GetBaseURL).
func channelDefaultURLForType(id int) string {
	if id > 0 && id < len(newAPIConstant.ChannelBaseURLs) {
		return newAPIConstant.ChannelBaseURLs[id]
	}
	return ""
}
