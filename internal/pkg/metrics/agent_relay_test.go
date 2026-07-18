package metrics

import (
	"io"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

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
		"agent_route_attempt_total":           {"path_kind", "result", "stage"},
		"agent_route_fallback_total":          {"from", "reason_class", "to"},
		"agent_tunnel_sessions":               {"availability", "convergence"},
		"agent_tunnel_streams":                {},
		"agent_tunnel_bytes_total":            {"direction"},
		"agent_tunnel_resets_total":           {"committed", "stage"},
		"agent_direct_probe_total":            {"result"},
		"agent_connectivity_probe_total":      {"path_kind", "result"},
		"agent_route_telemetry_dropped_total": {},
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
