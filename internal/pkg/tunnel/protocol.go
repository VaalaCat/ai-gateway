package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

var ErrInvalidOpenKind = errors.New("tunnel: invalid open stream kind")

const (
	DirectTunnelPath       = "/internal/agent/direct-tunnel"
	MaxMetadataBytes       = MaxV2PayloadBytes
	MaxV2StreamWindowBytes = 8 * 1024 * 1024
	MaxV2SessionQueueBytes = 64 * 1024 * 1024
	MaxV2ConcurrentStreams = 4096
)

var defaultDirectLimits = Limits{
	MaxMetadataBytes: MaxV2PayloadBytes, MaxDataBytes: MaxV2PayloadBytes,
	InitialStreamWindow: 512 * 1024, MaxQueuedSessionBytes: 8 * 1024 * 1024,
	MaxConcurrentStreams: 256,
}

type CommitState uint8

const (
	PreCommit CommitState = iota
	CommitUncertain
	Committed
)

type ProbePolicy string

const (
	ProbeRespectBusinessPolicy ProbePolicy = "respect_business_policy"
	ProbeBypassBusinessPolicy  ProbePolicy = "bypass_business_policy"
)

type Limits struct {
	MaxMetadataBytes      int64 `json:"max_metadata_bytes"`
	MaxDataBytes          int64 `json:"max_data_bytes"`
	InitialStreamWindow   int64 `json:"initial_stream_window"`
	MaxQueuedSessionBytes int64 `json:"max_queued_session_bytes"`
	MaxConcurrentStreams  int   `json:"max_concurrent_streams"`
}

type DirectHello struct {
	ProtocolVersion uint8  `json:"protocol_version"`
	Limits          Limits `json:"limits"`
}

type DirectReady struct {
	ProtocolVersion   uint8  `json:"protocol_version"`
	TargetAgentID     string `json:"target_agent_id"`
	SessionGeneration uint64 `json:"session_generation"`
	Limits            Limits `json:"limits"`
}

type DirectAccepted struct {
	SessionGeneration uint64 `json:"session_generation"`
}

type DirectConfirmed struct {
	SessionGeneration uint64 `json:"session_generation"`
}

func SelectDirectLimits(source, target Limits) (Limits, error) {
	source, err := normalizeDirectLimits(source)
	if err != nil {
		return Limits{}, err
	}
	target, err = normalizeDirectLimits(target)
	if err != nil {
		return Limits{}, err
	}
	return NormalizeV2Limits(Limits{
		MaxMetadataBytes:      min(source.MaxMetadataBytes, target.MaxMetadataBytes),
		MaxDataBytes:          min(source.MaxDataBytes, target.MaxDataBytes),
		InitialStreamWindow:   min(source.InitialStreamWindow, target.InitialStreamWindow),
		MaxQueuedSessionBytes: min(source.MaxQueuedSessionBytes, target.MaxQueuedSessionBytes),
		MaxConcurrentStreams:  min(source.MaxConcurrentStreams, target.MaxConcurrentStreams),
	})
}

func normalizeDirectLimits(limits Limits) (Limits, error) {
	if limits.MaxMetadataBytes < 0 || limits.MaxDataBytes < 0 || limits.InitialStreamWindow < 0 ||
		limits.MaxQueuedSessionBytes < 0 || limits.MaxConcurrentStreams < 0 {
		return Limits{}, ErrInvalidLimits
	}
	if limits.MaxMetadataBytes == 0 {
		limits.MaxMetadataBytes = defaultDirectLimits.MaxMetadataBytes
	}
	if limits.MaxDataBytes == 0 {
		limits.MaxDataBytes = defaultDirectLimits.MaxDataBytes
	}
	if limits.InitialStreamWindow == 0 {
		limits.InitialStreamWindow = defaultDirectLimits.InitialStreamWindow
	}
	if limits.MaxQueuedSessionBytes == 0 {
		limits.MaxQueuedSessionBytes = defaultDirectLimits.MaxQueuedSessionBytes
	}
	if limits.MaxConcurrentStreams == 0 {
		limits.MaxConcurrentStreams = defaultDirectLimits.MaxConcurrentStreams
	}
	return NormalizeV2Limits(limits)
}

func NormalizeV2Limits(limits Limits) (Limits, error) {
	if limits.MaxMetadataBytes <= 0 || limits.MaxDataBytes <= 0 ||
		limits.InitialStreamWindow <= 0 || limits.MaxQueuedSessionBytes <= 0 ||
		limits.MaxConcurrentStreams <= 0 {
		return Limits{}, ErrInvalidLimits
	}
	if limits.InitialStreamWindow > MaxV2StreamWindowBytes ||
		limits.MaxQueuedSessionBytes > MaxV2SessionQueueBytes ||
		limits.MaxConcurrentStreams > MaxV2ConcurrentStreams {
		return Limits{}, ErrInvalidLimits
	}
	if limits.MaxMetadataBytes > MaxV2PayloadBytes {
		limits.MaxMetadataBytes = MaxV2PayloadBytes
	}
	if limits.MaxDataBytes > MaxV2PayloadBytes {
		limits.MaxDataBytes = MaxV2PayloadBytes
	}
	return limits, nil
}

type Open struct {
	ProbePolicy    ProbePolicy                   `json:"probe_policy,omitempty"`
	Method         string                        `json:"method"`
	Path           string                        `json:"path"`
	Header         map[string][]string           `json:"header"`
	BodyLength     int64                         `json:"body_length"`
	RemainingNanos int64                         `json:"remaining_nanos"`
	RequestID      string                        `json:"request_id"`
	SourceAgentID  string                        `json:"source_agent_id"`
	TargetAgentID  string                        `json:"target_agent_id"`
	RouteID        uint                          `json:"route_id"`
	Hop            uint8                         `json:"hop"`
	ResponseWindow int64                         `json:"response_window"`
	Attempt        *attemptwire.AttemptProxyMeta `json:"attempt,omitempty"`
	API            *apiattempt.APIAttemptMeta    `json:"api,omitempty"`
	WebSocket      *WebSocketOpen                `json:"websocket,omitempty"`
}

const (
	MaxWebSocketControlPayloadBytes = 125
	WebSocketTextMessage            = 1
	WebSocketBinaryMessage          = 2
	WebSocketCloseProtocolError     = 1002
)

type WebSocketOpen struct {
	ResponseWindow int64 `json:"response_window"`
}

type WebSocketAccepted struct {
	RequestWindow  int64               `json:"request_window"`
	Subprotocol    string              `json:"subprotocol,omitempty"`
	ProviderStatus int                 `json:"provider_status,omitempty"`
	Rejection      *WebSocketRejection `json:"rejection,omitempty"`
}

type WebSocketRejection struct {
	StatusCode      int                 `json:"status_code"`
	Header          map[string][]string `json:"header,omitempty"`
	Body            []byte              `json:"body,omitempty"`
	HeaderTruncated bool                `json:"header_truncated,omitempty"`
	BodyTruncated   bool                `json:"body_truncated,omitempty"`
}

// ValidWebSocketRejectionStatus reports whether an ordinary HTTP response can
// be the terminal result of a failed WebSocket upgrade.
func ValidWebSocketRejectionStatus(status int) bool { return status >= 200 && status <= 599 }

type WebSocketMessageStart struct {
	MessageID uint64 `json:"message_id"`
	Type      int    `json:"type"`
}

type WebSocketEventKind uint8

const (
	WebSocketMessageStartEvent WebSocketEventKind = iota + 1
	WebSocketMessageDataEvent
	WebSocketMessageEndEvent
	WebSocketPingEvent
	WebSocketPongEvent
	WebSocketCloseEvent
)

type WebSocketEvent struct {
	Kind      WebSocketEventKind
	MessageID uint64
	Type      int
	Data      []byte
	Code      int
	Reason    string
}

func EncodeWebSocketClose(code int, reason string) ([]byte, error) {
	if code == 0 {
		if reason != "" {
			return nil, ErrMalformedMetadata
		}
		return nil, nil
	}
	if !validWebSocketCloseCode(code) || !utf8.ValidString(reason) {
		return nil, ErrMalformedMetadata
	}
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload, uint16(code))
	copy(payload[2:], reason)
	if len(payload) > MaxWebSocketControlPayloadBytes {
		return nil, ErrPayloadTooLarge
	}
	return payload, nil
}

func DecodeWebSocketClose(payload []byte) (WebSocketEvent, error) {
	if len(payload) == 0 {
		return WebSocketEvent{Kind: WebSocketCloseEvent}, nil
	}
	if len(payload) < 2 || len(payload) > MaxWebSocketControlPayloadBytes {
		return WebSocketEvent{}, ErrMalformedMetadata
	}
	code := int(binary.BigEndian.Uint16(payload[:2]))
	if !validWebSocketCloseCode(code) || !utf8.Valid(payload[2:]) {
		return WebSocketEvent{}, ErrMalformedMetadata
	}
	return WebSocketEvent{Kind: WebSocketCloseEvent, Code: code, Reason: string(payload[2:])}, nil
}

func validWebSocketCloseCode(code int) bool {
	if code >= 3000 && code <= 4999 {
		return true
	}
	return code >= 1000 && code <= 1013 && code != 1004 && code != 1005 && code != 1006
}

func ValidateWebSocketMessageType(messageType int) error {
	if messageType != WebSocketTextMessage && messageType != WebSocketBinaryMessage {
		return fmt.Errorf("tunnel: invalid websocket message type %d", messageType)
	}
	return nil
}

type OpenStreamKind uint8

const (
	OpenStreamProbe OpenStreamKind = iota + 1
	OpenStreamAttempt
	OpenStreamAPI
)

func (o Open) StreamKind() (OpenStreamKind, error) {
	if o.ProbePolicy != "" {
		if o.Attempt == nil && o.API == nil && o.IsConnectivityProbe() {
			return OpenStreamProbe, nil
		}
		return 0, ErrInvalidOpenKind
	}
	if o.Attempt != nil && o.API == nil {
		return OpenStreamAttempt, nil
	}
	if o.Attempt == nil && o.API != nil {
		return OpenStreamAPI, nil
	}
	return 0, ErrInvalidOpenKind
}

func (o Open) IsConnectivityProbe() bool {
	return (o.ProbePolicy == ProbeRespectBusinessPolicy || o.ProbePolicy == ProbeBypassBusinessPolicy) &&
		o.Method == http.MethodGet && o.Path == "/ping" &&
		len(o.Header) == 0 && o.BodyLength == 0 && o.RemainingNanos > 0 &&
		o.TargetAgentID != "" && o.RouteID == 0 && o.Hop == 0 &&
		o.ResponseWindow > 0 && o.ResponseWindow <= MaxV2StreamWindowBytes && o.Attempt == nil && o.API == nil
}

type Ready struct {
	RequestWindow int64 `json:"request_window"`
}

type Headers struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Trailer    map[string][]string `json:"trailer,omitempty"`
}

type Trailers struct {
	Header  map[string][]string `json:"header,omitempty"`
	Dynamic []string            `json:"dynamic,omitempty"`
}

type Reset struct {
	Code      string `json:"code"`
	Stage     string `json:"stage"`
	Committed bool   `json:"committed"`
}

type WindowUpdate struct {
	Bytes int64 `json:"bytes"`
}

type Hello struct {
	Nonce             string `json:"nonce"`
	DesiredGeneration uint64 `json:"desired_generation"`
}

type Welcome struct {
	NonceProof        string   `json:"nonce_proof"`
	MasterInstanceID  string   `json:"master_instance_id"`
	SessionGeneration uint64   `json:"session_generation"`
	Capabilities      []string `json:"capabilities"`
	Limits            Limits   `json:"limits"`
}

type Authenticated struct {
	DesiredGeneration uint64 `json:"desired_generation"`
	SessionGeneration uint64 `json:"session_generation"`
}

type Confirmed struct {
	DesiredGeneration uint64 `json:"desired_generation"`
	SessionGeneration uint64 `json:"session_generation"`
}

func EncodeMetadata[T any](value T, limit int64) ([]byte, error) {
	effectiveLimit, err := metadataLimit(limit)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, ErrMalformedMetadata
	}
	if int64(len(payload)) > effectiveLimit {
		return nil, ErrMetadataTooLarge
	}
	return payload, nil
}

func DecodeMetadata[T any](payload []byte, dst *T, limit int64) error {
	effectiveLimit, err := metadataLimit(limit)
	if err != nil {
		return err
	}
	if int64(len(payload)) > effectiveLimit {
		return ErrMetadataTooLarge
	}
	if dst == nil {
		return ErrNilMetadataDestination
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return ErrMalformedMetadata
	}
	return nil
}

func metadataLimit(limit int64) (int64, error) {
	if limit <= 0 {
		return 0, ErrInvalidLimits
	}
	if limit > MaxMetadataBytes {
		return MaxMetadataBytes, nil
	}
	return limit, nil
}
