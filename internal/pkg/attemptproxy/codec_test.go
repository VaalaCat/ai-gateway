package attemptproxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResultJSONRoundTripPreservesExplicitResult(t *testing.T) {
	want := validResult()

	raw, err := EncodeResultJSON(want)
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	require.LessOrEqual(t, len(raw), MaxResultWireBytes)

	got, err := DecodeResultJSON(raw)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResultJSONRoundTripPreservesHeaderFailureFallback(t *testing.T) {
	want := validResult()
	want.Trace = &AttemptTraceWire{
		InboundPath: "/v1/responses", InboundHeaders: `{"X-In":["kept"]}`,
		FailureFallback: &AttemptTraceBodyWire{
			InboundBody: "inbound", OutboundBody: "outbound",
			ResponseBody: "provider-raw", ClientResponseBody: "client-encoded",
		},
	}

	raw, err := EncodeResultJSON(want)
	require.NoError(t, err)
	got, err := DecodeResultJSON(raw)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResultJSONGraduallyTrimsHeaderFailureFallback(t *testing.T) {
	const limit = 1024
	large := strings.Repeat("x", 400)
	result := validResult()
	result.Trace = &AttemptTraceWire{
		InboundPath: "/v1/responses", OutboundPath: "/v1/chat/completions",
		FailureFallback: &AttemptTraceBodyWire{
			InboundBody: large, OutboundBody: large,
			ResponseBody: large, ClientResponseBody: large,
		},
	}
	original := *result.Trace.FailureFallback

	raw, err := EncodeResultJSONWithin(result, limit)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), limit)
	require.Equal(t, original, *result.Trace.FailureFallback, "encoding mutated caller fallback")
	got, err := DecodeResultJSONWithin(raw, limit)
	require.NoError(t, err)
	require.NotNil(t, got.Trace)
	require.NotNil(t, got.Trace.FailureFallback, "fallback marker was dropped before trace summary was required")
	require.Empty(t, got.Trace.FailureFallback.InboundBody)
	require.Empty(t, got.Trace.FailureFallback.OutboundBody)
	require.Equal(t, large, got.Trace.FailureFallback.ResponseBody)
	require.Empty(t, got.Trace.FailureFallback.ClientResponseBody)
}

func TestResultJSONKeepsFallbackMarkerUntilMinimumDoesNotFit(t *testing.T) {
	large := strings.Repeat("x", 256)
	result := validResult()
	result.Trace = &AttemptTraceWire{
		InboundPath: "/v1/responses",
		FailureFallback: &AttemptTraceBodyWire{
			InboundBody: large, OutboundBody: large,
			ResponseBody: large, ClientResponseBody: large,
		},
	}
	minimum := result
	minimumTrace := *result.Trace
	minimumTrace.FailureFallback = &AttemptTraceBodyWire{}
	minimum.Trace = &minimumTrace
	minimumRaw, err := json.Marshal(minimum)
	require.NoError(t, err)

	raw, err := EncodeResultJSONWithin(result, len(minimumRaw))
	require.NoError(t, err)
	require.Len(t, raw, len(minimumRaw))
	got, err := DecodeResultJSONWithin(raw, len(minimumRaw))
	require.NoError(t, err)
	require.NotNil(t, got.Trace.FailureFallback)
	require.Equal(t, &AttemptTraceBodyWire{}, got.Trace.FailureFallback)

	raw, err = EncodeResultJSONWithin(result, len(minimumRaw)-1)
	require.NoError(t, err)
	got, err = DecodeResultJSONWithin(raw, len(minimumRaw)-1)
	require.NoError(t, err)
	require.Nil(t, got.Trace.FailureFallback)
}

func TestResultJSONPreservesDispatchCompatibility(t *testing.T) {
	t.Run("nonzero round trip", func(t *testing.T) {
		result := AttemptProxyResult{Kind: ResultSucceeded, Dispatches: 3, ProviderDispatched: true}
		raw, err := EncodeResultJSON(result)
		require.NoError(t, err)
		got, err := DecodeResultJSON(raw)
		require.NoError(t, err)
		require.Equal(t, 3, got.Dispatches)
		require.True(t, got.ProviderDispatched)
	})

	t.Run("zero is omitted", func(t *testing.T) {
		raw, err := EncodeResultJSON(AttemptProxyResult{Kind: ResultExecutionRejected})
		require.NoError(t, err)
		require.NotContains(t, string(raw), `"dispatches"`)
	})

	t.Run("old payload remains compatible", func(t *testing.T) {
		got, err := DecodeResultJSON([]byte(`{"kind":"succeeded","provider_dispatched":true}`))
		require.NoError(t, err)
		require.Zero(t, got.Dispatches)
		require.True(t, got.ProviderDispatched)
	})
}

func TestResultJSONTrimsTraceWithoutMutatingInput(t *testing.T) {
	large := strings.Repeat("x", MaxResultWireBytes)
	result := AttemptProxyResult{
		Kind: ResultProviderFailed, Dispatches: 3, ProviderDispatched: true,
		ProviderResultKnown: true, PlanAdvanceAllowed: true, HTTPStatus: 500,
		Trace: &AttemptTraceWire{
			InboundPath: "/v1/responses", OutboundPath: "/provider/responses",
			InboundHeaders: large, InboundBody: large, UpstreamStatus: 500, ErrorStage: "upstream_status",
		},
	}
	originalTrace := *result.Trace

	raw, err := EncodeResultJSON(result)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), MaxResultWireBytes)
	require.Equal(t, originalTrace, *result.Trace)

	got, err := DecodeResultJSON(raw)
	require.NoError(t, err)
	require.Equal(t, 3, got.Dispatches)
	require.True(t, got.PlanAdvanceAllowed)
	require.Equal(t, &AttemptTraceWire{
		InboundPath: "/v1/responses", OutboundPath: "/provider/responses",
		UpstreamStatus: 500, ErrorStage: "upstream_status",
	}, got.Trace)
}

func TestResultJSONWireBoundaries(t *testing.T) {
	t.Run("exact limit", func(t *testing.T) {
		want := resultWithJSONSize(t, MaxResultWireBytes)
		raw, err := EncodeResultJSON(want)
		require.NoError(t, err)
		require.Len(t, raw, MaxResultWireBytes)
		got, err := DecodeResultJSON(raw)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("fallback exact limit", func(t *testing.T) {
		want := resultWithFallbackJSONSize(t, MaxResultWireBytes)
		raw, err := EncodeResultJSON(want)
		require.NoError(t, err)
		require.Len(t, raw, MaxResultWireBytes)
		got, err := DecodeResultJSON(raw)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("encode over limit", func(t *testing.T) {
		_, err := EncodeResultJSON(resultWithJSONSize(t, MaxResultWireBytes+1))
		require.ErrorIs(t, err, ErrResultTooLarge)
	})

	t.Run("decode over limit", func(t *testing.T) {
		_, err := DecodeResultJSON([]byte(strings.Repeat("x", MaxResultWireBytes+1)))
		require.ErrorIs(t, err, ErrResultTooLarge)
	})
}

func TestResultJSONNegotiatedWireBoundaries(t *testing.T) {
	const limit = 4 * 1024
	want := resultWithJSONSize(t, limit)

	raw, err := EncodeResultJSONWithin(want, limit)
	require.NoError(t, err)
	require.Len(t, raw, limit)
	got, err := DecodeResultJSONWithin(raw, limit)
	require.NoError(t, err)
	require.Equal(t, want, got)

	_, err = EncodeResultJSONWithin(resultWithJSONSize(t, limit+1), limit)
	require.ErrorIs(t, err, ErrResultTooLarge)
	_, err = DecodeResultJSONWithin(append(raw, 'x'), limit)
	require.ErrorIs(t, err, ErrResultTooLarge)

	for _, invalidLimit := range []int{0, -1, MaxResultWireBytes + 1} {
		_, err = EncodeResultJSONWithin(want, invalidLimit)
		require.ErrorIs(t, err, ErrResultTooLarge)
		_, err = DecodeResultJSONWithin(raw, invalidLimit)
		require.ErrorIs(t, err, ErrResultTooLarge)
	}
}

func TestResultJSONRejectsInvalidContracts(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
	}{
		{name: "empty", payload: ""},
		{name: "malformed", payload: `{"kind":`},
		{name: "missing kind", payload: `{}`},
		{name: "invalid kind", payload: `{"kind":"unknown"}`},
		{name: "negative dispatches", payload: `{"kind":"succeeded","dispatches":-1,"provider_dispatched":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeResultJSON([]byte(test.payload))
			require.ErrorIs(t, err, ErrInvalidContract)
		})
	}

	for _, result := range []AttemptProxyResult{
		{Kind: ResultSucceeded, Dispatches: -1, ProviderDispatched: true},
		{Kind: ResultSucceeded, Dispatches: 1},
	} {
		_, err := EncodeResultJSON(result)
		require.ErrorIs(t, err, ErrInvalidContract)
	}
}

func TestAutoDisableTriggerResultRoundTrip(t *testing.T) {
	for _, trigger := range []ChannelAutoDisableTrigger{
		{Source: SourceAdmin, ChannelID: 7, Revision: 0, Reason: ChannelAutoDisableReasonConsecutiveErrors},
		{Source: SourcePrivate, ChannelID: 7, Revision: 4, Reason: ChannelAutoDisableReasonConsecutiveErrors},
	} {
		t.Run(string(trigger.Source), func(t *testing.T) {
			want := validResult()
			want.AutoDisableTriggers = []ChannelAutoDisableTrigger{trigger}

			raw, err := EncodeResultJSON(want)
			require.NoError(t, err)
			got, err := DecodeResultJSON(raw)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestAutoDisableTriggerRejectsInvalidResultContracts(t *testing.T) {
	valid := ChannelAutoDisableTrigger{
		Source: SourceAdmin, ChannelID: 7, Reason: ChannelAutoDisableReasonConsecutiveErrors,
	}
	tests := []struct {
		name     string
		triggers []ChannelAutoDisableTrigger
	}{
		{name: "zero channel ID", triggers: []ChannelAutoDisableTrigger{{Source: SourceAdmin, Reason: ChannelAutoDisableReasonConsecutiveErrors}}},
		{name: "unknown source", triggers: []ChannelAutoDisableTrigger{{Source: "unknown", ChannelID: 7, Reason: ChannelAutoDisableReasonConsecutiveErrors}}},
		{name: "unknown reason", triggers: []ChannelAutoDisableTrigger{{Source: SourceAdmin, ChannelID: 7, Reason: "unknown"}}},
		{name: "more than result maximum", triggers: []ChannelAutoDisableTrigger{valid, valid}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validResult()
			result.AutoDisableTriggers = test.triggers
			_, err := EncodeResultJSON(result)
			require.ErrorIs(t, err, ErrInvalidContract)
		})
	}
}

func TestAutoDisableTriggerSurvivesResultTraceTrimming(t *testing.T) {
	trigger := ChannelAutoDisableTrigger{
		Source: SourcePrivate, ChannelID: 9, Revision: 4, Reason: ChannelAutoDisableReasonConsecutiveErrors,
	}
	result := validResult()
	result.AutoDisableTriggers = []ChannelAutoDisableTrigger{trigger}
	result.Trace = &AttemptTraceWire{
		InboundPath: "/v1/responses", InboundBody: strings.Repeat("x", MaxResultWireBytes),
	}

	raw, err := EncodeResultJSON(result)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), MaxResultWireBytes)
	got, err := DecodeResultJSON(raw)
	require.NoError(t, err)
	require.Equal(t, []ChannelAutoDisableTrigger{trigger}, got.AutoDisableTriggers)
	require.NotNil(t, got.Trace)
	require.Empty(t, got.Trace.InboundBody)
}

func validResult() AttemptProxyResult {
	return AttemptProxyResult{
		Kind:                ResultSucceeded,
		PromptTokens:        12,
		CompletionTokens:    7,
		CacheReadTokens:     5,
		CacheWriteTokens:    3,
		FirstResponseMs:     123,
		UpstreamModel:       "gpt-4o-2024-08-06",
		TokenSource:         "provider",
		ProviderDispatched:  true,
		ProviderResultKnown: true,
		Written:             true,
		PlanAdvanceAllowed:  true,
		ResponseStarted:     true,
		HTTPStatus:          200,
		ErrorType:           "masked_error",
		ReasonCode:          "masked_reason",
		ErrorMessage:        "masked provider result",
	}
}

func resultWithJSONSize(t *testing.T, size int) AttemptProxyResult {
	t.Helper()
	result := validResult()
	result.ErrorMessage = "x"
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	fixedBytes := len(raw) - len(result.ErrorMessage)
	require.Greater(t, size, fixedBytes)
	result.ErrorMessage = strings.Repeat("x", size-fixedBytes)
	raw, err = json.Marshal(result)
	require.NoError(t, err)
	require.Len(t, raw, size)
	return result
}

func resultWithFallbackJSONSize(t *testing.T, size int) AttemptProxyResult {
	t.Helper()
	result := validResult()
	result.Trace = &AttemptTraceWire{
		InboundPath: "/v1/responses",
		FailureFallback: &AttemptTraceBodyWire{
			InboundBody: "inbound", OutboundBody: "outbound",
			ResponseBody: "provider", ClientResponseBody: "client",
		},
	}
	result.ErrorMessage = "x"
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	fixedBytes := len(raw) - len(result.ErrorMessage)
	require.Greater(t, size, fixedBytes)
	result.ErrorMessage = strings.Repeat("x", size-fixedBytes)
	raw, err = json.Marshal(result)
	require.NoError(t, err)
	require.Len(t, raw, size)
	return result
}
