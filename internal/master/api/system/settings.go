package system

import (
	"context"
	"encoding/json"
	"maps"
	"net/url"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// settingDefs 注册 master-only setting(不需要同步到 agent)。
// 需要同步到 agent 的 setting 走 internal/settings.AgentSettings,
// 通过 settings.Defaults / settings.Validate 入这里 union 起来。
var settingDefs = map[string]struct {
	Default  string
	Validate func(string) bool
}{
	"registration_enabled": {
		Default: "false",
		Validate: func(v string) bool {
			return v == "true" || v == "false"
		},
	},
	"proxy_url": {
		Default: "",
		Validate: func(v string) bool {
			if v == "" {
				return true
			}
			u, err := url.Parse(v)
			return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
		},
	},
	"oauth_auto_create": {
		Default: "false",
		Validate: func(v string) bool {
			return v == "true" || v == "false"
		},
	},
	consts.SettingKeyBYOKEnabled: {
		Default: consts.BYOKDefaultEnabledStr,
		Validate: func(v string) bool {
			return v == "true" || v == "false"
		},
	},
	consts.SettingKeyBYOKMaxChannelsPerUser: {
		Default: consts.BYOKDefaultMaxChannelsPerUserStr,
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			// 0 表示 quota 禁用；上限给个不致命的 sanity 值防误填。
			return err == nil && n >= 0 && n <= 10000
		},
	},
	consts.SettingKeyBYOKBillingMode: {
		Default: consts.BYOKDefaultBillingMode,
		Validate: func(v string) bool {
			return v == consts.BYOKBillingModeFree || v == consts.BYOKBillingModeServiceFee
		},
	},
	consts.SettingKeyBYOKServiceFeeRatio: {
		Default: consts.BYOKDefaultServiceFeeRatioStr,
		Validate: func(v string) bool {
			f, err := strconv.ParseFloat(v, 64)
			return err == nil && f >= 0 && f <= 1
		},
	},
	consts.SettingKeyBYOKBaseURLAllowlist: {
		Default: consts.BYOKDefaultBaseURLAllowlistStr,
		Validate: func(v string) bool {
			// 仅 admin 自定义部分；系统内置走 consts.SystemBYOKBaseURLs。
			// 接受空串、合法 JSON 字符串数组。
			if v == "" {
				return true
			}
			var arr []string
			return json.Unmarshal([]byte(v), &arr) == nil
		},
	},
}

type SettingsResponse struct {
	Settings map[string]string `json:"settings"`
}

type GetSettingsRequest struct{}

func (h *Handler) GetSettings(c *app.Context, _ GetSettingsRequest) (SettingsResponse, error) {
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	records, err := q.Setting().GetAll()
	if err != nil {
		return SettingsResponse{}, api.InternalError("get settings failed", err)
	}

	agentDefaults := settings.Defaults()
	result := make(map[string]string)
	for key, def := range settingDefs {
		result[key] = def.Default
	}
	maps.Copy(result, agentDefaults)
	for _, r := range records {
		if _, ok := settingDefs[r.Key]; ok {
			result[r.Key] = r.Value
			continue
		}
		if _, ok := agentDefaults[r.Key]; ok {
			result[r.Key] = r.Value
		}
	}

	return SettingsResponse{Settings: result}, nil
}

type UpdateSettingsRequest struct {
	Settings map[string]string `json:"settings" binding:"required"`
}

func (h *Handler) UpdateSettings(c *app.Context, req UpdateSettingsRequest) (SettingsResponse, error) {
	agentKeys := settings.Defaults()
	for key, value := range req.Settings {
		if _, ok := agentKeys[key]; ok {
			if err := settings.Validate(key, value); err != nil {
				return SettingsResponse{}, api.BadRequestError(err.Error(), nil)
			}
			continue
		}
		def, ok := settingDefs[key]
		if !ok {
			return SettingsResponse{}, api.BadRequestError("unknown setting: "+key, nil)
		}
		if !def.Validate(value) {
			return SettingsResponse{}, api.BadRequestError("invalid value for "+key, nil)
		}
	}

	m := dao.NewAdminMutation(dao.NewContext(c.App))
	for key, value := range req.Settings {
		if err := m.Setting().Set(key, value); err != nil {
			return SettingsResponse{}, api.InternalError("save setting failed", err)
		}

		if err := events.PublishSettingUpdate(context.Background(), c.GetBus(), models.Setting{Key: key, Value: value}); err != nil {
			return SettingsResponse{}, api.InternalError("publish setting.update failed", err)
		}
	}

	return h.GetSettings(c, GetSettingsRequest{})
}
