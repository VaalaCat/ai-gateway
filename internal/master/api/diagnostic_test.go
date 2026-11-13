package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDiagnosticErrorRedactsResponseAndLogs(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "wrapped timeout", err: fmt.Errorf("Authorization: Bearer private-value: %w", context.DeadlineExceeded), code: "rpc_timeout"},
		{name: "freeform agent response", err: fmt.Errorf("agent rpc error -32603: api_key=sk-private-value"), code: "agent_operation_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, entries := observer.New(zap.WarnLevel)
			failure := DiagnosticError(zap.New(core), "node-a", "agent.inflight", time.Now(), test.err)
			require.Equal(t, test.code, failure.Code)
			require.NotContains(t, failure.Message, "private-value")
			require.Len(t, entries.All(), 1)
			fields := entries.All()[0].ContextMap()
			encoded, err := json.Marshal(fields)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private-value")
			require.NotContains(t, string(encoded), "api_key")
			require.Equal(t, "node-a", fields["agent_id"])
			require.Equal(t, "agent.inflight", fields["method"])
			require.Equal(t, test.code, fields["code"])
		})
	}
}
