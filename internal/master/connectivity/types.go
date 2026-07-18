package connectivity

import (
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

const (
	ControlReasonHeartbeatMissing    = "heartbeat_missing"
	ControlReasonHeartbeatStale      = "heartbeat_stale"
	ControlReasonHeartbeatRecovering = "heartbeat_recovering"
)

const (
	ErrorCodeConnectionGenerationChanged = "connection_generation_changed"

	DenialAgentDisabled           = "agent_disabled"
	DenialControlDisconnected     = "control_disconnected"
	DenialRelayUnsupported        = "relay_unsupported"
	DenialRelayNotConfigured      = "relay_not_configured"
	DenialRelayDisabled           = "relay_disabled"
	DenialRelaySessionUnavailable = "relay_session_unavailable"
)

var ErrConnectionGenerationChanged = errors.New(ErrorCodeConnectionGenerationChanged)

type RecentError struct {
	Code       string `json:"code"`
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	OccurredAt int64  `json:"occurred_at"`
	Count      uint64 `json:"count"`
}

type RouteFailureDiagnostic struct {
	RequestID     string `json:"request_id"`
	SourceAgentID string `json:"source_agent_id"`
	TargetAgentID string `json:"target_agent_id"`
	RouteID       uint   `json:"route_id"`
	PathKind      string `json:"path_kind"`
	Stage         string `json:"stage"`
	CommitState   string `json:"commit_state"`
	ReasonCode    string `json:"reason_code"`
	OccurredAt    int64  `json:"occurred_at"`
}

type ControlSnapshot struct {
	State             string   `json:"state"`
	Health            string   `json:"health"`
	ReasonCodes       []string `json:"reason_codes"`
	SessionGeneration uint64   `json:"session_generation"`
	ConnectedAt       int64    `json:"connected_at"`
	HeartbeatAt       int64    `json:"heartbeat_at"`
	RuntimeReportedAt int64    `json:"runtime_reported_at"`
	LastSeen          int64    `json:"last_seen"`
}

type RelayDesiredSnapshot struct {
	Mode              string `json:"mode"`
	ConfiguredURI     string `json:"configured_uri"`
	EffectiveURI      string `json:"effective_uri"`
	DesiredGeneration uint64 `json:"desired_generation"`
}

type RelayActiveSnapshot struct {
	URI               string `json:"uri"`
	ActiveGeneration  uint64 `json:"active_generation"`
	SessionGeneration uint64 `json:"session_generation"`
	ConnectedAt       int64  `json:"connected_at"`
	Streams           int    `json:"streams"`
	RetryAt           int64  `json:"retry_at"`
}

type RelaySnapshot struct {
	Support             string               `json:"support"`
	Config              string               `json:"config"`
	Availability        string               `json:"availability"`
	AcceptingNewStreams bool                 `json:"accepting_new_streams"`
	Convergence         string               `json:"convergence"`
	Desired             RelayDesiredSnapshot `json:"desired"`
	Active              RelayActiveSnapshot  `json:"active"`
	RecentErrors        []RecentError        `json:"recent_errors"`
}

type RelaySummary struct {
	Support             string `json:"support"`
	Config              string `json:"config"`
	Availability        string `json:"availability"`
	AcceptingNewStreams bool   `json:"accepting_new_streams"`
	Convergence         string `json:"convergence"`
	Streams             int    `json:"streams"`
}

type DirectSummary struct {
	State       string `json:"state"`
	Reachable   int    `json:"reachable"`
	Degraded    int    `json:"degraded"`
	Unreachable int    `json:"unreachable"`
	Stale       int    `json:"stale"`
	Total       int    `json:"total"`
}

type DirectAddressSnapshot struct {
	URL string `json:"url"`
	Tag string `json:"tag"`
}

type DirectTargetSnapshot struct {
	TargetAgentID      string                  `json:"target_agent_id"`
	TargetName         string                  `json:"target_name"`
	Addresses          []DirectAddressSnapshot `json:"addresses"`
	Network            string                  `json:"network"`
	Identity           string                  `json:"identity"`
	Eligible           bool                    `json:"eligible"`
	Checking           bool                    `json:"checking"`
	ProbeGeneration    uint64                  `json:"probe_generation"`
	AddressFingerprint string                  `json:"address_fingerprint"`
	CheckedAt          int64                   `json:"checked_at"`
	LatencyMS          int64                   `json:"latency_ms"`
	LastError          *RecentError            `json:"last_error,omitempty"`
	RecentErrors       []RecentError           `json:"recent_errors"`
}

type DirectSnapshot struct {
	Generation uint64                          `json:"generation"`
	Summary    DirectSummary                   `json:"summary"`
	Targets    map[string]DirectTargetSnapshot `json:"targets,omitempty"`
}

type RelayPathSummary struct {
	State       string `json:"state"`
	Reachable   int    `json:"reachable"`
	Unreachable int    `json:"unreachable"`
	Unavailable int    `json:"unavailable"`
	Unknown     int    `json:"unknown"`
	Unsupported int    `json:"unsupported"`
	Stale       int    `json:"stale"`
	Total       int    `json:"total"`
}

type RelayTargetSnapshot struct {
	TargetAgentID         string                   `json:"target_agent_id"`
	TargetName            string                   `json:"target_name"`
	State                 protocol.RelayProbeState `json:"state"`
	Stage                 protocol.RelayProbeStage `json:"stage,omitempty"`
	Checking              bool                     `json:"checking"`
	ProbeGeneration       uint64                   `json:"probe_generation"`
	RelayFingerprint      string                   `json:"relay_fingerprint"`
	SourceRelayGeneration uint64                   `json:"source_relay_generation"`
	TargetRelayGeneration uint64                   `json:"target_relay_generation"`
	CheckedAt             int64                    `json:"checked_at"`
	LatencyMS             int64                    `json:"latency_ms"`
	LastError             *RecentError             `json:"last_error,omitempty"`
}

type RelayPathSnapshot struct {
	Generation uint64                         `json:"generation"`
	Summary    RelayPathSummary               `json:"summary"`
	Targets    map[string]RelayTargetSnapshot `json:"targets,omitempty"`
}

type RouteDirectTargetSnapshot struct {
	State              string                  `json:"state"`
	Addresses          []DirectAddressSnapshot `json:"addresses"`
	Network            string                  `json:"network"`
	Identity           string                  `json:"identity"`
	Eligible           bool                    `json:"eligible"`
	Checking           bool                    `json:"checking"`
	ProbeGeneration    uint64                  `json:"probe_generation"`
	AddressFingerprint string                  `json:"address_fingerprint"`
	CheckedAt          int64                   `json:"checked_at"`
	LatencyMS          int64                   `json:"latency_ms"`
	LastError          *RecentError            `json:"last_error,omitempty"`
}

type RouteTargetSnapshot struct {
	TargetAgentID string                    `json:"target_agent_id"`
	TargetName    string                    `json:"target_name"`
	Direct        RouteDirectTargetSnapshot `json:"direct"`
	Relay         RelayTargetSnapshot       `json:"relay"`
}

type RouteTargetSummaries struct {
	Direct DirectSummary    `json:"direct"`
	Relay  RelayPathSummary `json:"relay"`
}

type RouteTargetsSnapshot struct {
	Generation uint64                         `json:"generation"`
	Summaries  RouteTargetSummaries           `json:"summaries"`
	Targets    map[string]RouteTargetSnapshot `json:"targets"`
}

type RouteTargetsPage struct {
	SnapshotEpoch string                `json:"snapshot_epoch"`
	SnapshotSeq   uint64                `json:"snapshot_seq"`
	ObservedAt    int64                 `json:"observed_at"`
	Summaries     RouteTargetSummaries  `json:"summaries"`
	Data          []RouteTargetSnapshot `json:"data"`
	NextCursor    string                `json:"next_cursor,omitempty"`
	Limit         int                   `json:"limit"`
}

type OperationStatus struct {
	Operation  Operation `json:"operation"`
	Allowed    bool      `json:"allowed"`
	DenialCode string    `json:"denial_code,omitempty"`
}

type ConnectionSnapshot struct {
	Version           string               `json:"version"`
	SnapshotEpoch     string               `json:"snapshot_epoch"`
	SnapshotSeq       uint64               `json:"snapshot_seq"`
	ObservedAt        int64                `json:"observed_at"`
	AgentID           string               `json:"agent_id"`
	AdminStatus       int                  `json:"admin_status"`
	Control           ControlSnapshot      `json:"control"`
	Relay             RelaySnapshot        `json:"relay"`
	Direct            DirectSnapshot       `json:"direct"`
	RelayPaths        RelayPathSnapshot    `json:"-"`
	TargetSummaries   RouteTargetSummaries `json:"target_summaries"`
	RouteTargets      RouteTargetsSnapshot `json:"-"`
	AllowedOperations []OperationStatus    `json:"allowed_operations"`
}

type ConnectionSummary struct {
	Version       string               `json:"version"`
	SnapshotEpoch string               `json:"snapshot_epoch"`
	SnapshotSeq   uint64               `json:"snapshot_seq"`
	ObservedAt    int64                `json:"observed_at"`
	Control       ControlSnapshot      `json:"control"`
	Relay         RelaySummary         `json:"relay"`
	Direct        DirectSummary        `json:"direct"`
	Targets       RouteTargetSummaries `json:"targets"`
}

type SnapshotBatch struct {
	SnapshotEpoch string
	SnapshotSeq   uint64
	ObservedAt    int64
	Items         map[string]ConnectionSnapshot
}

type Operation string

const (
	OperationFullSync           Operation = "full_sync"
	OperationProbe              Operation = "probe"
	OperationRelayReconnect     Operation = "relay_reconnect"
	OperationRelayDrain         Operation = "relay_drain"
	OperationRelayDisconnect    Operation = "relay_disconnect"
	OperationDirectCircuitReset Operation = "direct_circuit_reset"
	OperationInterrupt          Operation = "interrupt"
)

type OperationLease struct {
	SnapshotEpoch     string
	ControlGeneration uint64
	RelayGeneration   uint64
}

type ControlSessionFact struct {
	Generation        uint64
	ConnectedAt       int64
	HeartbeatAt       int64
	RuntimeReportedAt int64
	Runtime           *AgentRuntimeFact
	RecentErrors      []RecentError
}

type AgentRuntimeFact struct {
	Uptime               int64                                `json:"uptime"`
	CachedTokens         int                                  `json:"cached_tokens"`
	CachedChannels       int                                  `json:"cached_channels"`
	CachedModels         int                                  `json:"cached_models"`
	CachedGlobalRoutings int                                  `json:"cached_global_routings"`
	CachedUserRoutings   int                                  `json:"cached_user_routings"`
	ActiveConnections    int                                  `json:"active_connections"`
	Version              int64                                `json:"version"`
	MasterVersion        int64                                `json:"master_version"`
	PendingUsage         int                                  `json:"pending_usage"`
	CacheStats           map[string]protocol.CacheEntityStats `json:"cache_stats,omitempty"`
	Relay                *RelayRuntimeFact                    `json:"relay,omitempty"`
}

type RelayRuntimeFact struct {
	Desired             RelayDesiredSnapshot
	Active              RelayActiveSnapshot
	Support             string
	Config              string
	Availability        string
	AcceptingNewStreams bool
	Convergence         string
	RecentErrors        []RecentError
}

type ControlSource interface {
	GetControlSession(agentID string) (ControlSessionFact, bool)
}

type RelaySource interface {
	GetRelayRuntime(agentID string) (RelayRuntimeFact, bool)
}

type HealthSource interface {
	ReasonCodes(agentID string) []string
}

type Sources struct {
	Control ControlSource
	Relay   RelaySource
	Health  HealthSource
}

type Options struct {
	HeartbeatDegradedAfter time.Duration
	RecoverySamples        int
	Now                    func() time.Time
	Logger                 *zap.Logger
}

type OperationDeniedError struct {
	Operation  Operation
	DenialCode string
}

func (e *OperationDeniedError) Error() string {
	return fmt.Sprintf("operation %s denied: %s", e.Operation, e.DenialCode)
}
