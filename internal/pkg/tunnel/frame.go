package tunnel

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"math"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

const (
	ProtocolVersion   uint8 = 2
	HeaderSize              = 28
	MaxV2PayloadBytes       = 64 * 1024
)

type StreamID [16]byte

type Type uint8

const (
	FrameOpen Type = iota + 1
	FrameReady
	FrameCommit
	FrameCommitted
	FrameRequestData
	FrameRequestEnd
	FrameHeaders
	FrameResponseData
	FrameAttemptResult
	FrameEnd
	FrameCancel
	FrameReset
	FrameWindowUpdate
	FrameAPIResult
	FrameWebSocketMessageStart
	FrameWebSocketMessageData
	FrameWebSocketMessageEnd
	FrameWebSocketPing
	FrameWebSocketPong
	FrameWebSocketClose
)

type Frame struct {
	Version  uint8
	Type     Type
	Flags    uint16
	StreamID StreamID
	Sequence uint32
	Payload  []byte
}

func NewStreamID() (StreamID, error) {
	return newStreamID(rand.Reader)
}

func newStreamID(reader io.Reader) (StreamID, error) {
	var streamID StreamID
	if _, err := io.ReadFull(reader, streamID[:]); err != nil {
		return StreamID{}, ErrStreamIDGeneration
	}
	if streamID == (StreamID{}) {
		return StreamID{}, ErrZeroStreamID
	}
	return streamID, nil
}

func NextSequence(sequence uint32) (uint32, error) {
	if sequence == math.MaxUint32 {
		return 0, ErrSequenceOverflow
	}
	return sequence + 1, nil
}

func Encode(frame Frame, limits Limits) ([]byte, error) {
	if err := validateFrameFields(frame.Version, frame.Type, frame.Flags); err != nil {
		return nil, err
	}
	limit, err := FramePayloadLimit(frame.Type, limits)
	if err != nil {
		return nil, err
	}
	if int64(len(frame.Payload)) > limit {
		return nil, ErrPayloadTooLarge
	}
	if frame.StreamID == (StreamID{}) {
		return nil, ErrZeroStreamID
	}

	message := make([]byte, HeaderSize+len(frame.Payload))
	message[0] = frame.Version
	message[1] = byte(frame.Type)
	binary.BigEndian.PutUint16(message[2:4], frame.Flags)
	copy(message[4:20], frame.StreamID[:])
	binary.BigEndian.PutUint32(message[20:24], frame.Sequence)
	binary.BigEndian.PutUint32(message[24:28], uint32(len(frame.Payload)))
	copy(message[HeaderSize:], frame.Payload)
	return message, nil
}

func Decode(message []byte, limits Limits) (Frame, error) {
	if len(message) < HeaderSize {
		return Frame{}, ErrShortHeader
	}

	version := message[0]
	frameType := Type(message[1])
	flags := binary.BigEndian.Uint16(message[2:4])
	if err := validateFrameFields(version, frameType, flags); err != nil {
		return Frame{}, err
	}

	payloadBytes := binary.BigEndian.Uint32(message[24:28])
	if uint64(payloadBytes) != uint64(len(message)-HeaderSize) {
		return Frame{}, ErrPayloadLengthMismatch
	}
	limit, err := FramePayloadLimit(frameType, limits)
	if err != nil {
		return Frame{}, err
	}
	if int64(payloadBytes) > limit {
		return Frame{}, ErrPayloadTooLarge
	}

	var streamID StreamID
	copy(streamID[:], message[4:20])
	if streamID == (StreamID{}) {
		return Frame{}, ErrZeroStreamID
	}

	var payload []byte
	if payloadBytes > 0 {
		payload = make([]byte, int(payloadBytes))
		copy(payload, message[HeaderSize:])
	}
	return Frame{
		Version:  version,
		Type:     frameType,
		Flags:    flags,
		StreamID: streamID,
		Sequence: binary.BigEndian.Uint32(message[20:24]),
		Payload:  payload,
	}, nil
}

func validateFrameFields(version uint8, frameType Type, flags uint16) error {
	if version != ProtocolVersion {
		return ErrUnsupportedVersion
	}
	if frameType < FrameOpen || frameType > FrameWebSocketClose {
		return ErrUnknownFrameType
	}
	if flags != 0 {
		return ErrUnknownFlags
	}
	return nil
}

// FramePayloadLimit returns the negotiated payload ceiling for one frame type.
func FramePayloadLimit(frameType Type, limits Limits) (int64, error) {
	limit := limits.MaxMetadataBytes
	if frameType == FrameRequestData || frameType == FrameResponseData || frameType == FrameWebSocketMessageData {
		limit = limits.MaxDataBytes
	}
	if frameType == FrameWebSocketPing || frameType == FrameWebSocketPong || frameType == FrameWebSocketClose {
		limit = MaxWebSocketControlPayloadBytes
	}
	if limit <= 0 {
		return 0, ErrInvalidLimits
	}
	if limit > MaxV2PayloadBytes {
		limit = MaxV2PayloadBytes
	}
	// behavior change: Result obeys the negotiated metadata ceiling as well as its wire hard cap.
	if frameType == FrameAttemptResult && limit > attemptwire.MaxResultWireBytes {
		limit = attemptwire.MaxResultWireBytes
	}
	return limit, nil
}
