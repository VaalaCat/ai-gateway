package tunnel

import (
	"encoding/json"
	"net/http"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

const (
	MaxMetadataBytes       = MaxV1PayloadBytes
	MaxV1StreamWindowBytes = 8 * 1024 * 1024
	MaxV1SessionQueueBytes = 64 * 1024 * 1024
	MaxV1ConcurrentStreams = 4096
)

type CommitState uint8

const (
	PreCommit CommitState = iota
	CommitUncertain
	Committed
)

type StreamPurpose string

const StreamPurposeConnectivityProbe StreamPurpose = "connectivity_probe"

type Limits struct {
	MaxMetadataBytes      int64 `json:"max_metadata_bytes"`
	MaxDataBytes          int64 `json:"max_data_bytes"`
	InitialStreamWindow   int64 `json:"initial_stream_window"`
	MaxQueuedSessionBytes int64 `json:"max_queued_session_bytes"`
	MaxConcurrentStreams  int   `json:"max_concurrent_streams"`
}

func NormalizeV1Limits(limits Limits) (Limits, error) {
	if limits.MaxMetadataBytes <= 0 || limits.MaxDataBytes <= 0 ||
		limits.InitialStreamWindow <= 0 || limits.MaxQueuedSessionBytes <= 0 ||
		limits.MaxConcurrentStreams <= 0 {
		return Limits{}, ErrInvalidLimits
	}
	if limits.InitialStreamWindow > MaxV1StreamWindowBytes ||
		limits.MaxQueuedSessionBytes > MaxV1SessionQueueBytes ||
		limits.MaxConcurrentStreams > MaxV1ConcurrentStreams {
		return Limits{}, ErrInvalidLimits
	}
	if limits.MaxMetadataBytes > MaxV1PayloadBytes {
		limits.MaxMetadataBytes = MaxV1PayloadBytes
	}
	if limits.MaxDataBytes > MaxV1PayloadBytes {
		limits.MaxDataBytes = MaxV1PayloadBytes
	}
	return limits, nil
}

type Open struct {
	Purpose        StreamPurpose                 `json:"purpose,omitempty"`
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
}

func (o Open) IsConnectivityProbe() bool {
	return o.Purpose == StreamPurposeConnectivityProbe &&
		o.Method == http.MethodGet && o.Path == "/ping" &&
		len(o.Header) == 0 && o.BodyLength == 0 && o.RemainingNanos > 0 &&
		o.TargetAgentID != "" && o.RouteID == 0 && o.Hop == 0 &&
		o.ResponseWindow > 0 && o.ResponseWindow <= MaxV1StreamWindowBytes
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
