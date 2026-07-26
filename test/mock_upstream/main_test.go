package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMockUpstreamScenarios(t *testing.T) {
	handler := newMockUpstreamHandler(25 * time.Millisecond)
	for _, tt := range []struct {
		model       string
		stream      bool
		wantStatus  int
		wantContent string
	}{
		{model: "mock-success", wantStatus: 200, wantContent: "chat.completion"},
		{model: "mock-no-usage", wantStatus: 200, wantContent: "mock success without usage"},
		{model: "mock-stream", stream: true, wantStatus: 200, wantContent: "[DONE]"},
		{model: "mock-429", wantStatus: 429, wantContent: "rate_limit"},
		{model: "mock-500", wantStatus: 500, wantContent: "server_error"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			body := []byte(`{"model":"` + tt.model + `","stream":` + map[bool]string{true: "true", false: "false"}[tt.stream] + `}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			require.Equal(t, tt.wantStatus, resp.Code)
			require.Contains(t, resp.Body.String(), tt.wantContent)
			if tt.model == "mock-no-usage" {
				require.NotContains(t, resp.Body.String(), `"usage":`)
			}
		})
	}
}

func TestMockUpstreamTimeoutActuallyWaits(t *testing.T) {
	delay := 30 * time.Millisecond
	started := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"mock-timeout"}`))
	resp := httptest.NewRecorder()
	newMockUpstreamHandler(delay).ServeHTTP(resp, req)
	require.GreaterOrEqual(t, time.Since(started), delay)
	require.Equal(t, http.StatusGatewayTimeout, resp.Code)
}

func TestMockUpstreamHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	newMockUpstreamHandler(time.Second).ServeHTTP(resp, req)
	require.Equal(t, http.StatusNoContent, resp.Code)
}
