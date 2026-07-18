package protocol

type RelayProbeState string

const (
	RelayProbeReachable   RelayProbeState = "reachable"
	RelayProbeUnreachable RelayProbeState = "unreachable"
	RelayProbeUnavailable RelayProbeState = "unavailable"
	RelayProbeUnknown     RelayProbeState = "unknown"
)

type RelayProbeStage string

const (
	RelayProbeStageOpen     RelayProbeStage = "open"
	RelayProbeStageCommit   RelayProbeStage = "commit"
	RelayProbeStageResponse RelayProbeStage = "response"
)

type RelayProbeTarget struct {
	TargetAgentID         string `json:"target_agent_id"`
	SourceRelayGeneration uint64 `json:"source_relay_generation"`
	TargetRelayGeneration uint64 `json:"target_relay_generation"`
}

type RelayProbeResult struct {
	TargetAgentID string          `json:"target_agent_id"`
	State         RelayProbeState `json:"state"`
	Stage         RelayProbeStage `json:"stage,omitempty"`
	LatencyMS     int64           `json:"latency_ms"`
	CheckedAt     int64           `json:"checked_at"`
	ReasonCode    string          `json:"reason_code,omitempty"`
}
