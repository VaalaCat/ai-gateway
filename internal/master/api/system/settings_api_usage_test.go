package system

import (
	"testing"

	masterapiusage "github.com/VaalaCat/ai-gateway/internal/master/apiusage"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestAPIUsageSettingsAreMasterOnlyWithValidatedBounds(t *testing.T) {
	agentDefaults := settings.Defaults()
	for _, key := range []string{masterapiusage.QueueCapacityKey, masterapiusage.WorkerConcurrencyKey} {
		require.NotContains(t, agentDefaults, key)
		require.Contains(t, settingDefs, key)
	}
	require.Equal(t, "10000", settingDefs[masterapiusage.QueueCapacityKey].Default)
	require.Equal(t, "2", settingDefs[masterapiusage.WorkerConcurrencyKey].Default)

	valid := map[string][]string{
		masterapiusage.QueueCapacityKey:     {"100", "10000", "1000000"},
		masterapiusage.WorkerConcurrencyKey: {"1", "2", "32"},
	}
	for key, values := range valid {
		for _, value := range values {
			require.True(t, settingDefs[key].Validate(value), "%s=%s", key, value)
		}
	}
	invalid := map[string][]string{
		masterapiusage.QueueCapacityKey:     {"99", "1000001", "invalid"},
		masterapiusage.WorkerConcurrencyKey: {"0", "33", "invalid"},
	}
	for key, values := range invalid {
		for _, value := range values {
			require.False(t, settingDefs[key].Validate(value), "%s=%s", key, value)
		}
	}
}

func TestUpdateAPIUsageSettingsDoesNotBroadcastToAgents(t *testing.T) {
	c, bus := newSettingsContextWithBus(t)
	response, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		masterapiusage.QueueCapacityKey:     "100",
		masterapiusage.WorkerConcurrencyKey: "32",
	}})
	require.NoError(t, err)
	require.Equal(t, "100", response.Settings[masterapiusage.QueueCapacityKey])
	require.Equal(t, "32", response.Settings[masterapiusage.WorkerConcurrencyKey])
	require.Empty(t, bus.events)
}
