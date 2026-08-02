package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"go.uber.org/zap"
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
	consts.SettingKeyTokenModelWhitelistSelfService: {
		Default: "false",
		Validate: func(v string) bool {
			return v == "true" || v == "false"
		},
	},
	consts.SettingKeyModelMarketplaceEnabled: {
		Default: strconv.FormatBool(consts.ModelMarketplaceDefaultEnabled),
		Validate: func(v string) bool {
			return v == "true" || v == "false"
		},
	},
	consts.SettingKeyModelMarketplaceMinSamples: {
		Default: strconv.Itoa(consts.ModelMarketplaceDefaultMinSamples),
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= consts.ModelMarketplaceMinimumSamples && n <= consts.ModelMarketplaceMaximumSamples
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
	"pricing_source_priority": {
		Default: "models.dev,basellm",
		Validate: func(v string) bool {
			if v == "" {
				return false
			}
			for _, p := range strings.Split(v, ",") {
				if strings.TrimSpace(p) == "" {
					return false
				}
			}
			return true
		},
	},
	"pricing_disagreement_threshold": {
		Default: "0.2",
		Validate: func(v string) bool {
			f, err := strconv.ParseFloat(v, 64)
			return err == nil && f >= 0 && f <= 1
		},
	},
	"oauth_auto_create": {
		Default: "false",
		Validate: func(v string) bool {
			return v == "true" || v == "false"
		},
	},
	consts.SettingKeyInviteEnabled: {
		Default: "false",
		Validate: func(v string) bool {
			return v == "true" || v == "false"
		},
	},
	consts.SettingKeyInviteUserMaxCodes: {
		Default: "5",
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			// 0 表示禁止普通用户建码;上限给个 sanity 值。
			return err == nil && n >= 0 && n <= 10000
		},
	},
	consts.SettingKeyInviteUserMaxUses: {
		Default: "1",
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= 1 && n <= 10000
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
	consts.SettingAgentConnectivityProbeSuccessTTLSeconds: {
		Default: "300",
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= 30 && n <= 3600
		},
	},
	consts.SettingAgentConnectivityProbeFailureRetryMinSeconds: {
		Default: "30",
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= 5 && n <= 300
		},
	},
	consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds: {
		Default: "300",
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= 5 && n <= 3600
		},
	},
	consts.SettingKeyRebuildSliceSleepMs: {
		Default: "1000",
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= 0 && n <= 60000
		},
	},
	consts.SettingKeyBillingLogRetentionDays: {
		Default: strconv.Itoa(consts.DefaultBillingLogRetentionDays),
		Validate: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= consts.MinimumBillingLogRetentionDays && n <= consts.MaximumBillingLogRetentionDays
		},
	},
}

func init() {
	for key, definition := range masterlogqueue.DeliverySettingDefinitions() {
		definition := definition
		settingDefs[key] = struct {
			Default  string
			Validate func(string) bool
		}{
			Default: definition.Default,
			Validate: func(value string) bool {
				n, err := strconv.ParseInt(value, 10, 64)
				return err == nil && n >= definition.Min && n <= definition.Max
			},
		}
	}
}

type SettingsResponse struct {
	Settings map[string]string `json:"settings"`
}

type GetSettingsRequest struct{}

// SeedMasterSettingsSnapshot loads the master-only settings registry once at
// startup. AgentSettings are intentionally excluded from this snapshot.
func SeedMasterSettingsSnapshot(application app.Application) error {
	if application == nil || application.GetCoreDB() == nil {
		return errors.New("master settings snapshot requires core database")
	}
	var records []models.Setting
	if err := application.GetCoreDB().Find(&records).Error; err != nil {
		return fmt.Errorf("load master settings: %w", err)
	}
	application.GetMasterSettings().Replace(masterSettingsSnapshotValues(records))
	return nil
}

func masterSettingsSnapshotValues(records []models.Setting) map[string]string {
	values := make(map[string]string, len(settingDefs))
	for key, definition := range settingDefs {
		values[key] = definition.Default
	}
	for _, record := range records {
		if _, ok := settingDefs[record.Key]; ok {
			values[record.Key] = record.Value
		}
	}
	return values
}

func masterSettingsSnapshotUpdates(values map[string]string) map[string]string {
	masterValues := make(map[string]string)
	for key, value := range values {
		if _, ok := settingDefs[key]; ok {
			masterValues[key] = value
		}
	}
	return masterValues
}

func (h *Handler) GetSettings(c *app.Context, _ GetSettingsRequest) (SettingsResponse, error) {
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
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
	requestContext := c.RequestContext()
	release, err := h.acquireSettingsUpdate(requestContext)
	if err != nil {
		return SettingsResponse{}, err
	}
	defer release()

	agentKeys := settings.Defaults()
	for key, value := range req.Settings {
		_, isAgent := agentKeys[key]
		def, isDef := settingDefs[key]
		if !isAgent && !isDef {
			return SettingsResponse{}, api.BadRequestError("unknown setting: "+key, nil)
		}
		if isAgent {
			if err := settings.Validate(key, value); err != nil {
				return SettingsResponse{}, api.BadRequestError(err.Error(), nil)
			}
		}
		if key == consts.SettingAgentRelayDefaultURI {
			if value == "" {
				req.Settings[key] = ""
			} else {
				parsed, err := tunnel.ParseRelayURI(value)
				if err != nil {
					return SettingsResponse{}, api.BadRequestError("invalid value for "+key, nil)
				}
				req.Settings[key] = parsed.URI.String()
			}
		}
		if isDef && !def.Validate(value) {
			return SettingsResponse{}, api.BadRequestError("invalid value for "+key, nil)
		}
	}
	minRaw, minUpdated := req.Settings[consts.SettingAgentConnectivityProbeFailureRetryMinSeconds]
	maxRaw, maxUpdated := req.Settings[consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds]
	if minUpdated || maxUpdated {
		q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, requestContext)).Setting()
		if !minUpdated {
			minRaw = strconv.Itoa(q.LookupInt(consts.SettingAgentConnectivityProbeFailureRetryMinSeconds, 30))
		}
		if !maxUpdated {
			maxRaw = strconv.Itoa(q.LookupInt(consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds, 300))
		}
		minimum, _ := strconv.Atoi(minRaw)
		maximum, _ := strconv.Atoi(maxRaw)
		if maximum < minimum {
			return SettingsResponse{}, api.BadRequestError("connectivity probe failure retry max must be greater than or equal to min", nil)
		}
	}

	keys := make([]string, 0, len(req.Settings))
	for key := range req.Settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	daoContext := dao.NewContextWithContext(c.App, requestContext)
	if err := dao.RunInTx(daoContext, func(txContext dao.Context) error {
		m := dao.NewAdminMutation(txContext)
		for _, key := range keys {
			if err := m.Setting().Set(key, req.Settings[key]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return SettingsResponse{}, api.InternalError("save setting failed", err)
	}
	c.App.GetMasterSettings().Update(masterSettingsSnapshotUpdates(req.Settings))

	if h.RefreshProbeTimings != nil && containsProbeTimingSetting(req.Settings) {
		h.RefreshProbeTimings(requestContext)
	}

	publishErrors := make([]error, 0)
	for _, key := range keys {
		if err := events.PublishSettingUpdate(requestContext, c.GetBus(), models.Setting{Key: key, Value: req.Settings[key]}); err != nil {
			publishErrors = append(publishErrors, err)
		}
	}
	if publishErr := errors.Join(publishErrors...); publishErr != nil && c.Logger != nil {
		c.Logger.Warn("settings committed with publish failures",
			zap.String("code", "settings_publish_after_commit_failed"),
			zap.Int("failed", len(publishErrors)),
			zap.Int("attempted", len(keys)),
		)
	}

	return h.GetSettings(c, GetSettingsRequest{})
}

func containsProbeTimingSetting(values map[string]string) bool {
	for _, key := range []string{
		consts.SettingAgentConnectivityProbeSuccessTTLSeconds,
		consts.SettingAgentConnectivityProbeFailureRetryMinSeconds,
		consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds,
	} {
		if _, ok := values[key]; ok {
			return true
		}
	}
	return false
}
