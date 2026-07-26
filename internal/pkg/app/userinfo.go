package app

import "github.com/VaalaCat/ai-gateway/internal/models"

// UserInfo 承载认证用户身份信息，在应用内流转。
type UserInfo struct {
	UserID            uint
	Username          string
	DisplayName       string // JWT claim ClaimDisplayName,SSO 注册时由 IdP 同步,可空
	AvatarURL         string // JWT claim ClaimAvatarURL,SSO 注册时由 IdP 同步,可空
	Role              int
	TokenID           uint // API Token 认证时填充，JWT 登录时为 0
	TokenName         string
	TraceEnabled      bool                  // Agent 侧：Token 是否开启 trace
	TraceMode         models.TokenTraceMode // Agent 侧：Token 成功请求的 trace 捕获范围
	BYOKOnly          bool                  // Agent 侧：Token 是否限定只用 BYOK/私有渠道
	TokenModels       []string              // Agent 侧：Token 允许的模型列表（空表示不限）
	AllowedChannelIDs []uint                // Agent 侧：Token 允许的频道列表（空表示不限）

	// 新增：来自该用户所在 Group
	GroupID                uint
	GroupModels            []string
	GroupAllowedChannelIDs []uint
}
