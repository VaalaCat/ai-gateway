package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/master/controlrpc"
	"github.com/stretchr/testify/require"
)

func TestGetInflightClassifiesDiagnosticFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		raw    json.RawMessage
		status int
		code   string
	}{
		{name: "timeout", err: context.DeadlineExceeded, status: 504, code: "rpc_timeout"},
		{name: "canceled", err: context.Canceled, status: 408, code: "request_canceled"},
		{name: "disconnected", err: controlrpc.ErrDisconnected, status: 503, code: "control_disconnected"},
		{name: "generation", err: connectivity.ErrConnectionGenerationChanged, status: 409, code: "connection_generation_changed"},
		{name: "send", err: controlrpc.ErrSendFailed, status: 502, code: "send_failed"},
		{name: "invalid", raw: json.RawMessage(`{"secret":"bad shape"}`), status: 502, code: "invalid_agent_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, appCtx, _, _ := newFullSyncFixture(t, 1)
			h.HubCallSession = func(ctx context.Context, _ string, _ uint64, _ string, _ any, timeout time.Duration) (json.RawMessage, error) {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), api.DiagnosticNodeTimeout)
				require.Equal(t, api.DiagnosticNodeTimeout, timeout)
				if test.err != nil {
					return nil, fmt.Errorf("Authorization: Bearer private: %w", test.err)
				}
				return test.raw, nil
			}
			_, err := h.GetInflight(appCtx, AgentIDQuery{ID: strconv.Itoa(1)})
			failure := requireAPIError(t, err)
			require.Equal(t, test.status, failure.Status)
			require.Equal(t, test.code, failure.Code)
			_, body := (api.DefaultErrorMapper{}).Map(err)
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private")
			require.NotContains(t, string(encoded), "secret")
		})
	}
}
