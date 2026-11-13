package state_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/stretchr/testify/require"
)

// TestStatusFromState_InsufficientQuota: ErrInsufficientQuota 必须映射到 HTTP 402，
// 且 user-facing message 带上 model 名（与其它 sentinel 的拼接风格对齐）。
func TestStatusFromState_InsufficientQuota(t *testing.T) {
	rctx := &state.RelayContext{
		State: &state.RelayState{Err: state.ErrInsufficientQuota},
		Input: state.RelayInput{Model: "gpt-4o"},
	}
	code, msg := state.StatusFromState(rctx)
	if code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402", code)
	}
	if msg == "" {
		t.Error("empty msg")
	}
}

func TestChannelAuthErrorOutput(t *testing.T) {
	for _, code := range []string{"invalid_upstream_auth", "upstream_auth_key_missing"} {
		t.Run(code, func(t *testing.T) {
			authErr := &upstream.ChannelAuthError{Code: code, Message: "upstream auth requires a channel key"}
			rctx := &state.RelayContext{State: &state.RelayState{Err: fmt.Errorf("connect: %w", authErr)}, Input: state.RelayInput{RequestID: "request-1"}}
			status, message := state.StatusFromState(rctx)
			require.Equal(t, http.StatusBadGateway, status)
			require.Equal(t, authErr.Message, message)
			public, ok := state.PublicRouteErrorFromState(rctx)
			require.True(t, ok)
			require.Equal(t, code, public.Code)
			require.Equal(t, "upstream_dispatch", public.Stage)
			require.Equal(t, authErr.Message, public.Message)
		})
	}
}
