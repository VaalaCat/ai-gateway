package attemptproxy

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetaUsesControlJSON(t *testing.T) {
	meta := validMeta()
	raw, err := EncodeMeta(meta)
	require.NoError(t, err)
	require.True(t, json.Valid([]byte(raw)))
	require.JSONEq(t, `{
		"attempt": {
			"channel": {"source": "admin", "id": 1},
			"real_model": "gpt-4o",
			"mode": "native"
		},
		"request_path": "/v1/responses"
	}`, raw)
}

func TestResultTrailerUsesRawURLBase64(t *testing.T) {
	result := validResult()
	raw, err := EncodeResult(result)
	require.NoError(t, err)
	require.NotContains(t, raw, "=")
	require.NotContains(t, raw, "+")
	require.NotContains(t, raw, "/")

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	var got AttemptProxyResult
	require.NoError(t, json.Unmarshal(decoded, &got))
	require.Equal(t, result, got)

	got, err = DecodeResult(raw)
	require.NoError(t, err)
	require.Equal(t, result, got)
}

func TestAttemptProxyResultDispatchesJSONCompatibility(t *testing.T) {
	t.Run("nonzero round trip", func(t *testing.T) {
		result := AttemptProxyResult{Kind: ResultSucceeded, Dispatches: 3, ProviderDispatched: true}
		raw, err := EncodeResult(result)
		require.NoError(t, err)
		got, err := DecodeResult(raw)
		require.NoError(t, err)
		require.Equal(t, 3, got.Dispatches)
		require.True(t, got.ProviderDispatched)
	})

	t.Run("zero is omitted and decodes as zero", func(t *testing.T) {
		result := AttemptProxyResult{Kind: ResultExecutionRejected}
		raw, err := EncodeResult(result)
		require.NoError(t, err)
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		require.NoError(t, err)
		require.NotContains(t, string(decoded), `"dispatches"`)
		got, err := DecodeResult(raw)
		require.NoError(t, err)
		require.Zero(t, got.Dispatches)
	})

	t.Run("old target payload remains compatible", func(t *testing.T) {
		raw := base64.RawURLEncoding.EncodeToString([]byte(`{"kind":"succeeded","provider_dispatched":true}`))
		got, err := DecodeResult(raw)
		require.NoError(t, err)
		require.Zero(t, got.Dispatches)
		require.True(t, got.ProviderDispatched)
	})

	t.Run("negative count is invalid", func(t *testing.T) {
		result := AttemptProxyResult{Kind: ResultSucceeded, Dispatches: -1, ProviderDispatched: true}
		require.ErrorIs(t, result.Validate(), ErrInvalidContract)
		_, err := EncodeResult(result)
		require.ErrorIs(t, err, ErrInvalidContract)
	})

	t.Run("nonzero count requires provider dispatched", func(t *testing.T) {
		result := AttemptProxyResult{Kind: ResultSucceeded, Dispatches: 1}
		require.ErrorIs(t, result.Validate(), ErrInvalidContract)
		_, err := EncodeResult(result)
		require.ErrorIs(t, err, ErrInvalidContract)
	})
}

func TestEncodeResultAcceptsExactWireLimit(t *testing.T) {
	result := resultWithJSONSize(t, MaxResultWireBytes*3/4)
	raw, err := EncodeResult(result)
	require.NoError(t, err)
	require.Len(t, raw, MaxResultWireBytes)

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	require.Len(t, decoded, MaxResultWireBytes*3/4)
	got, err := DecodeResult(raw)
	require.NoError(t, err)
	require.Equal(t, result, got)
}

func TestEncodeResultJSONUsesSharedTraceTrimming(t *testing.T) {
	t.Run("small result remains unchanged", func(t *testing.T) {
		result := validResult()
		result.Trace = &AttemptTraceWire{InboundPath: "/v1/responses", InboundBody: "small"}

		raw, err := EncodeResultJSON(result)
		require.NoError(t, err)
		require.LessOrEqual(t, len(raw), MaxResultWireBytes)
		var got AttemptProxyResult
		require.NoError(t, json.Unmarshal(raw, &got))
		require.Equal(t, result, got)
	})

	t.Run("oversized trace is progressively trimmed", func(t *testing.T) {
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
		require.Equal(t, originalTrace, *result.Trace, "raw encoding must not mutate its input")
		var got AttemptProxyResult
		require.NoError(t, json.Unmarshal(raw, &got))
		require.Equal(t, 3, got.Dispatches)
		require.True(t, got.ProviderDispatched)
		require.True(t, got.PlanAdvanceAllowed)
		require.Equal(t, &AttemptTraceWire{
			InboundPath: "/v1/responses", OutboundPath: "/provider/responses",
			UpstreamStatus: 500, ErrorStage: "upstream_status",
		}, got.Trace)
	})

	t.Run("exact raw limit is accepted", func(t *testing.T) {
		result := resultWithJSONSize(t, MaxResultWireBytes)
		raw, err := EncodeResultJSON(result)
		require.NoError(t, err)
		require.Len(t, raw, MaxResultWireBytes)
	})

	t.Run("oversized scalars remain an error", func(t *testing.T) {
		result := resultWithJSONSize(t, MaxResultWireBytes+1)
		_, err := EncodeResultJSON(result)
		require.ErrorIs(t, err, ErrResultTooLarge)
	})
}

func TestEncodeResultTrimsTraceBodiesInOrder(t *testing.T) {
	body := strings.Repeat("x", 20*1024)
	result := validResult()
	result.Trace = &AttemptTraceWire{
		InboundPath:        "/v1/responses",
		OutboundPath:       "/provider/responses",
		InboundBody:        body,
		OutboundBody:       body,
		ClientResponseBody: body,
		ResponseBody:       body,
		UpstreamStatus:     200,
		ErrorStage:         "response",
	}
	originalTrace := *result.Trace

	raw, err := EncodeResult(result)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), MaxResultWireBytes)
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	require.LessOrEqual(t, len(decoded), MaxResultWireBytes*3/4)
	require.Equal(t, originalTrace, *result.Trace, "EncodeResult must not mutate its input")

	got, err := DecodeResult(raw)
	require.NoError(t, err)
	wantScalars := result
	wantScalars.Trace = nil
	gotScalars := got
	gotScalars.Trace = nil
	require.Equal(t, wantScalars, gotScalars)
	require.NotNil(t, got.Trace)
	require.Empty(t, got.Trace.InboundBody)
	require.Empty(t, got.Trace.OutboundBody)
	require.Empty(t, got.Trace.ClientResponseBody)
	require.Equal(t, body, got.Trace.ResponseBody)
}

func TestEncodeResultKeepsOnlyTraceSummaryWhenHeadersExceedLimit(t *testing.T) {
	result := validResult()
	result.Trace = &AttemptTraceWire{
		InboundPath:     "/v1/responses",
		OutboundPath:    "/provider/responses",
		InboundHeaders:  strings.Repeat("i", MaxResultWireBytes),
		OutboundHeaders: strings.Repeat("o", MaxResultWireBytes),
		UpstreamStatus:  429,
		ErrorStage:      "provider_response",
	}

	raw, err := EncodeResult(result)
	require.NoError(t, err)
	got, err := DecodeResult(raw)
	require.NoError(t, err)
	require.Equal(t, &AttemptTraceWire{
		InboundPath:    "/v1/responses",
		OutboundPath:   "/provider/responses",
		UpstreamStatus: 429,
		ErrorStage:     "provider_response",
	}, got.Trace)
}

func TestEncodeResultDropsTraceWhenSummaryExceedsLimit(t *testing.T) {
	result := validResult()
	result.Trace = &AttemptTraceWire{
		InboundPath:    strings.Repeat("p", MaxResultWireBytes),
		OutboundPath:   "/provider/responses",
		UpstreamStatus: 503,
		ErrorStage:     "provider_response",
	}

	raw, err := EncodeResult(result)
	require.NoError(t, err)
	got, err := DecodeResult(raw)
	require.NoError(t, err)
	require.Nil(t, got.Trace)
	wantScalars := result
	wantScalars.Trace = nil
	require.Equal(t, wantScalars, got)
}

func TestEncodeResultRejectsOversizedScalars(t *testing.T) {
	result := validResult()
	result.ErrorMessage = strings.Repeat("e", MaxResultWireBytes)
	_, err := EncodeResult(result)
	require.ErrorIs(t, err, ErrResultTooLarge)
}

func TestDecodeResultRejectsCorruptBase64(t *testing.T) {
	_, err := DecodeResult("not%%%base64")
	require.ErrorIs(t, err, ErrInvalidContract)
	require.NotContains(t, err.Error(), "not%%%base64")
}

func TestDecodeResultRejectsOversizedBase64BeforeDecode(t *testing.T) {
	tooLarge := strings.Repeat("A", MaxResultWireBytes+1)
	_, err := DecodeResult(tooLarge)
	require.ErrorIs(t, err, ErrResultTooLarge)
}

func TestDecodeResultRejectsOversizedPayloadAfterDecode(t *testing.T) {
	_, err := decodeResultJSON([]byte(strings.Repeat("x", MaxResultWireBytes*3/4+1)))
	require.ErrorIs(t, err, ErrResultTooLarge)
}

func TestDecodeResultRejectsMissingKind(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"http_status":502}`))
	_, err := DecodeResult(raw)
	require.ErrorIs(t, err, ErrInvalidContract)
}

func validMeta() AttemptProxyMeta {
	return AttemptProxyMeta{
		Attempt: BoundAttempt{
			Channel: ChannelRef{Source: SourceAdmin, ID: 1}, RealModel: "gpt-4o", Mode: ModeNative,
		},
		RequestPath: "/v1/responses",
	}
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
