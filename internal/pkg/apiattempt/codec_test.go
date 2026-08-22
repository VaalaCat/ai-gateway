package apiattempt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Production break caught: a non-empty error message must survive result JSON
// transport, including legacy payload decode and trace-only slimming.
func TestAPIExecutionResultErrorMessageJSONAndSlim(t *testing.T) {
	result := APIExecutionResult{
		ProviderDispatchKnown: true, ErrorStage: "transport", ErrorCode: "api_unavailable",
		ErrorMessage: "dial tcp: connection refused",
	}

	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.Equal(t, "dial tcp: connection refused", fields["error_message"])
	var decoded APIExecutionResult
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, result, decoded)

	t.Run("legacy payload leaves error message empty", func(t *testing.T) {
		var legacy APIExecutionResult
		require.NoError(t, json.Unmarshal([]byte(`{"provider_dispatch_known":true}`), &legacy))
		require.Empty(t, legacy.ErrorMessage)
		require.NoError(t, legacy.Validate())
	})
	t.Run("slim keeps error message after dropping trace", func(t *testing.T) {
		withTrace := result
		withTrace.Trace = &APIExecutionTrace{ResponseBody: &APIBodyCapture{
			Captured: true, Status: "captured", Data: strings.Repeat("x", 4096), CapturedBytes: 4096, TotalBytes: 4096,
		}}
		baseJSON, err := json.Marshal(result)
		require.NoError(t, err)
		slim, err := withTrace.Slim(len(baseJSON))
		require.NoError(t, err)
		require.Nil(t, slim.Trace)
		require.Equal(t, result.ErrorMessage, slim.ErrorMessage)
	})
}
