package tunnel

import "errors"

var (
	ErrShortHeader            = errors.New("tunnel: frame header too short")
	ErrUnsupportedVersion     = errors.New("tunnel: unsupported protocol version")
	ErrUnknownFrameType       = errors.New("tunnel: unknown frame type")
	ErrUnknownFlags           = errors.New("tunnel: unknown frame flags")
	ErrPayloadLengthMismatch  = errors.New("tunnel: payload length mismatch")
	ErrPayloadTooLarge        = errors.New("tunnel: frame payload too large")
	ErrZeroStreamID           = errors.New("tunnel: stream ID is zero")
	ErrSequenceOverflow       = errors.New("tunnel: sequence overflow")
	ErrMetadataTooLarge       = errors.New("tunnel: metadata too large")
	ErrMalformedMetadata      = errors.New("tunnel: malformed metadata")
	ErrInvalidLimits          = errors.New("tunnel: invalid limits")
	ErrNilMetadataDestination = errors.New("tunnel: nil metadata destination")
	ErrStreamIDGeneration     = errors.New("tunnel: stream ID generation failed")
)

const (
	ErrorCodeRelayProtocol         = "relay_protocol"
	ErrorCodeRelayOverloaded       = "relay_overloaded"
	ErrorCodeStreamWindowTimeout   = "stream_window_timeout"
	ErrorCodeRequestCancelled      = "request_cancelled"
	ErrorCodeRequestDeadline       = "request_deadline"
	ErrorCodeSessionClosed         = "session_closed"
	ErrorCodeDrainTimeout          = "drain_timeout"
	ErrorCodeRelayFallbackDisabled = "relay_fallback_disabled"
)
