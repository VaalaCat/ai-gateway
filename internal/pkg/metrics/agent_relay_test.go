package metrics

import (
	"io"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestDirectMetricsLifecycleResultsAndStableReasons(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAgentRelayMetrics(registry, registry)
	metrics.AddDirectSessions(DirectSessionConnecting, 1)
	metrics.AddDirectSessions(DirectSessionConnecting, -1)
	metrics.AddDirectSessions(DirectSessionActive, 1)
	metrics.AddDirectSessions(DirectSessionActive, -1)
	metrics.AddDirectSessions(DirectSessionDraining, 1)
	metrics.AddDirectStreams(1)
	metrics.AddDirectStreams(-1)
	metrics.IncDirectSessionDial(DirectDialSucceeded, DirectReasonNone)
	metrics.IncDirectSessionDial(DirectDialFailed, DirectReasonCode("password=secret arbitrary error"))
	metrics.IncDirectSessionReuse()
	metrics.ObserveDirectResultFrame(ResultKind("succeeded"), DirectReasonNone, 31)
	metrics.ObserveDirectResultFrame(ResultInvalid, DirectReasonProtocol)
	metrics.ObserveDirectResultFrame(ResultTooLarge, DirectReasonCode("400 KiB body token=secret"))
	metrics.IncPathDisabled(PathDirect, DirectReasonPolicy)
	metrics.ObserveDirectSessionDuration(250 * time.Millisecond)

	families, err := registry.Gather()
	require.NoError(t, err)
	require.Equal(t, float64(1), metricValue(t, families, "agent_direct_sessions", map[string]string{"state": "draining"}))
	require.Equal(t, float64(0), metricValue(t, families, "agent_direct_streams", nil))
	require.Equal(t, float64(1), metricValue(t, families, "agent_direct_session_dials_total", map[string]string{"result": "failure", "reason_code": "other"}))
	require.Equal(t, float64(1), metricValue(t, families, "agent_direct_session_reuses_total", nil))
	require.Equal(t, float64(1), metricValue(t, families, "agent_direct_result_frames_total", map[string]string{"result": "invalid", "reason_code": "protocol"}))
	require.Equal(t, float64(1), metricValue(t, families, "agent_direct_result_frames_total", map[string]string{"result": "too_large", "reason_code": "other"}))
	require.Equal(t, float64(31), metricValue(t, families, "agent_direct_result_bytes_total", nil))
	require.Equal(t, float64(1), metricValue(t, families, "agent_path_disabled_total", map[string]string{"path_kind": "direct", "reason_code": "policy"}))
	require.Equal(t, uint64(1), metricHistogramCount(t, families, "agent_direct_session_duration_seconds"))
}

func TestDirectMetricsInvalidTypedLifecycleValuesAreNoOp(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAgentRelayMetrics(registry, registry)
	metrics.AddDirectSessions(DirectSessionState("password=secret"), 1)
	metrics.IncDirectSessionDial(DirectDialResult("unexpected"), DirectReasonOther)

	families, err := registry.Gather()
	require.NoError(t, err)
	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.GetName())
	}
	require.NotContains(t, names, "agent_direct_sessions")
	require.NotContains(t, names, "agent_direct_session_dials_total")
}

func metricValue(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string) float64 {
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
				case metric.Gauge != nil:
					return metric.Gauge.GetValue()
				case metric.Counter != nil:
					return metric.Counter.GetValue()
				}
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
	return 0
}

func metricHistogramCount(t *testing.T, families []*dto.MetricFamily, name string) uint64 {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name && len(family.Metric) == 1 {
			return family.Metric[0].GetHistogram().GetSampleCount()
		}
	}
	t.Fatalf("histogram %s not found", name)
	return 0
}

func TestAgentRelayMetricNamesAndLabelsAreExact(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAgentRelayMetrics(registry, registry)
	metrics.ObserveRouteAttempt(RouteAttempt{PathKind: PathDirect, Result: ResultFailure, Stage: StageDial})
	metrics.ObserveFallback(RouteFallback{From: PathDirect, To: PathRelay, ReasonClass: ReasonAvailability})
	metrics.SetTunnelSession(SessionAvailable, ConvergenceConverged, 1)
	metrics.SetTunnelStreams(2)
	metrics.AddTunnelBytes(DirectionInbound, 3)
	metrics.IncTunnelReset(StageProtocol, true)
	metrics.IncDirectProbe(ProbeVerified)
	metrics.IncConnectivityProbe(PathRelay, ProbeReachable)
	metrics.IncRouteTelemetryDropped()

	families, err := registry.Gather()
	require.NoError(t, err)
	want := map[string][]string{
		"agent_route_attempt_total":             {"path_kind", "result", "stage"},
		"agent_route_fallback_total":            {"from", "reason_class", "to"},
		"agent_tunnel_sessions":                 {"availability", "convergence"},
		"agent_tunnel_streams":                  {},
		"agent_tunnel_bytes_total":              {"direction"},
		"agent_tunnel_resets_total":             {"committed", "stage"},
		"agent_direct_probe_total":              {"result"},
		"agent_connectivity_probe_total":        {"path_kind", "result"},
		"agent_route_telemetry_dropped_total":   {},
		"agent_direct_streams":                  {},
		"agent_direct_session_reuses_total":     {},
		"agent_direct_result_bytes_total":       {},
		"agent_direct_session_duration_seconds": {},
	}
	require.Len(t, families, len(want))
	for _, family := range families {
		labels := make([]string, 0)
		for _, label := range family.Metric[0].Label {
			labels = append(labels, label.GetName())
		}
		sort.Strings(labels)
		require.Equal(t, want[family.GetName()], labels, family.GetName())
	}
}

func TestAgentRelayMetricAPIHasNoRawStringLabels(t *testing.T) {
	typeOf := reflect.TypeOf((*AgentRelayMetrics)(nil))
	for _, methodName := range []string{
		"ObserveRouteAttempt", "ObserveFallback", "SetTunnelSession", "AddTunnelBytes", "IncTunnelReset", "IncDirectProbe", "IncConnectivityProbe",
		"AddDirectSessions", "IncDirectSessionDial", "ObserveDirectResultFrame", "IncPathDisabled",
	} {
		method, ok := typeOf.MethodByName(methodName)
		require.True(t, ok, methodName)
		for index := 1; index < method.Type.NumIn(); index++ {
			parameter := method.Type.In(index)
			if parameter.Kind() == reflect.String {
				require.NotEmpty(t, parameter.PkgPath(), "%s accepts raw string label", methodName)
			}
		}
	}
}

func TestAgentRelayMetricRegistriesAndHandlersAreIsolated(t *testing.T) {
	agentRegistry := prometheus.NewRegistry()
	masterRegistry := prometheus.NewRegistry()
	agent := NewAgentRelayMetrics(agentRegistry, agentRegistry)
	master := NewAgentRelayMetrics(masterRegistry, masterRegistry)
	agent.IncDirectProbe(ProbeVerified)
	master.IncDirectProbe(ProbeInvalid)

	agentResponse := httptest.NewRecorder()
	agent.Handler().ServeHTTP(agentResponse, httptest.NewRequest("GET", "/metrics", nil))
	masterResponse := httptest.NewRecorder()
	master.Handler().ServeHTTP(masterResponse, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, agentResponse.Code)
	require.Equal(t, 200, masterResponse.Code)
	agentBody, err := io.ReadAll(agentResponse.Result().Body)
	require.NoError(t, err)
	masterBody, err := io.ReadAll(masterResponse.Result().Body)
	require.NoError(t, err)
	require.Contains(t, string(agentBody), `result="verified"`)
	require.NotContains(t, string(agentBody), `result="invalid"`)
	require.Contains(t, string(masterBody), `result="invalid"`)
	require.NotContains(t, string(masterBody), `result="verified"`)
}
