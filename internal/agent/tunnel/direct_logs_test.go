package tunnel

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDirectLogsSanitizeSensitiveFailureFields(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logs := newDirectLogs(zap.New(core), diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute}))
	event := directLogEvent{
		SourceAgentID: "source-a", TargetAgentID: "target-a", Stage: "handshake", ReasonCode: "protocol",
		SessionGeneration: 7, StreamID: "11", RequestID: "request-a", CommitState: "committed",
		ResponseStarted: true, SourceOutbound: true, TargetInbound: false, ResultKind: "provider_failed",
		ResultBytes: 32, Duration: 2 * time.Second,
	}
	body := strings.Repeat("x", 400<<10)
	secret := "password=hunter2 secret=marker token=abc ticket=xyz"
	logs.DialFailed(event, "wss://user:proxy-password@direct.example/private?token=query-secret", errors.New(secret+" body="+body))

	require.Equal(t, 1, observed.Len())
	fields := observed.All()[0].ContextMap()
	require.Equal(t, "source-a", fields["source_agent_id"])
	require.Equal(t, "target-a", fields["target_agent_id"])
	require.Equal(t, "wss", fields["endpoint_scheme"])
	require.Equal(t, "direct.example", fields["endpoint_host"])
	require.Equal(t, DirectTunnelPath, fields["endpoint_path"])
	require.Equal(t, "redacted", fields["error"])
	encoded := strings.ToLower(fmt.Sprint(fields))
	for _, forbidden := range []string{"hunter2", "query-secret", "proxy-password", "ticket=", "token=", "secret=", body[:1024]} {
		require.NotContains(t, encoded, strings.ToLower(forbidden))
	}
}

func TestDirectLogsSuppressAndRecoverRepeatedFailures(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	suppressor := diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute})
	logs := newDirectLogs(zap.New(core), suppressor)
	event := directLogEvent{SourceAgentID: "source-a", TargetAgentID: "target-a", Stage: "result", ReasonCode: "invalid"}

	for range 3 {
		logs.ResultProtocolFailed(event, "wss://direct.example/ignored?token=secret", errors.New(`{"trace":"secret"}`))
	}
	require.Equal(t, 1, observed.Len())
	logs.ResultProtocolRecovered(event)
	require.Equal(t, 2, observed.Len())
	require.Equal(t, "direct result protocol recovered", observed.All()[1].Message)
	require.EqualValues(t, 2, observed.All()[1].ContextMap()["suppressed_count"])
	require.False(t, suppressor.Contains(diagnostics.SuppressionKey{
		Source: "source-a", Target: "target-a", PathKind: "direct", Stage: "result", ReasonCode: "invalid",
	}))
}

func TestDirectLogsEmitWindowEndSummary(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logs := newDirectLogs(zap.New(core), diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute}))
	now := time.Unix(1_700_000_000, 0)
	logs.now = func() time.Time { return now }
	event := directLogEvent{SourceAgentID: "source-a", TargetAgentID: "target-a", Stage: "dial", ReasonCode: "unavailable"}
	for range 3 {
		logs.DialFailed(event, "wss://direct.example/private?token=secret", errors.New("dial failed"))
	}
	require.Len(t, observed.All(), 1)
	now = now.Add(time.Minute)
	logs.DialFailed(event, "wss://direct.example/private?token=secret", errors.New("dial failed"))
	require.Len(t, observed.All(), 2)
	require.Equal(t, "direct failures suppressed", observed.All()[1].Message)
	require.Equal(t, "window_end", observed.All()[1].ContextMap()["summary_kind"])
	require.EqualValues(t, 2, observed.All()[1].ContextMap()["suppressed_count"])
}

func TestDirectLogsEmptyAndNilInputsRemainBounded(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logs := newDirectLogs(zap.New(core), nil)
	logs.PolicyDisabled(directLogEvent{})
	logs.Closed(directLogEvent{}, "", nil)
	require.Len(t, observed.All(), 2)
	for _, entry := range observed.All() {
		require.Less(t, len(fmt.Sprint(entry.ContextMap())), 2048)
	}
}

func TestDirectLogsResultDrainFinishedUsesOnlyBoundedFields(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logs := newDirectLogs(zap.New(core), nil)
	logs.ResultDrainFinished(directLogEvent{
		SourceAgentID: "source-a", TargetAgentID: "target-a", Stage: "drain", ReasonCode: "timeout",
		SessionGeneration: 7, StreamID: "stream-a", RequestID: "request-a", CommitState: "committed",
		ResponseStarted: true, SourceOutbound: true, Duration: 5 * time.Second,
		DiscardedResponseBytes: 11, ResultReceived: true, EndReceived: false, ResultKind: "succeeded",
	})

	require.Equal(t, 1, observed.Len())
	entry := observed.All()[0]
	require.Equal(t, "direct attempt result drain finished", entry.Message)
	fields := entry.ContextMap()
	require.Equal(t, "direct", fields["route_path"])
	require.Equal(t, "direct_outgoing", fields["session_direction"])
	require.EqualValues(t, 5000, fields["drain_duration_ms"])
	require.EqualValues(t, 11, fields["discarded_response_bytes"])
	require.Equal(t, true, fields["result_received"])
	require.Equal(t, false, fields["end_received"])
	require.Equal(t, "succeeded", fields["result_kind"])
	for _, forbidden := range []string{"header", "body", "result_json", "ticket", "query", "uri"} {
		require.NotContains(t, fields, forbidden)
	}

	logs.ResultDrainFinished(directLogEvent{
		SourceAgentID: "source-a", TargetAgentID: "target-a", Stage: "drain", ReasonCode: "timeout",
		SessionGeneration: 7, StreamID: "stream-b", RequestID: "request-b", CommitState: "committed",
		ResponseStarted: true, SourceOutbound: true, Duration: 5 * time.Second,
		ResultReceived: false, EndReceived: false, ResultKind: "",
	})
	require.Equal(t, 2, observed.Len())
	missingResultFields := observed.All()[1].ContextMap()
	require.Equal(t, false, missingResultFields["result_received"])
	require.Equal(t, "", missingResultFields["result_kind"])
	for _, forbidden := range []string{"header", "body", "result_json", "ticket", "query", "uri"} {
		require.NotContains(t, missingResultFields, forbidden)
	}
}
