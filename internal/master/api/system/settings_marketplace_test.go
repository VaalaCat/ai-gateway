package system

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestSeedMasterSettingsSnapshotLoadsOnlyMasterSettings(t *testing.T) {
	c := newSettingsContext(t)
	require.NoError(t, c.App.GetCoreDB().Create([]models.Setting{
		{Key: consts.SettingKeyModelMarketplaceEnabled, Value: "true"},
		{Key: "retry_max_channels", Value: "37"},
	}).Error)

	require.NoError(t, SeedMasterSettingsSnapshot(c.App))
	snapshot := c.App.GetMasterSettings()
	require.True(t, snapshot.LookupBool(consts.SettingKeyModelMarketplaceEnabled, false))
	require.Equal(t, "20", mustLookupMasterSetting(t, snapshot, consts.SettingKeyModelMarketplaceMinSamples))
	_, agentSettingPresent := snapshot.Lookup("retry_max_channels")
	require.False(t, agentSettingPresent)
}

func TestUpdateSettingsRefreshesMasterSnapshotBeforePublish(t *testing.T) {
	c, bus := newSettingsContextWithBus(t)
	require.NoError(t, SeedMasterSettingsSnapshot(c.App))
	bus.failAt = map[int]error{0: errors.New("publish unavailable")}

	_, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		consts.SettingKeyModelMarketplaceEnabled: "true",
		"retry_max_channels":                     "37",
	}})
	require.NoError(t, err)
	require.True(t, c.App.GetMasterSettings().LookupBool(consts.SettingKeyModelMarketplaceEnabled, false))
	_, agentSettingPresent := c.App.GetMasterSettings().Lookup("retry_max_channels")
	require.False(t, agentSettingPresent)
}

func mustLookupMasterSetting(t *testing.T, snapshot interface {
	Lookup(string) (string, bool)
}, key string) string {
	t.Helper()
	value, ok := snapshot.Lookup(key)
	require.True(t, ok, "master setting %q missing", key)
	return value
}

func TestModelMarketplaceSettingsDefaults(t *testing.T) {
	got, err := (&Handler{}).GetSettings(newSettingsContext(t), GetSettingsRequest{})
	require.NoError(t, err)
	require.Equal(t, "false", got.Settings[consts.SettingKeyModelMarketplaceEnabled])
	require.Equal(t, "20", got.Settings[consts.SettingKeyModelMarketplaceMinSamples])
}

func TestModelMarketplaceSettingsAcceptValidBoundaries(t *testing.T) {
	for _, minSamples := range []string{"1", "20", "100000"} {
		t.Run(minSamples, func(t *testing.T) {
			got, err := (&Handler{}).UpdateSettings(newSettingsContext(t), UpdateSettingsRequest{Settings: map[string]string{
				consts.SettingKeyModelMarketplaceEnabled:    "true",
				consts.SettingKeyModelMarketplaceMinSamples: minSamples,
			}})
			require.NoError(t, err)
			require.Equal(t, "true", got.Settings[consts.SettingKeyModelMarketplaceEnabled])
			require.Equal(t, minSamples, got.Settings[consts.SettingKeyModelMarketplaceMinSamples])
		})
	}
}

func TestModelMarketplaceSettingsRejectInvalidValues(t *testing.T) {
	tests := map[string]map[string]string{
		"invalid boolean": {consts.SettingKeyModelMarketplaceEnabled: "1"},
		"below minimum":   {consts.SettingKeyModelMarketplaceMinSamples: "0"},
		"above maximum":   {consts.SettingKeyModelMarketplaceMinSamples: "100001"},
		"not an integer":  {consts.SettingKeyModelMarketplaceMinSamples: "1.5"},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := (&Handler{}).UpdateSettings(newSettingsContext(t), UpdateSettingsRequest{Settings: values})
			require.True(t, isBadRequest(err), "error = %v", err)
		})
	}
}
