package models

import newAPIConstant "github.com/QuantumNous/new-api/constant"

type Channel struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Name         string `gorm:"size:64" json:"name"`
	Type         int    `gorm:"index" json:"type"`
	Key          string `gorm:"type:text" json:"key"`
	BaseURL      string `gorm:"size:256" json:"base_url"`
	Models       string `gorm:"type:text" json:"models"`
	ModelMapping string `gorm:"type:text" json:"model_mapping"`
	Weight       uint   `gorm:"default:1" json:"weight"`
	Priority     int    `gorm:"default:0" json:"priority"`
	Status       int    `gorm:"default:1" json:"status"`
	Tag          string `gorm:"size:64;index" json:"tag"`
	Remark       string `gorm:"size:255" json:"remark"`

	// Native protocol layer configuration
	SupportedAPITypes  string `gorm:"type:text" json:"supported_api_types"`
	Endpoints          string `gorm:"type:text" json:"endpoints"`
	PassthroughEnabled bool   `gorm:"default:false" json:"passthrough_enabled"`
	UseLegacyAdaptor   bool   `gorm:"default:false" json:"use_legacy_adaptor"`
	SystemPrompt       string `gorm:"type:text" json:"system_prompt"`
	RoleMapping        string `gorm:"type:text" json:"role_mapping"`
	SystemPromptInInput bool  `gorm:"default:false" json:"system_prompt_in_input"`
	ProxyURL           string `gorm:"size:256" json:"proxy_url"`
	ParamOverride      string `gorm:"type:text" json:"param_override"`
	HeaderOverride     string `gorm:"type:text" json:"header_override"`

	// Legacy: new-api specific fields (remove together when removing new-api)
	Setting           string `gorm:"type:text" json:"setting"`
	Organization      string `gorm:"size:128" json:"organization"`
	ApiVersion        string `gorm:"size:32" json:"api_version"`
	TestModel         string `gorm:"size:128" json:"test_model"`
	AutoBan           int    `gorm:"default:0" json:"auto_ban"`
	StatusCodeMapping string `gorm:"type:text" json:"status_code_mapping"`
	OtherSettings     string `gorm:"type:text" json:"other_settings"`

	CreatedAt int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64 `gorm:"autoUpdateTime" json:"updated_at"`
}

// GetBaseURL returns the channel's base URL, falling back to the default
// from ChannelBaseURLs when not explicitly set. This matches the behavior
// of new-api's Channel.GetBaseURL().
func (ch *Channel) GetBaseURL() string {
	if ch.BaseURL != "" {
		return ch.BaseURL
	}
	if ch.Type > 0 && ch.Type < len(newAPIConstant.ChannelBaseURLs) {
		return newAPIConstant.ChannelBaseURLs[ch.Type]
	}
	return ""
}
