package state_test

import (
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
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
