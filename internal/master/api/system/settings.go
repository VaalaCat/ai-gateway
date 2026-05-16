package system

import (
	"context"
	"net/url"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

var settingDefs = map[string]struct {
	Default  string
	Validate func(string) bool
}{
	"trace_max_body_size": {
		Default: "65536",
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= 4096 && n <= 16*1024*1024
		},
	},
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

	result := make(map[string]string)
	for key, def := range settingDefs {
		result[key] = def.Default
	}
	for _, r := range records {
		if _, ok := settingDefs[r.Key]; ok {
			result[r.Key] = r.Value
		}
	}

	return SettingsResponse{Settings: result}, nil
}

type UpdateSettingsRequest struct {
	Settings map[string]string `json:"settings" binding:"required"`
}

func (h *Handler) UpdateSettings(c *app.Context, req UpdateSettingsRequest) (SettingsResponse, error) {
	for key, value := range req.Settings {
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
