package private_channel

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// CreateRequest length caps mirror the gorm `size:N` tags on ChannelCore
// (internal/models/channel_core.go). Free-form text columns (`type:text`) get
// a defensive max=4096 to keep request bodies bounded; matching DB columns are
// effectively unlimited but a single channel record should never carry 4KB of
// JSON snippets. Patch path enforces the same caps via validateFieldLengthsCtx.
type CreateRequest struct {
	Name              string            `json:"name" binding:"required,max=64"`
	Type              int               `json:"type" binding:"required"`
	Key               string            `json:"key" binding:"required,max=4096"`
	BaseURL           string            `json:"base_url" binding:"omitempty,max=256"`
	Models            []string          `json:"models" binding:"required,min=1"`
	ModelMapping      map[string]string `json:"model_mapping"`
	Weight            uint              `json:"weight"`
	Priority          int               `json:"priority"`
	SupportedAPITypes string            `json:"supported_api_types" binding:"omitempty,max=4096"`
	Endpoints         string            `json:"endpoints" binding:"omitempty,max=4096"`
	Organization      string            `json:"organization" binding:"omitempty,max=128"`
	ApiVersion        string            `json:"api_version" binding:"omitempty,max=32"`
	SystemPrompt      string            `json:"system_prompt" binding:"omitempty,max=4096"`
	RoleMapping       string            `json:"role_mapping" binding:"omitempty,max=4096"`
	ParamOverride     string            `json:"param_override" binding:"omitempty,max=4096"`
	Setting           string            `json:"setting" binding:"omitempty,max=4096"`
	Tag               string            `json:"tag" binding:"omitempty,max=64"`
	Remark            string            `json:"remark" binding:"omitempty,max=255"`
	TestModel         string            `json:"test_model" binding:"omitempty,max=128"`
	AutoBan           int               `json:"auto_ban"`
	StatusCodeMapping string            `json:"status_code_mapping" binding:"omitempty,max=4096"`
	OtherSettings     string            `json:"other_settings" binding:"omitempty,max=4096"`
	PassthroughEnabled  bool `json:"passthrough_enabled"`
	UseLegacyAdaptor    bool `json:"use_legacy_adaptor"`
	SystemPromptInInput bool `json:"system_prompt_in_input"`
}

// UpdateRequest carries patch fields (decoded via BindURIAndBodyMap in router wiring later).
type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fs map[string]any) { r.Fields = fs }

type UpdateKeyRequest struct {
	ID  string `uri:"id" binding:"required"`
	Key string `json:"key" binding:"required"`
}

type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
	Type   string `form:"type"`
	Status string `form:"status"`
}

// DetailResponse intentionally never exposes plaintext Key — only last4.
type DetailResponse struct {
	ID                  uint              `json:"id"`
	OwnerID             uint              `json:"owner_id"`
	Name                string            `json:"name"`
	Type                int               `json:"type"`
	KeyLast4            string            `json:"key_last4"`
	BaseURL             string            `json:"base_url"`
	Models              []string          `json:"models"`
	ModelMapping        map[string]string `json:"model_mapping"`
	Weight              uint              `json:"weight"`
	Priority            int               `json:"priority"`
	Status              int               `json:"status"`
	SupportedAPITypes   string            `json:"supported_api_types"`
	Endpoints           string            `json:"endpoints"`
	PassthroughEnabled  bool              `json:"passthrough_enabled"`
	UseLegacyAdaptor    bool              `json:"use_legacy_adaptor"`
	Organization        string            `json:"organization"`
	ApiVersion          string            `json:"api_version"`
	SystemPrompt        string            `json:"system_prompt"`
	SystemPromptInInput bool              `json:"system_prompt_in_input"`
	RoleMapping         string            `json:"role_mapping"`
	ParamOverride       string            `json:"param_override"`
	Setting             string            `json:"setting"`
	Tag                 string            `json:"tag"`
	Remark              string            `json:"remark"`
	TestModel           string            `json:"test_model"`
	AutoBan             int               `json:"auto_ban"`
	StatusCodeMapping   string            `json:"status_code_mapping"`
	OtherSettings       string            `json:"other_settings"`
	CreatedAt           int64             `json:"created_at"`
	UpdatedAt           int64             `json:"updated_at"`
}

func toDetailResponse(pc *models.PrivateChannel) DetailResponse {
	return DetailResponse{
		ID:                  pc.ID,
		OwnerID:             pc.OwnerID,
		Name:                pc.Name,
		Type:                pc.Type,
		KeyLast4:            pc.KeyLast4,
		BaseURL:             pc.BaseURL,
		Models:              []string(pc.Models),
		ModelMapping:        pc.ModelMapping.Data(),
		Weight:              pc.Weight,
		Priority:            pc.Priority,
		Status:              pc.Status,
		SupportedAPITypes:   pc.SupportedAPITypes,
		Endpoints:           pc.Endpoints,
		PassthroughEnabled:  pc.PassthroughEnabled,
		UseLegacyAdaptor:    pc.UseLegacyAdaptor,
		Organization:        pc.Organization,
		ApiVersion:          pc.ApiVersion,
		SystemPrompt:        pc.SystemPrompt,
		SystemPromptInInput: pc.SystemPromptInInput,
		RoleMapping:         pc.RoleMapping,
		ParamOverride:       pc.ParamOverride,
		Setting:             pc.Setting,
		Tag:                 pc.Tag,
		Remark:              pc.Remark,
		TestModel:           pc.TestModel,
		AutoBan:             pc.AutoBan,
		StatusCodeMapping:   pc.StatusCodeMapping,
		OtherSettings:       pc.OtherSettings,
		CreatedAt:           pc.CreatedAt,
		UpdatedAt:           pc.UpdatedAt,
	}
}
