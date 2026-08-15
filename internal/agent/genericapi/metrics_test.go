package genericapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestGenericAPIMetricsBoundLabelsAndExposeQueueDrops(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAPIMetrics(registry)

	metrics.Requests.WithLabelValues(ProtocolHTTP, "success").Inc()
	metrics.Requests.WithLabelValues(ProtocolWebSocket, "error").Inc()
	metrics.Dispatches.WithLabelValues("direct", "success").Inc()
	metrics.Dispatches.WithLabelValues("relay", "error").Inc()
	metrics.Active.WithLabelValues(ProtocolHTTP).Set(1)
	metrics.UsageDropped.Inc()
	metrics.TraceSlimmed.Inc()

	families, err := registry.Gather()
	require.NoError(t, err)
	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		byName[family.GetName()] = family
	}

	tests := []struct {
		name       string
		family     string
		labelNames []string
	}{
		{name: "request success and failure use protocol outcome only", family: "generic_api_requests_total", labelNames: []string{"outcome", "protocol"}},
		{name: "dispatch success and failure use transport outcome only", family: "generic_api_dispatches_total", labelNames: []string{"outcome", "transport"}},
		{name: "active boundary uses protocol only", family: "generic_api_active", labelNames: []string{"protocol"}},
		{name: "queue drops are exposed without labels", family: "generic_api_usage_dropped_total", labelNames: []string{}},
		{name: "trace slimming is exposed without labels", family: "generic_api_trace_slimmed_total", labelNames: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			family := byName[test.family]
			require.NotNil(t, family, "missing metric family %s", test.family)
			require.NotEmpty(t, family.Metric)
			for _, metric := range family.Metric {
				got := make([]string, 0, len(metric.Label))
				for _, label := range metric.Label {
					got = append(got, label.GetName())
					require.NotContains(t, []string{"service", "route", "token", "request_id"}, label.GetName())
				}
				require.Equal(t, test.labelNames, got)
			}
		})
	}
}

type metricsProtocolHandler struct {
	err        error
	dispatched bool
}

func (h metricsProtocolHandler) Serve(_ context.Context, rc *RequestContext) error {
	rc.Agent.AgentRoutePath = app.RoutePathLocal
	rc.Execution = apiattempt.APIExecutionResult{
		ProviderDispatchKnown: true, ProviderDispatched: h.dispatched,
	}
	if h.err == nil {
		rc.Context.Status(http.StatusNoContent)
	}
	return h.err
}

func TestGenericAPIMetricsObserveHandlerSuccessFailureAndBoundary(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAPIMetrics(registry)

	success := newHandlerForTest(t)
	success.publisher.metrics = metrics
	success.executor = NewExecutor(map[string]ProtocolHandler{ProtocolHTTP: metricsProtocolHandler{dispatched: true}})
	response := httptest.NewRecorder()
	genericAPIRouter(success).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))
	require.Equal(t, http.StatusNoContent, response.Code)

	failure := newHandlerForTest(t)
	failure.publisher.metrics = metrics
	failure.executor = NewExecutor(map[string]ProtocolHandler{ProtocolHTTP: metricsProtocolHandler{dispatched: true, err: ErrExecutionUnavailable}})
	response = httptest.NewRecorder()
	genericAPIRouter(failure).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)

	boundary := newHandlerForTest(t)
	boundary.publisher.metrics = metrics
	request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	request.Header.Set("Connection", "upgrade")
	request.Header.Set("Upgrade", "not-websocket")
	response = httptest.NewRecorder()
	genericAPIRouter(boundary).ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code)

	families, err := registry.Gather()
	require.NoError(t, err)
	counts := map[string]float64{}
	active := -1.0
	for _, family := range families {
		for _, metric := range family.Metric {
			labels := ""
			for _, label := range metric.Label {
				labels += label.GetName() + "=" + label.GetValue() + ","
			}
			switch family.GetName() {
			case "generic_api_requests_total":
				counts[labels] = metric.GetCounter().GetValue()
			case "generic_api_dispatches_total":
				counts[family.GetName()+":"+labels] = metric.GetCounter().GetValue()
			case "generic_api_active":
				active = metric.GetGauge().GetValue()
			}
		}
	}
	require.Equal(t, float64(1), counts["outcome=success,protocol=http,"])
	require.Equal(t, float64(1), counts["outcome=error,protocol=http,"])
	require.Equal(t, float64(1), counts["outcome=error,protocol=unknown,"])
	require.Equal(t, float64(1), counts["generic_api_dispatches_total:outcome=success,transport=local,"])
	require.Equal(t, float64(1), counts["generic_api_dispatches_total:outcome=error,transport=local,"])
	require.Zero(t, active, "active gauge must return to zero after success, failure, and validation rejection")
}
