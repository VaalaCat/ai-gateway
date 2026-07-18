package protocol

type RouteEvent struct {
	RequestID          string                    `json:"request_id,omitempty"`
	TargetAgentID      string                    `json:"target_agent_id"`
	RouteID            uint                      `json:"route_id"`
	SelectorKind       string                    `json:"selector_kind"`
	PathKind           string                    `json:"path_kind"`
	Result             string                    `json:"result"`
	Stage              string                    `json:"stage"`
	ReasonCode         string                    `json:"reason_code"`
	CommitState        string                    `json:"commit_state"`
	AddressFingerprint string                    `json:"address_fingerprint"`
	DurationMS         int64                     `json:"duration_ms"`
	ObservedAt         int64                     `json:"observed_at"`
	Sequence           uint64                    `json:"sequence,omitempty"`
	Failures           []RouteFailureObservation `json:"failures,omitempty"`
}

// RouteFailureObservation describes one failed transport attempt without
// changing the final route event fields used by connectivity edge summaries.
type RouteFailureObservation struct {
	PathKind    string `json:"path_kind"`
	Stage       string `json:"stage"`
	CommitState string `json:"commit_state"`
	ReasonCode  string `json:"reason_code"`
}

type RouteEdgeSnapshot struct {
	TargetAgentID      string `json:"target_agent_id"`
	RouteID            uint   `json:"route_id"`
	SelectorKind       string `json:"selector_kind"`
	LastUsedAt         int64  `json:"last_used_at"`
	LastDirectResult   string `json:"last_direct_result"`
	AddressFingerprint string `json:"address_fingerprint"`
	SuccessCount       uint64 `json:"success_count"`
	LatencyTotalMS     uint64 `json:"latency_total_ms"`
}

type RouteTelemetryBatch struct {
	Generation uint64       `json:"generation"`
	Events     []RouteEvent `json:"events"`
}

type RouteEdgeDigest struct {
	Generation     uint64              `json:"generation"`
	Edges          []RouteEdgeSnapshot `json:"edges"`
	CoveredThrough uint64              `json:"covered_through,omitempty"`
}
