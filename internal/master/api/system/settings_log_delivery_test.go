package system

import (
	"testing"

	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestLogDeliverySettingsGetAndUpdate(t *testing.T) {
	c := newSettingsContext(t)
	h := &Handler{}
	got, err := h.GetSettings(c, GetSettingsRequest{})
	require.NoError(t, err)
	require.Equal(t, "10000", got.Settings[masterlogqueue.QueueMaxEntriesKey])
	require.Equal(t, "134217728", got.Settings[masterlogqueue.QueueMaxBytesKey])
	require.Equal(t, "100", got.Settings[masterlogqueue.DeliveryBatchSizeKey])
	require.Equal(t, "60", got.Settings[masterlogqueue.BackoffMaxSecondsKey])

	updated, err := h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		masterlogqueue.QueueMaxEntriesKey: "100", masterlogqueue.QueueMaxBytesKey: "1048576",
		masterlogqueue.DeliveryBatchSizeKey: "1000", masterlogqueue.BackoffMaxSecondsKey: "3600",
	}})
	require.NoError(t, err)
	require.Equal(t, "100", updated.Settings[masterlogqueue.QueueMaxEntriesKey])
}

func TestLogDeliverySettingsRejectInvalidBoundariesAndRemainMasterOnly(t *testing.T) {
	invalid := map[string]string{
		masterlogqueue.QueueMaxEntriesKey: "99", masterlogqueue.QueueMaxBytesKey: "1048575",
		masterlogqueue.DeliveryBatchSizeKey: "1001", masterlogqueue.BackoffMaxSecondsKey: "0",
	}
	for key, value := range invalid {
		t.Run(key, func(t *testing.T) {
			_, err := (&Handler{}).UpdateSettings(newSettingsContext(t), UpdateSettingsRequest{Settings: map[string]string{key: value}})
			require.True(t, isBadRequest(err))
		})
	}
	_, err := (&Handler{}).UpdateSettings(newSettingsContext(t), UpdateSettingsRequest{Settings: map[string]string{
		masterlogqueue.DeliveryBatchSizeKey: "not-a-number",
	}})
	require.True(t, isBadRequest(err))
	agentDefaults := settings.Defaults()
	for _, key := range []string{masterlogqueue.QueueMaxEntriesKey, masterlogqueue.QueueMaxBytesKey, masterlogqueue.DeliveryBatchSizeKey, masterlogqueue.BackoffMaxSecondsKey} {
		require.NotContains(t, agentDefaults, key)
	}
	_, err = (&Handler{}).UpdateSettings(newSettingsContext(t), UpdateSettingsRequest{Settings: map[string]string{"log.unknown": "1"}})
	require.True(t, isBadRequest(err))
}
