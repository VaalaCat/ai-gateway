package tunnel

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrameGoldenEncodingUsesNetworkOrderHeader(t *testing.T) {
	t.Parallel()

	streamID := StreamID{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	frame := Frame{
		Version:  1,
		Type:     FrameRequestData,
		StreamID: streamID,
		Sequence: 0x10203040,
		Payload:  []byte{0xaa, 0xbb, 0xcc},
	}

	encoded, err := Encode(frame, testLimits())
	require.NoError(t, err)
	require.Equal(t, []byte{
		0x01, byte(FrameRequestData), 0x00, 0x00,
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		0x10, 0x20, 0x30, 0x40,
		0x00, 0x00, 0x00, 0x03,
		0xaa, 0xbb, 0xcc,
	}, encoded)
}

func TestFrameTypeValuesAreStable(t *testing.T) {
	t.Parallel()

	require.Equal(t, Type(1), FrameOpen)
	require.Equal(t, Type(2), FrameReady)
	require.Equal(t, Type(3), FrameCommit)
	require.Equal(t, Type(4), FrameCommitted)
	require.Equal(t, Type(5), FrameRequestData)
	require.Equal(t, Type(6), FrameRequestEnd)
	require.Equal(t, Type(7), FrameHeaders)
	require.Equal(t, Type(8), FrameResponseData)
	require.Equal(t, Type(9), FrameEnd)
	require.Equal(t, Type(10), FrameCancel)
	require.Equal(t, Type(11), FrameReset)
	require.Equal(t, Type(12), FrameWindowUpdate)
}

func TestFrameRoundTripsEveryType(t *testing.T) {
	t.Parallel()

	frameTypes := []Type{
		FrameOpen,
		FrameReady,
		FrameCommit,
		FrameCommitted,
		FrameRequestData,
		FrameRequestEnd,
		FrameHeaders,
		FrameResponseData,
		FrameEnd,
		FrameCancel,
		FrameReset,
		FrameWindowUpdate,
	}

	for _, frameType := range frameTypes {
		frameType := frameType
		t.Run(fmt.Sprintf("type-%d", frameType), func(t *testing.T) {
			t.Parallel()

			var payload []byte
			switch frameType {
			case FrameOpen, FrameReady, FrameHeaders, FrameEnd, FrameReset, FrameWindowUpdate:
				payload = []byte(`{"value":"metadata"}`)
			case FrameRequestData, FrameResponseData:
				payload = []byte{0x00, 0xff, 0x7f}
			}
			want := Frame{
				Version:  1,
				Type:     frameType,
				StreamID: testStreamID(),
				Sequence: 7,
				Payload:  payload,
			}

			encoded, err := Encode(want, testLimits())
			require.NoError(t, err)
			require.Len(t, encoded, HeaderSize+len(payload))

			got, err := Decode(encoded, testLimits())
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestFrameDecodeRejectsInvalidHeaderFieldsInProtocolOrder(t *testing.T) {
	t.Parallel()

	valid := validEncodedFrame(t, FrameOpen, nil)
	tests := map[string]struct {
		mutate  func([]byte) []byte
		wantErr error
	}{
		"short header": {
			mutate:  func(data []byte) []byte { return data[:HeaderSize-1] },
			wantErr: ErrShortHeader,
		},
		"unknown version before type": {
			mutate: func(data []byte) []byte {
				data[0] = 2
				data[1] = 0xff
				return data
			},
			wantErr: ErrUnsupportedVersion,
		},
		"unknown type before flags": {
			mutate: func(data []byte) []byte {
				data[1] = 0xff
				binary.BigEndian.PutUint16(data[2:4], 1)
				return data
			},
			wantErr: ErrUnknownFrameType,
		},
		"unknown flags before length": {
			mutate: func(data []byte) []byte {
				binary.BigEndian.PutUint16(data[2:4], 1)
				binary.BigEndian.PutUint32(data[24:28], 1)
				return data
			},
			wantErr: ErrUnknownFlags,
		},
		"declared payload longer than actual before stream ID": {
			mutate: func(data []byte) []byte {
				clear(data[4:20])
				binary.BigEndian.PutUint32(data[24:28], 1)
				return data
			},
			wantErr: ErrPayloadLengthMismatch,
		},
		"actual payload longer than declared": {
			mutate: func(data []byte) []byte {
				return append(data, 0xff)
			},
			wantErr: ErrPayloadLengthMismatch,
		},
		"zero stream ID": {
			mutate: func(data []byte) []byte {
				clear(data[4:20])
				return data
			},
			wantErr: ErrZeroStreamID,
		},
	}

	for name, tt := range tests {
		name, tt := name, tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := append([]byte(nil), valid...)
			_, err := Decode(tt.mutate(data), testLimits())
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestFrameRejectsPayloadAbovePerTypeLimit(t *testing.T) {
	t.Parallel()

	tests := map[string]Type{
		"metadata":      FrameOpen,
		"request DATA":  FrameRequestData,
		"response DATA": FrameResponseData,
	}

	for name, frameType := range tests {
		name, frameType := name, frameType
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			payload := make([]byte, MaxV1PayloadBytes+1)
			frame := Frame{Version: 1, Type: frameType, StreamID: testStreamID(), Payload: payload}

			_, err := Encode(frame, testLimits())
			require.ErrorIs(t, err, ErrPayloadTooLarge)

			data := make([]byte, HeaderSize+len(payload))
			data[0] = 1
			data[1] = byte(frameType)
			streamID := testStreamID()
			copy(data[4:20], streamID[:])
			binary.BigEndian.PutUint32(data[24:28], uint32(len(payload)))
			_, err = Decode(data, testLimits())
			require.ErrorIs(t, err, ErrPayloadTooLarge)
		})
	}
}

func TestFrameAcceptsPayloadAtPerTypeLimit(t *testing.T) {
	t.Parallel()

	for _, frameType := range []Type{FrameOpen, FrameRequestData, FrameResponseData} {
		frameType := frameType
		t.Run(fmt.Sprintf("type-%d", frameType), func(t *testing.T) {
			t.Parallel()
			payload := make([]byte, MaxV1PayloadBytes)
			encoded, err := Encode(Frame{
				Version: 1, Type: frameType, StreamID: testStreamID(), Payload: payload,
			}, testLimits())
			require.NoError(t, err)
			decoded, err := Decode(encoded, testLimits())
			require.NoError(t, err)
			require.Len(t, decoded.Payload, MaxV1PayloadBytes)
		})
	}
}

func TestFrameUsesSmallerNegotiatedPayloadLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		frameType Type
		setLimit  func(*Limits)
	}{
		{name: "metadata", frameType: FrameOpen, setLimit: func(limits *Limits) { limits.MaxMetadataBytes = 8 }},
		{name: "request DATA", frameType: FrameRequestData, setLimit: func(limits *Limits) { limits.MaxDataBytes = 8 }},
		{name: "response DATA", frameType: FrameResponseData, setLimit: func(limits *Limits) { limits.MaxDataBytes = 8 }},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			limits := testLimits()
			tt.setLimit(&limits)
			frame := Frame{Version: 1, Type: tt.frameType, StreamID: testStreamID(), Payload: make([]byte, 9)}

			_, err := Encode(frame, limits)
			require.ErrorIs(t, err, ErrPayloadTooLarge)

			data := make([]byte, HeaderSize+len(frame.Payload))
			data[0], data[1] = 1, byte(tt.frameType)
			streamID := testStreamID()
			copy(data[4:20], streamID[:])
			binary.BigEndian.PutUint32(data[24:28], uint32(len(frame.Payload)))
			_, err = Decode(data, limits)
			require.ErrorIs(t, err, ErrPayloadTooLarge)
		})
	}
}

func TestFrameClampsNegotiatedPayloadLimitsToV1HardLimit(t *testing.T) {
	t.Parallel()

	limits := testLimits()
	limits.MaxMetadataBytes = 2 * MaxV1PayloadBytes
	limits.MaxDataBytes = 2 * MaxV1PayloadBytes

	for _, frameType := range []Type{FrameOpen, FrameRequestData, FrameResponseData} {
		frameType := frameType
		t.Run(fmt.Sprintf("type-%d", frameType), func(t *testing.T) {
			t.Parallel()
			frame := Frame{
				Version: 1, Type: frameType, StreamID: testStreamID(),
				Payload: make([]byte, MaxV1PayloadBytes+1),
			}
			_, err := Encode(frame, limits)
			require.ErrorIs(t, err, ErrPayloadTooLarge)
		})
	}
}

func TestFrameRejectsMissingRequiredLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		frameType Type
		limits    Limits
	}{
		{name: "zero metadata limit", frameType: FrameOpen, limits: Limits{MaxDataBytes: 1}},
		{name: "negative metadata limit", frameType: FrameOpen, limits: Limits{MaxMetadataBytes: -1, MaxDataBytes: 1}},
		{name: "zero DATA limit", frameType: FrameRequestData, limits: Limits{MaxMetadataBytes: 1}},
		{name: "negative DATA limit", frameType: FrameResponseData, limits: Limits{MaxMetadataBytes: 1, MaxDataBytes: -1}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			frame := Frame{Version: 1, Type: tt.frameType, StreamID: testStreamID()}
			_, err := Encode(frame, tt.limits)
			require.ErrorIs(t, err, ErrInvalidLimits)

			data := make([]byte, HeaderSize)
			data[0], data[1] = 1, byte(tt.frameType)
			streamID := testStreamID()
			copy(data[4:20], streamID[:])
			_, err = Decode(data, tt.limits)
			require.ErrorIs(t, err, ErrInvalidLimits)
		})
	}
}

func TestFrameEncodeRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	valid := Frame{Version: 1, Type: FrameOpen, StreamID: testStreamID()}
	tests := map[string]struct {
		mutate  func(Frame) Frame
		wantErr error
	}{
		"zero version": {
			mutate:  func(frame Frame) Frame { frame.Version = 0; return frame },
			wantErr: ErrUnsupportedVersion,
		},
		"future version": {
			mutate:  func(frame Frame) Frame { frame.Version = 2; return frame },
			wantErr: ErrUnsupportedVersion,
		},
		"unknown type": {
			mutate:  func(frame Frame) Frame { frame.Type = Type(0xff); return frame },
			wantErr: ErrUnknownFrameType,
		},
		"unknown flags": {
			mutate:  func(frame Frame) Frame { frame.Flags = 1; return frame },
			wantErr: ErrUnknownFlags,
		},
		"zero stream ID": {
			mutate:  func(frame Frame) Frame { frame.StreamID = StreamID{}; return frame },
			wantErr: ErrZeroStreamID,
		},
	}

	for name, tt := range tests {
		name, tt := name, tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Encode(tt.mutate(valid), testLimits())
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNextSequenceDetectsOverflow(t *testing.T) {
	t.Parallel()

	next, err := NextSequence(0)
	require.NoError(t, err)
	require.Equal(t, uint32(1), next)

	next, err = NextSequence(math.MaxUint32 - 1)
	require.NoError(t, err)
	require.Equal(t, uint32(math.MaxUint32), next)

	_, err = NextSequence(math.MaxUint32)
	require.ErrorIs(t, err, ErrSequenceOverflow)
}

func TestStreamIDGenerationRejectsZeroAndReadFailures(t *testing.T) {
	t.Parallel()

	_, err := newStreamID(bytes.NewReader(make([]byte, len(StreamID{}))))
	require.ErrorIs(t, err, ErrZeroStreamID)

	_, err = newStreamID(errorReader{})
	require.ErrorIs(t, err, ErrStreamIDGeneration)
}

func TestFrameErrorsDoNotExposePayload(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-ticket"
	data := validEncodedFrame(t, FrameOpen, []byte(secret))
	data[0] = 2

	_, err := Decode(data, testLimits())
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
}

func validEncodedFrame(t *testing.T, frameType Type, payload []byte) []byte {
	t.Helper()
	encoded, err := Encode(Frame{
		Version: 1, Type: frameType, StreamID: testStreamID(), Payload: payload,
	}, testLimits())
	require.NoError(t, err)
	return encoded
}

func testStreamID() StreamID {
	return StreamID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
}

func testLimits() Limits {
	return Limits{
		MaxMetadataBytes:      MaxV1PayloadBytes,
		MaxDataBytes:          MaxV1PayloadBytes,
		InitialStreamWindow:   256 * 1024,
		MaxQueuedSessionBytes: 4 * 1024 * 1024,
		MaxConcurrentStreams:  128,
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
