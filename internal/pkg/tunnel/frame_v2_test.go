package tunnel

import (
	"bytes"
	"testing"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/stretchr/testify/require"
)

func TestV2ProtocolVersionAndFrameTypeValuesAreStable(t *testing.T) {
	require.Equal(t, uint8(2), ProtocolVersion)
	require.Equal(t, Type(1), FrameOpen)
	require.Equal(t, Type(2), FrameReady)
	require.Equal(t, Type(3), FrameCommit)
	require.Equal(t, Type(4), FrameCommitted)
	require.Equal(t, Type(5), FrameRequestData)
	require.Equal(t, Type(6), FrameRequestEnd)
	require.Equal(t, Type(7), FrameHeaders)
	require.Equal(t, Type(8), FrameResponseData)
	require.Equal(t, Type(9), FrameAttemptResult)
	require.Equal(t, Type(10), FrameEnd)
	require.Equal(t, Type(11), FrameCancel)
	require.Equal(t, Type(12), FrameReset)
	require.Equal(t, Type(13), FrameWindowUpdate)
}

func TestV2RejectsV1Frames(t *testing.T) {
	frame := Frame{Version: 1, Type: FrameOpen, StreamID: testStreamID()}
	_, err := Encode(frame, testLimits())
	require.ErrorIs(t, err, ErrUnsupportedVersion)

	raw := validEncodedFrame(t, FrameOpen, nil)
	raw[0] = 1
	_, err = Decode(raw, testLimits())
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}

func TestAttemptResultFrameRoundTripAndWireBoundary(t *testing.T) {
	limits := testLimits()
	payload := bytes.Repeat([]byte("x"), attemptwire.MaxResultWireBytes)
	want := Frame{
		Version: ProtocolVersion, Type: FrameAttemptResult, StreamID: testStreamID(), Sequence: 9, Payload: payload,
	}

	raw, err := Encode(want, limits)
	require.NoError(t, err)
	got, err := Decode(raw, limits)
	require.NoError(t, err)
	require.Equal(t, want, got)

	want.Payload = append(want.Payload, 'x')
	_, err = Encode(want, limits)
	require.ErrorIs(t, err, ErrPayloadTooLarge)
}

func TestAttemptResultFrameUsesNegotiatedMetadataLimitWithoutShrinkingData(t *testing.T) {
	limits := testLimits()
	limits.MaxMetadataBytes = 4 * 1024
	limits.MaxDataBytes = 16 * 1024
	id := testStreamID()

	exact := Frame{
		Version: ProtocolVersion, Type: FrameAttemptResult, StreamID: id,
		Payload: bytes.Repeat([]byte("r"), int(limits.MaxMetadataBytes)),
	}
	raw, err := Encode(exact, limits)
	require.NoError(t, err)
	decoded, err := Decode(raw, limits)
	require.NoError(t, err)
	require.Equal(t, exact, decoded)

	tooLarge := exact
	tooLarge.Payload = append(tooLarge.Payload, 'r')
	_, err = Encode(tooLarge, limits)
	require.ErrorIs(t, err, ErrPayloadTooLarge)
	tooLargeRaw := append(append([]byte(nil), raw...), 'r')
	tooLargeRaw[27]++
	_, err = Decode(tooLargeRaw, limits)
	require.ErrorIs(t, err, ErrPayloadTooLarge)

	data := Frame{
		Version: ProtocolVersion, Type: FrameResponseData, StreamID: id,
		Payload: bytes.Repeat([]byte("d"), int(limits.MaxDataBytes)),
	}
	dataRaw, err := Encode(data, limits)
	require.NoError(t, err)
	_, err = Decode(dataRaw, limits)
	require.NoError(t, err)
}
