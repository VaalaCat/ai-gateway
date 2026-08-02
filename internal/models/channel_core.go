package models

import "gorm.io/datatypes"

// ChannelCore captures the scalar fields that admin Channel, BYOK
// PrivateChannel, and the master→agent SyncedPrivateChannel projection share
// verbatim. Fields whose Go type differs between admin (CSV text) and BYOK
// (strongly typed JSON) — Models / ModelMapping / Key — stay on each outer
// struct. ProxyURL / HeaderOverride are admin-only and likewise stay on
// Channel.
//
// Embedding rules:
//   - GORM expands embedded struct fields into table columns without a prefix.
//   - Outer structs may redeclare a field (e.g. PrivateChannel.Name with a
//     composite uniqueIndex) to override the embedded GORM tag. The redeclared
//     field shadows the promoted one — both for tags and for composite-literal
//     access.
//   - JSON tags on ChannelCore fields propagate via Go field promotion so the
//     external JSON shape is unchanged.
type ChannelCore struct {
	ID                  uint                                    `gorm:"primaryKey" json:"id"`
	Name                string                                  `gorm:"size:64" json:"name"`
	Type                int                                     `gorm:"index" json:"type"`
	Status              int                                     `gorm:"default:1" json:"status"`
	BaseURL             string                                  `gorm:"size:256" json:"base_url"`
	Weight              uint                                    `gorm:"default:1" json:"weight"`
	Priority            int                                     `gorm:"default:0" json:"priority"`
	SupportedAPITypes   string                                  `gorm:"type:text" json:"supported_api_types"`
	Endpoints           string                                  `gorm:"type:text" json:"endpoints"`
	PassthroughEnabled  bool                                    `gorm:"default:false" json:"passthrough_enabled"`
	UseLegacyAdaptor    bool                                    `gorm:"default:false" json:"use_legacy_adaptor"`
	Organization        string                                  `gorm:"size:128" json:"organization"`
	ApiVersion          string                                  `gorm:"size:32" json:"api_version"`
	SystemPrompt        string                                  `gorm:"type:text" json:"system_prompt"`
	SystemPromptInInput bool                                    `gorm:"default:false" json:"system_prompt_in_input"`
	RoleMapping         string                                  `gorm:"type:text" json:"role_mapping"`
	ParamOverride       string                                  `gorm:"type:text" json:"param_override"`
	Setting             string                                  `gorm:"type:text" json:"setting"`
	Tag                 string                                  `gorm:"size:64" json:"tag"`
	Remark              string                                  `gorm:"size:255" json:"remark"`
	TestModel           string                                  `gorm:"size:128" json:"test_model"`
	AutoBan             int                                     `gorm:"default:0" json:"auto_ban"`
	AutoBanState        datatypes.JSONType[ChannelDisableState] `gorm:"type:text;not null;default:'{}'" json:"auto_ban_state"`
	AutoBanRevision     uint64                                  `gorm:"default:0;not null" json:"auto_ban_revision"`
	StatusCodeMapping   string                                  `gorm:"type:text" json:"status_code_mapping"`
	OtherSettings       string                                  `gorm:"type:text" json:"other_settings"`
	CreatedAt           int64                                   `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt           int64                                   `gorm:"autoUpdateTime" json:"updated_at"`
	// Affinity 是粘性参与/ TTL 的每渠道覆盖。挂在 ChannelCore(而非 admin-only 的
	// Channel)上,因为这是唯一 admin 与 BYOK 都需要的覆盖;Resilience/Limit 仍是 admin 专属。
	Affinity datatypes.JSONType[ChannelAffinity] `gorm:"type:text" json:"affinity"`
}
