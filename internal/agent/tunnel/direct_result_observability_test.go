package tunnel

import (
	"strings"
	"testing"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDirectSourceResultMetricsAfterProtocolValidation(t *testing.T) {
	validPayload, err := attemptwire.EncodeResultJSON(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
	require.NoError(t, err)
	tests := []struct {
		name       string
		prepare    func(*testing.T, *Stream)
		payload    []byte
		wantResult pkgmetrics.ResultKind
		wantBytes  float64
	}{
		{name: "valid", prepare: prepareDirectResultHeaders, payload: validPayload, wantResult: pkgmetrics.ResultKind(attemptwire.ResultSucceeded), wantBytes: float64(len(validPayload))},
		{name: "malformed", prepare: prepareDirectResultHeaders, payload: []byte(`{"kind":`), wantResult: pkgmetrics.ResultInvalid},
		{name: "too large", prepare: prepareDirectResultHeaders, payload: []byte(strings.Repeat("x", attemptwire.MaxResultWireBytes+1)), wantResult: pkgmetrics.ResultTooLarge},
		{name: "wrong sequence", payload: validPayload, wantResult: pkgmetrics.ResultInvalid},
		{name: "wrong phase", payload: validPayload, wantResult: pkgmetrics.ResultInvalid, prepare: func(t *testing.T, stream *Stream) {
			stream.receiveSeq = 3
		}},
		{name: "probe", prepare: func(t *testing.T, stream *Stream) {
			stream.kind = streamKindProbe
			prepareDirectResultHeaders(t, stream)
		}, payload: validPayload, wantResult: pkgmetrics.ResultInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			metrics := pkgmetrics.NewAgentRelayMetrics(registry, registry)
			stream := directObservedResultStream(t, metrics, zap.NewNop(), nil)
			if test.prepare != nil {
				test.prepare(t, stream)
			}
			stream.handleFrame(wire.Frame{Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: test.payload})
			families, err := registry.Gather()
			require.NoError(t, err)
			reason := "protocol"
			if test.wantResult == pkgmetrics.ResultKind(attemptwire.ResultSucceeded) {
				reason = "none"
			}
			require.Equal(t, float64(1), directMetricValue(t, families, "agent_direct_result_frames_total", map[string]string{
				"result": string(test.wantResult), "reason_code": reason,
			}))
			require.Equal(t, test.wantBytes, directMetricValue(t, families, "agent_direct_result_bytes_total", nil))
		})
	}
}

func TestDirectSourceDuplicateResultCountsOneSuccessAndOneProtocolFailure(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := pkgmetrics.NewAgentRelayMetrics(registry, registry)
	stream := directObservedResultStream(t, metrics, zap.NewNop(), nil)
	prepareDirectResultHeaders(t, stream)
	payload, err := attemptwire.EncodeResultJSON(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
	require.NoError(t, err)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload}))
	require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameAttemptResult, Sequence: 5, StreamID: stream.id, Payload: payload}))
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Equal(t, float64(1), directMetricValue(t, families, "agent_direct_result_frames_total", map[string]string{"result": "succeeded", "reason_code": "none"}))
	require.Equal(t, float64(1), directMetricValue(t, families, "agent_direct_result_frames_total", map[string]string{"result": "invalid", "reason_code": "protocol"}))
	require.Equal(t, float64(len(payload)), directMetricValue(t, families, "agent_direct_result_bytes_total", nil))
}

func TestDirectResultProtocolFailureRecoversOnNextValidResult(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logs := newDirectLogs(zap.New(core), diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute}))
	for range 3 {
		stream := directObservedResultStream(t, nil, zap.NewNop(), logs)
		prepareDirectResultHeaders(t, stream)
		require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: []byte(`{"token":"secret"}`)}))
	}
	stream := directObservedResultStream(t, nil, zap.NewNop(), logs)
	prepareDirectResultHeaders(t, stream)
	payload, err := attemptwire.EncodeResultJSON(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
	require.NoError(t, err)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload}))

	require.Equal(t, []string{"direct result protocol failed", "direct result protocol recovered"}, observedMessages(observed.All()))
	require.NotContains(t, strings.ToLower(observed.All()[0].ContextMap()["error"].(string)), "secret")
}

func TestDirectTargetResultFailuresRecordOnlyFailedFrames(t *testing.T) {
	tests := []struct {
		name       string
		queueBytes int64
		enqueueErr bool
		result     attemptwire.AttemptProxyResult
		wantResult pkgmetrics.ResultKind
		wantRecord bool
	}{
		{
			name: "encode too large", queueBytes: 1 << 20,
			result:     attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProxyRejected, ErrorMessage: strings.Repeat("x", attemptwire.MaxResultWireBytes+1)},
			wantResult: pkgmetrics.ResultTooLarge,
			wantRecord: true,
		},
		{
			name: "enqueue failure", enqueueErr: true,
			result: attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProxyRejected, ErrorMessage: strings.Repeat("x", 256)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queueBytes := test.queueBytes
			if test.enqueueErr {
				headersPayload, err := wire.EncodeMetadata(wire.Headers{StatusCode: 200}, wire.MaxMetadataBytes)
				require.NoError(t, err)
				queueBytes = int64(wire.HeaderSize + len(headersPayload))
			}
			registry := prometheus.NewRegistry()
			metrics := pkgmetrics.NewAgentRelayMetrics(registry, registry)
			core, observed := observer.New(zap.DebugLevel)
			stream, _ := newAttemptResultTargetStream(t, queueBytes)
			stream.session.opts = defaultSessionOptions(SessionOptions{Direction: SessionDirectionDirectIncoming, Metrics: metrics, Logger: zap.New(core)})
			stream.session.opts.directLogs = newDirectLogs(zap.New(core), diagnostics.NewSuppressor(diagnostics.SuppressorOptions{}))
			stream.session.opts.directSourceAgentID = "source-a"
			stream.session.opts.directTargetAgentID = "target-a"
			require.NoError(t, enqueueAttemptResultHeaders(t, stream))
			require.Error(t, stream.WriteAttemptResult(test.result))

			families, err := registry.Gather()
			require.NoError(t, err)
			wantCount := float64(0)
			if test.wantRecord {
				wantCount = 1
			}
			require.Equal(t, wantCount, directMetricValue(t, families, "agent_direct_result_frames_total", map[string]string{
				"result": string(test.wantResult), "reason_code": "protocol",
			}))
			require.Zero(t, directMetricValue(t, families, "agent_direct_result_bytes_total", nil))
			if test.wantRecord {
				require.Equal(t, []string{"direct result protocol failed"}, observedMessages(observed.All()))
			} else {
				require.Empty(t, observedMessages(observed.All()))
			}
		})
	}
}

func directObservedResultStream(t *testing.T, metrics *pkgmetrics.AgentRelayMetrics, logger *zap.Logger, logs *directLogs) *Stream {
	t.Helper()
	stream := committedReceiveTestStream(t)
	stream.kind = streamKindAttempt
	stream.session.opts.Direction = SessionDirectionDirectOutgoing
	stream.session.opts.Metrics = metrics
	stream.session.opts.Logger = logger
	if logs != nil {
		stream.session.opts.directLogs = logs
	}
	stream.session.opts.directSourceAgentID = "source-a"
	stream.session.opts.directTargetAgentID = "target-a"
	return stream
}

func prepareDirectResultHeaders(t *testing.T, stream *Stream) {
	t.Helper()
	stream.handleFrame(responseHeadersFrame(t, stream, 3))
}

func observedMessages(entries []observer.LoggedEntry) []string {
	messages := make([]string, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, entry.Message)
	}
	return messages
}

func directMetricValue(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string) float64 {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			matched := true
			for key, value := range labels {
				found := false
				for _, label := range metric.Label {
					found = found || (label.GetName() == key && label.GetValue() == value)
				}
				matched = matched && found
			}
			if matched {
				switch {
				case metric.Counter != nil:
					return metric.Counter.GetValue()
				case metric.Gauge != nil:
					return metric.Gauge.GetValue()
				}
			}
		}
	}
	return 0
}
