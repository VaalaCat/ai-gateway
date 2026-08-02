package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

const (
	testMetricsToken = "test-metrics-token"
	testMetricName   = "authenticated_handler_private_requests_total"
)

type countingGatherer struct {
	gatherer prometheus.Gatherer
	calls    atomic.Int32
}

func (g *countingGatherer) Gather() ([]*dto.MetricFamily, error) {
	g.calls.Add(1)
	return g.gatherer.Gather()
}

func TestAuthenticatedHandlerAcceptsBearerVariants(t *testing.T) {
	defaultOnly := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "authenticated_handler_default_only_total",
		Help: "Metric used to detect accidental use of the default registry.",
	})
	require.NoError(t, prometheus.Register(defaultOnly))
	t.Cleanup(func() { prometheus.Unregister(defaultOnly) })
	defaultOnly.Inc()

	for _, authorization := range []string{
		"Bearer " + testMetricsToken,
		"Bearer    " + testMetricsToken,
		"bEaReR " + testMetricsToken,
	} {
		t.Run(authorization, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			privateMetric := prometheus.NewCounter(prometheus.CounterOpts{
				Name: testMetricName,
				Help: "Private metric exposed only after authentication.",
			})
			registry.MustRegister(privateMetric)
			privateMetric.Add(3)
			gatherer := &countingGatherer{gatherer: registry}
			handler := NewAuthenticatedHandler(gatherer, testMetricsToken)
			request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			request.Header.Set("Authorization", authorization)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, int32(1), gatherer.calls.Load())
			require.Contains(t, response.Body.String(), "# HELP "+testMetricName)
			require.Contains(t, response.Body.String(), testMetricName+" 3")
			require.NotContains(t, response.Body.String(), "authenticated_handler_default_only_total")
		})
	}
}

func TestAuthenticatedHandlerRejectsInvalidAuthorizationWithoutGathering(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "wrong token", values: []string{"Bearer wrong-token"}},
		{name: "basic scheme", values: []string{"Basic " + testMetricsToken}},
		{name: "empty credential", values: []string{"Bearer "}},
		{name: "scheme only", values: []string{"Bearer"}},
		{name: "tab delimiter", values: []string{"Bearer\t" + testMetricsToken}},
		{name: "internal space", values: []string{"Bearer test-metrics token"}},
		{name: "internal tab", values: []string{"Bearer test-metrics\ttoken"}},
		{name: "trailing space", values: []string{"Bearer " + testMetricsToken + " "}},
		{name: "duplicate header", values: []string{"Bearer " + testMetricsToken, "Bearer " + testMetricsToken}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			privateMetric := prometheus.NewCounter(prometheus.CounterOpts{
				Name: testMetricName,
				Help: "Private metric that must not be gathered after failed authentication.",
			})
			registry.MustRegister(privateMetric)
			privateMetric.Inc()
			gatherer := &countingGatherer{gatherer: registry}
			handler := NewAuthenticatedHandler(gatherer, testMetricsToken)
			request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			request.Header["Authorization"] = test.values
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			require.Equal(t, http.StatusUnauthorized, response.Code)
			require.Equal(t, "Bearer", response.Header().Get("WWW-Authenticate"))
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			require.Equal(t, http.StatusText(http.StatusUnauthorized)+"\n", response.Body.String())
			require.NotContains(t, response.Body.String(), testMetricsToken)
			require.NotContains(t, response.Body.String(), "# HELP "+testMetricName)
			require.Equal(t, int32(0), gatherer.calls.Load())
		})
	}
}

func TestAuthenticatedHandlerRejectsEmptyCredentialWhenConfiguredTokenIsEmpty(t *testing.T) {
	gatherer := &countingGatherer{gatherer: prometheus.NewRegistry()}
	handler := NewAuthenticatedHandler(gatherer, "")
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer ")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Equal(t, int32(0), gatherer.calls.Load())
	require.False(t, strings.Contains(response.Body.String(), testMetricsToken))
}
