package protocol

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/stretchr/testify/require"
)

func TestUsageLogEntryAutoDisableTriggerRoundTrip(t *testing.T) {
	want := UsageLogEntry{RequestID: "r1", AutoDisableTriggers: []attemptproxy.ChannelAutoDisableTrigger{{
		Source: attemptproxy.SourcePrivate, ChannelID: 9, Revision: 4,
		Reason: attemptproxy.ChannelAutoDisableReasonConsecutiveErrors,
	}}}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	var got UsageLogEntry
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want.AutoDisableTriggers, got.AutoDisableTriggers)
}

func TestUsageLogEntryAutoDisableTriggerDistinguishesSources(t *testing.T) {
	want := []attemptproxy.ChannelAutoDisableTrigger{
		{Source: attemptproxy.SourceAdmin, ChannelID: 9, Reason: attemptproxy.ChannelAutoDisableReasonConsecutiveErrors},
		{Source: attemptproxy.SourcePrivate, ChannelID: 9, Reason: attemptproxy.ChannelAutoDisableReasonConsecutiveErrors},
	}
	raw, err := json.Marshal(UsageLogEntry{RequestID: "r1", AutoDisableTriggers: want})
	require.NoError(t, err)
	var got UsageLogEntry
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want, got.AutoDisableTriggers)
}

func TestUsageLogEntryAutoDisableTriggerOmitsZeroValue(t *testing.T) {
	raw, err := json.Marshal(UsageLogEntry{RequestID: "r1"})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "auto_disable_triggers")
}
