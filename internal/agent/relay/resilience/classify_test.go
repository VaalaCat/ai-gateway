package resilience

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

func up(status int, etype string) state.AttemptResult {
	return state.AttemptResult{Err: &common.UpstreamError{Status: status, ProviderErrorType: etype}}
}

func TestClassify_RetryableServerErrors(t *testing.T) {
	for _, s := range []int{0 /*网络*/, 500, 502, 503, 429} {
		d := Classify(up(s, ""))
		if !d.RetrySameChannel || !d.CountToBreaker || d.AbortAll {
			t.Fatalf("status %d want retry+breaker, got %+v", s, d)
		}
	}
}

func TestClassify_AuthNoRetryButBreaker(t *testing.T) {
	for _, s := range []int{401, 403} {
		d := Classify(up(s, ""))
		if d.RetrySameChannel || !d.CountToBreaker || d.AbortAll {
			t.Fatalf("status %d want no-retry+breaker+no-abort, got %+v", s, d)
		}
	}
}

func TestClassify_InvalidRequestAborts(t *testing.T) {
	d := Classify(up(400, "invalid_request_error"))
	if d.RetrySameChannel || d.CountToBreaker || !d.AbortAll {
		t.Fatalf("400 invalid_request want abort-all, no breaker, got %+v", d)
	}
}

func TestClassify_WrittenAndCanceledAbort(t *testing.T) {
	dw := Classify(state.AttemptResult{Written: true, Err: errors.New("mid-stream")})
	if dw.RetrySameChannel || dw.CountToBreaker || !dw.AbortAll {
		t.Fatalf("written want abort-all no breaker, got %+v", dw)
	}
	dc := Classify(state.AttemptResult{Err: context.Canceled})
	if dc.RetrySameChannel || dc.CountToBreaker || !dc.AbortAll {
		t.Fatalf("canceled want abort-all no breaker, got %+v", dc)
	}
}

func TestClassify_OtherClientErrorFailover(t *testing.T) {
	d := Classify(up(404, ""))
	if d.RetrySameChannel || d.CountToBreaker || d.AbortAll {
		t.Fatalf("other 4xx want failover only (all false), got %+v", d)
	}
}

func TestClassify_SuccessNoAction(t *testing.T) {
	d := Classify(state.AttemptResult{})
	if d.RetrySameChannel || d.CountToBreaker || d.AbortAll {
		t.Fatalf("success want all false, got %+v", d)
	}
}
