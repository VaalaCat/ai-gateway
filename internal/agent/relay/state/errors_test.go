package state

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
)

// TestSentinelErrorsMessage：每个 sentinel 的 Error() 必须等于 consts 里的常量
// （或对 ForcedChannelID 这种没 consts 常量的，用 plan 指定的字面量）。
func TestSentinelErrorsMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"ErrReadBody", ErrReadBody, consts.ErrReadRequestBody},
		{"ErrInvalidBody", ErrInvalidBody, consts.ErrInvalidRequestBody},
		{"ErrModelRequired", ErrModelRequired, consts.ErrModelRequired},
		{"ErrInvalidForcedChannelID", ErrInvalidForcedChannelID, "invalid X-Channel-ID"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err.Error() != c.want {
				t.Errorf("got %q want %q", c.err.Error(), c.want)
			}
		})
	}
}

// TestSentinelErrorsAreDistinct：边界 — 所有 sentinel 必须是不同 error 实例，
// 否则 errors.Is 区分不出来。
func TestSentinelErrorsAreDistinct(t *testing.T) {
	errs := []error{
		ErrReadBody, ErrInvalidBody, ErrModelRequired, ErrInvalidForcedChannelID,
		ErrNoRoutableModel, ErrNoChannelAvailable, ErrModelNotAllowed, ErrRoutingFallback,
	}
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Errorf("sentinel %d and %d are same instance", i, j)
			}
		}
	}
}

// TestErrRoutingFallback_Message：ErrRoutingFallback 的 Error() 必须等于
// consts.ErrNoChannelAvailable，对齐 main:handler.go 502 fallback ErrorMessage 写入 UsageLog.ErrorMessage 的字面量。
//
// behavior parity with main
func TestErrRoutingFallback_Message(t *testing.T) {
	if ErrRoutingFallback.Error() != consts.ErrNoChannelAvailable {
		t.Errorf("ErrRoutingFallback.Error() = %q, want %q",
			ErrRoutingFallback.Error(), consts.ErrNoChannelAvailable)
	}
}

// TestSentinelErrorsIs：sentinel 必须能被 errors.Is 识别（包括包裹后的）。
func TestSentinelErrorsIs(t *testing.T) {
	wrapped := errors.Join(errors.New("ctx"), ErrReadBody)
	if !errors.Is(wrapped, ErrReadBody) {
		t.Fatal("errors.Is should match wrapped ErrReadBody")
	}
	if errors.Is(wrapped, ErrInvalidBody) {
		t.Fatal("errors.Is should NOT match an unrelated sentinel")
	}
}
