package tunnel

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/stretchr/testify/require"
)

func TestNormalizeV1Limits(t *testing.T) {
	valid := Limits{MaxMetadataBytes: math.MaxInt64, MaxDataBytes: math.MaxInt64,
		InitialStreamWindow: MaxV1StreamWindowBytes, MaxQueuedSessionBytes: MaxV1SessionQueueBytes,
		MaxConcurrentStreams: MaxV1ConcurrentStreams}
	got, err := NormalizeV1Limits(valid)
	require.NoError(t, err)
	require.EqualValues(t, MaxV1PayloadBytes, got.MaxMetadataBytes)
	require.EqualValues(t, MaxV1PayloadBytes, got.MaxDataBytes)

	invalid := []Limits{
		{},
		{MaxMetadataBytes: -1, MaxDataBytes: 1, InitialStreamWindow: 1, MaxQueuedSessionBytes: 1, MaxConcurrentStreams: 1},
		{MaxMetadataBytes: 1, MaxDataBytes: 0, InitialStreamWindow: 1, MaxQueuedSessionBytes: 1, MaxConcurrentStreams: 1},
		{MaxMetadataBytes: 1, MaxDataBytes: 1, InitialStreamWindow: MaxV1StreamWindowBytes + 1, MaxQueuedSessionBytes: 1, MaxConcurrentStreams: 1},
		{MaxMetadataBytes: 1, MaxDataBytes: 1, InitialStreamWindow: 1, MaxQueuedSessionBytes: MaxV1SessionQueueBytes + 1, MaxConcurrentStreams: 1},
		{MaxMetadataBytes: 1, MaxDataBytes: 1, InitialStreamWindow: 1, MaxQueuedSessionBytes: 1, MaxConcurrentStreams: MaxV1ConcurrentStreams + 1},
	}
	for _, limits := range invalid {
		_, err := NormalizeV1Limits(limits)
		require.ErrorIs(t, err, ErrInvalidLimits)
	}
}

func TestMetadataRoundTripsEveryType(t *testing.T) {
	t.Parallel()

	tests := map[string]func(t *testing.T){
		"Open": func(t *testing.T) {
			want := Open{
				Method: "POST", Path: "/v1/chat/completions",
				Header:     map[string][]string{"Content-Type": {"application/json"}},
				BodyLength: 123, RemainingNanos: 456, RequestID: "req-1",
				SourceAgentID: "agent-a", TargetAgentID: "agent-b", RouteID: 42,
				Hop: 1, ResponseWindow: 65536, Purpose: StreamPurposeConnectivityProbe,
			}
			requireMetadataRoundTrip(t, want)
		},
		"Ready": func(t *testing.T) {
			requireMetadataRoundTrip(t, Ready{RequestWindow: 65536})
		},
		"Headers": func(t *testing.T) {
			requireMetadataRoundTrip(t, Headers{
				StatusCode: 201,
				Header:     map[string][]string{"Content-Type": {"application/json"}},
				Trailer:    map[string][]string{"X-Checksum": {"abc"}},
			})
		},
		"Reset": func(t *testing.T) {
			requireMetadataRoundTrip(t, Reset{Code: ErrorCodeRelayProtocol, Stage: "request", Committed: true})
		},
		"WindowUpdate": func(t *testing.T) {
			requireMetadataRoundTrip(t, WindowUpdate{Bytes: 32768})
		},
		"Hello": func(t *testing.T) {
			requireMetadataRoundTrip(t, Hello{Nonce: "nonce", DesiredGeneration: 7})
		},
		"Welcome": func(t *testing.T) {
			requireMetadataRoundTrip(t, Welcome{
				NonceProof: "proof", MasterInstanceID: "master-1", SessionGeneration: 8,
				Capabilities: []string{"relay-v1"}, Limits: testLimits(),
			})
		},
	}

	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			test(t)
		})
	}
}

func TestMetadataJSONFieldNamesAreStable(t *testing.T) {
	t.Parallel()

	open := Open{
		Method: "GET", Path: "/", Header: map[string][]string{}, BodyLength: -1,
		RemainingNanos: 2, RequestID: "r", SourceAgentID: "a", TargetAgentID: "b",
		RouteID: 3, Hop: 4, ResponseWindow: 5,
	}
	payload, err := EncodeMetadata(open, MaxMetadataBytes)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"method":"GET","path":"/","header":{},"body_length":-1,
		"remaining_nanos":2,"request_id":"r","source_agent_id":"a",
		"target_agent_id":"b","route_id":3,"hop":4,"response_window":5
	}`, string(payload))

	headersPayload, err := EncodeMetadata(Headers{
		StatusCode: 204,
		Header:     map[string][]string{},
	}, MaxMetadataBytes)
	require.NoError(t, err)
	require.JSONEq(t, `{"status_code":204,"header":{}}`, string(headersPayload))
	require.NotContains(t, string(headersPayload), "trailer")
}

func TestBoundAttemptOpenJSONAndFrameRoundTrip(t *testing.T) {
	t.Parallel()

	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourcePrivate, ID: 17},
			RealModel: "provider-model", Mode: attemptwire.ModePassthrough,
		},
		RequestPath: "/v1/responses/resp_123",
	}
	want := Open{
		Method: "POST", Path: attemptwire.EndpointPath, Header: map[string][]string{},
		TargetAgentID: "target-a", RouteID: 0, ResponseWindow: 1024, Attempt: &meta,
	}
	payload, err := EncodeMetadata(want, MaxMetadataBytes)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"method":"POST","path":"/internal/agent/attempt","header":{},
		"body_length":0,"remaining_nanos":0,"request_id":"",
		"source_agent_id":"","target_agent_id":"target-a","route_id":0,
		"hop":0,"response_window":1024,"attempt":{"attempt":{"channel":{
		"source":"private","id":17},"real_model":"provider-model",
		"mode":"passthrough"},"request_path":"/v1/responses/resp_123"}
	}`, string(payload))

	encoded, err := Encode(Frame{
		Version: ProtocolVersion, Type: FrameOpen, StreamID: StreamID{1}, Sequence: 1, Payload: payload,
	}, testLimits())
	require.NoError(t, err)
	frame, err := Decode(encoded, testLimits())
	require.NoError(t, err)
	var got Open
	require.NoError(t, DecodeMetadata(frame.Payload, &got, MaxMetadataBytes))
	require.Equal(t, want, got)
	require.NotSame(t, want.Attempt, got.Attempt)

	got.Attempt.Attempt.RealModel = "mutated"
	require.Equal(t, "provider-model", want.Attempt.Attempt.RealModel)
}

func TestPlainOpenOmitsBoundAttemptMetadata(t *testing.T) {
	t.Parallel()

	payload, err := EncodeMetadata(Open{
		Method: "GET", Path: "/ping", TargetAgentID: "target-a", ResponseWindow: 1,
	}, MaxMetadataBytes)
	require.NoError(t, err)
	require.NotContains(t, string(payload), `"attempt"`)

	var got Open
	require.NoError(t, DecodeMetadata(payload, &got, MaxMetadataBytes))
	require.Nil(t, got.Attempt)
}

func TestMetadataWireShapesAreStable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata any
		want     string
	}{
		{name: "Ready", metadata: Ready{RequestWindow: 1}, want: `{"request_window":1}`},
		{name: "Headers with trailers", metadata: Headers{
			StatusCode: 200,
			Header:     map[string][]string{"X-H": {"h"}},
			Trailer:    map[string][]string{"X-T": {"t"}},
		}, want: `{"status_code":200,"header":{"X-H":["h"]},"trailer":{"X-T":["t"]}}`},
		{name: "Reset", metadata: Reset{
			Code: ErrorCodeRequestCancelled, Stage: "response", Committed: true,
		}, want: `{"code":"request_cancelled","stage":"response","committed":true}`},
		{name: "WindowUpdate", metadata: WindowUpdate{Bytes: 2}, want: `{"bytes":2}`},
		{name: "Hello", metadata: Hello{
			Nonce: "n", DesiredGeneration: 3,
		}, want: `{"nonce":"n","desired_generation":3}`},
		{name: "Welcome", metadata: Welcome{
			NonceProof: "p", MasterInstanceID: "m", SessionGeneration: 4,
			Capabilities: []string{"relay-v1"}, Limits: Limits{
				MaxMetadataBytes: 1, MaxDataBytes: 2, InitialStreamWindow: 3,
				MaxQueuedSessionBytes: 4, MaxConcurrentStreams: 5,
			},
		}, want: `{
			"nonce_proof":"p","master_instance_id":"m","session_generation":4,
			"capabilities":["relay-v1"],"limits":{"max_metadata_bytes":1,
			"max_data_bytes":2,"initial_stream_window":3,
			"max_queued_session_bytes":4,"max_concurrent_streams":5}
		}`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			payload, err := EncodeMetadata(tt.metadata, MaxMetadataBytes)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(payload))
		})
	}
}

func TestLimitsJSONFieldNamesAreStable(t *testing.T) {
	t.Parallel()

	payload, err := EncodeMetadata(testLimits(), MaxMetadataBytes)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"max_metadata_bytes":65536,
		"max_data_bytes":65536,
		"initial_stream_window":262144,
		"max_queued_session_bytes":4194304,
		"max_concurrent_streams":128
	}`, string(payload))
}

func TestMetadataEnforcesEncodedSizeBoundary(t *testing.T) {
	t.Parallel()

	const envelopeBytes = len(`{"value":""}`)
	atLimit := struct {
		Value string `json:"value"`
	}{Value: string(bytes.Repeat([]byte{'a'}, MaxMetadataBytes-envelopeBytes))}

	payload, err := EncodeMetadata(atLimit, MaxMetadataBytes)
	require.NoError(t, err)
	require.Len(t, payload, MaxMetadataBytes)

	var decoded struct {
		Value string `json:"value"`
	}
	err = DecodeMetadata(payload, &decoded, MaxMetadataBytes)
	require.NoError(t, err)
	require.Equal(t, atLimit, decoded)

	atLimit.Value += "a"
	_, err = EncodeMetadata(atLimit, MaxMetadataBytes)
	require.ErrorIs(t, err, ErrMetadataTooLarge)

	var raw json.RawMessage
	err = DecodeMetadata(make([]byte, MaxMetadataBytes+1), &raw, MaxMetadataBytes)
	require.ErrorIs(t, err, ErrMetadataTooLarge)
}

func TestMetadataHonorsSmallerCallerLimit(t *testing.T) {
	t.Parallel()

	const (
		callerLimit   = int64(32)
		envelopeBytes = len(`{"value":""}`)
	)
	tests := []struct {
		name      string
		bytes     int
		wantError error
	}{
		{name: "below boundary", bytes: int(callerLimit) - envelopeBytes - 1},
		{name: "at boundary", bytes: int(callerLimit) - envelopeBytes},
		{name: "above boundary", bytes: int(callerLimit) - envelopeBytes + 1, wantError: ErrMetadataTooLarge},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := struct {
				Value string `json:"value"`
			}{Value: string(bytes.Repeat([]byte{'a'}, tt.bytes))}
			payload, err := EncodeMetadata(want, callerLimit)
			if tt.wantError != nil {
				require.ErrorIs(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.LessOrEqual(t, int64(len(payload)), callerLimit)

			var got struct {
				Value string `json:"value"`
			}
			err = DecodeMetadata(payload, &got, callerLimit)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}

	overLimitPayload := bytes.Repeat([]byte{' '}, int(callerLimit)+1)
	var raw json.RawMessage
	err := DecodeMetadata(overLimitPayload, &raw, callerLimit)
	require.ErrorIs(t, err, ErrMetadataTooLarge)
}

func TestMetadataClampsCallerLimitToV1HardLimit(t *testing.T) {
	t.Parallel()

	const (
		callerLimit   = 2 * MaxMetadataBytes
		envelopeBytes = len(`{"value":""}`)
	)
	value := struct {
		Value string `json:"value"`
	}{Value: string(bytes.Repeat([]byte{'a'}, MaxMetadataBytes+1-envelopeBytes))}

	payload, err := json.Marshal(value)
	require.NoError(t, err)
	require.Len(t, payload, MaxMetadataBytes+1)

	_, err = EncodeMetadata(value, callerLimit)
	require.ErrorIs(t, err, ErrMetadataTooLarge)

	var got struct {
		Value string `json:"value"`
	}
	err = DecodeMetadata(payload, &got, callerLimit)
	require.ErrorIs(t, err, ErrMetadataTooLarge)
}

func TestMetadataRejectsInvalidCallerLimitsBeforeOtherValidation(t *testing.T) {
	t.Parallel()

	for _, limit := range []int64{0, -1} {
		_, err := EncodeMetadata(make(chan int), limit)
		require.ErrorIs(t, err, ErrInvalidLimits)

		err = DecodeMetadata[Open](make([]byte, MaxMetadataBytes+1), nil, limit)
		require.ErrorIs(t, err, ErrInvalidLimits)
	}
}

func TestMetadataDecodeRejectsNilDestinationAfterSizeValidation(t *testing.T) {
	t.Parallel()

	err := DecodeMetadata[Open]([]byte(`{}`), nil, MaxMetadataBytes)
	require.ErrorIs(t, err, ErrNilMetadataDestination)

	err = DecodeMetadata[Open](make([]byte, MaxMetadataBytes+1), nil, MaxMetadataBytes)
	require.ErrorIs(t, err, ErrMetadataTooLarge)
}

func TestMetadataEncodeMarshalFailureIsStableAndSanitized(t *testing.T) {
	t.Parallel()

	_, err := EncodeMetadata(make(chan string), MaxMetadataBytes)
	require.ErrorIs(t, err, ErrMalformedMetadata)
	require.NotContains(t, err.Error(), "chan")
}

func TestMetadataRejectsMalformedJSONWithoutLeakingIt(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-ticket"
	tests := [][]byte{
		nil,
		[]byte(`{"method":`),
		[]byte(`{} trailing-` + secret),
	}

	for _, payload := range tests {
		var metadata Open
		err := DecodeMetadata(payload, &metadata, MaxMetadataBytes)
		require.ErrorIs(t, err, ErrMalformedMetadata)
		require.NotContains(t, err.Error(), secret)
		if len(payload) > 0 {
			require.NotContains(t, err.Error(), string(payload))
		}
	}
}

func TestNewStreamIDReturnsNonZeroRandomValues(t *testing.T) {
	t.Parallel()

	first, err := NewStreamID()
	require.NoError(t, err)
	require.NotEqual(t, StreamID{}, first)

	second, err := NewStreamID()
	require.NoError(t, err)
	require.NotEqual(t, StreamID{}, second)
	require.NotEqual(t, first, second)
}

func TestStableProtocolErrorCodes(t *testing.T) {
	t.Parallel()

	require.Equal(t, "relay_protocol", ErrorCodeRelayProtocol)
	require.Equal(t, "relay_overloaded", ErrorCodeRelayOverloaded)
	require.Equal(t, "stream_window_timeout", ErrorCodeStreamWindowTimeout)
	require.Equal(t, "request_cancelled", ErrorCodeRequestCancelled)
	require.Equal(t, "request_deadline", ErrorCodeRequestDeadline)
	require.Equal(t, "session_closed", ErrorCodeSessionClosed)
	require.Equal(t, "drain_timeout", ErrorCodeDrainTimeout)
	require.Equal(t, "relay_fallback_disabled", ErrorCodeRelayFallbackDisabled)
}

func TestProtocolErrorsAreStable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		message string
	}{
		{ErrShortHeader, "tunnel: frame header too short"},
		{ErrUnsupportedVersion, "tunnel: unsupported protocol version"},
		{ErrUnknownFrameType, "tunnel: unknown frame type"},
		{ErrUnknownFlags, "tunnel: unknown frame flags"},
		{ErrPayloadLengthMismatch, "tunnel: payload length mismatch"},
		{ErrPayloadTooLarge, "tunnel: frame payload too large"},
		{ErrZeroStreamID, "tunnel: stream ID is zero"},
		{ErrSequenceOverflow, "tunnel: sequence overflow"},
		{ErrMetadataTooLarge, "tunnel: metadata too large"},
		{ErrMalformedMetadata, "tunnel: malformed metadata"},
		{ErrInvalidLimits, "tunnel: invalid limits"},
		{ErrNilMetadataDestination, "tunnel: nil metadata destination"},
		{ErrStreamIDGeneration, "tunnel: stream ID generation failed"},
	}

	for _, tt := range tests {
		require.EqualError(t, tt.err, tt.message)
	}
}

func TestCommitStateValuesAreStable(t *testing.T) {
	t.Parallel()

	require.Equal(t, CommitState(0), PreCommit)
	require.Equal(t, CommitState(1), CommitUncertain)
	require.Equal(t, CommitState(2), Committed)
}

func TestTrailersMetadataRoundTripAndEmptyEndCompatibility(t *testing.T) {
	want := Trailers{
		Header:  map[string][]string{"X-Usage": {"tokens=7"}},
		Dynamic: []string{"X-Usage"},
	}
	requireMetadataRoundTrip(t, want)

	limits := Limits{MaxMetadataBytes: MaxMetadataBytes, MaxDataBytes: MaxV1PayloadBytes}
	frame := Frame{Version: ProtocolVersion, Type: FrameEnd, StreamID: [16]byte{1}, Sequence: 9}
	encoded, err := Encode(frame, limits)
	require.NoError(t, err)
	decoded, err := Decode(encoded, limits)
	require.NoError(t, err)
	require.Empty(t, decoded.Payload)
}

func requireMetadataRoundTrip[T any](t *testing.T, want T) {
	t.Helper()
	payload, err := EncodeMetadata(want, MaxMetadataBytes)
	require.NoError(t, err)
	var got T
	err = DecodeMetadata(payload, &got, MaxMetadataBytes)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
