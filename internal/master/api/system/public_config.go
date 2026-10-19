package system

import (
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// PublicConfigResponse 是无需鉴权即可读取的平台公开配置/特性开关。
// 后续同类公开 flag 直接往这里加字段。
type PublicConfigResponse struct {
	RegistrationEnabled bool `json:"registration_enabled"`
	InviteEnabled       bool `json:"invite_enabled"`
	// InviteUserMaxCodes 是普通用户名下有效邀请码数量上限;0 表示禁止普通用户建码。
	// 前端据此(结合 isAdmin)决定是否展示邀请入口。
	InviteUserMaxCodes int `json:"invite_user_max_codes"`
	// Only these explicitly public text/image settings are projected here.
	SiteName       string `json:"site_name"`
	SiteTitle      string `json:"site_title"`
	SiteIconURL    string `json:"site_icon_url"`
	WelcomeTitle   string `json:"welcome_title"`
	WelcomeMessage string `json:"welcome_message"`
}

// PublicConfig 暴露登录前/登录后前端都需要的平台开关。公开端点,无鉴权。
func (h *Handler) PublicConfig(c *app.Context, _ api.EmptyRequest) (PublicConfigResponse, error) {
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	return PublicConfigResponse{
		RegistrationEnabled: q.Setting().LookupBool("registration_enabled", false),
		InviteEnabled:       q.Setting().LookupBool(consts.SettingKeyInviteEnabled, false),
		InviteUserMaxCodes:  q.Setting().LookupInt(consts.SettingKeyInviteUserMaxCodes, 5),
		SiteName:            q.Setting().LookupString("site_name", settingDefs["site_name"].Default),
		SiteTitle:           q.Setting().LookupString("site_title", settingDefs["site_title"].Default),
		SiteIconURL:         q.Setting().LookupString("site_icon_url", settingDefs["site_icon_url"].Default),
		WelcomeTitle:        q.Setting().LookupString("welcome_title", settingDefs["welcome_title"].Default),
		WelcomeMessage:      q.Setting().LookupString("welcome_message", settingDefs["welcome_message"].Default),
	}, nil
}
