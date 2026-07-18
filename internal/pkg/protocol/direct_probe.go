package protocol

const (
	DirectIngressContractV1   = "direct_ingress_v1"
	DirectIngressIdentityPath = "/v1/direct-ingress/identity"
)

type DirectIngressIdentity struct {
	Contract string `json:"contract"`
	Role     string `json:"role"`
	AgentID  string `json:"agent_id"`
}

// Address is the shared agent HTTP address value used by probe and routing APIs.
type Address struct {
	URL string `json:"url"`
	Tag string `json:"tag"`
}

type DirectProbeTarget struct {
	TargetAgentID      string    `json:"target_agent_id"`
	Addresses          []Address `json:"addresses"`
	EffectiveProxy     string    `json:"effective_proxy"`
	AddressFingerprint string    `json:"address_fingerprint"`
	TargetGeneration   uint64    `json:"target_generation"`
}

type DirectProbeResult struct {
	TargetAgentID      string `json:"target_agent_id"`
	AddressFingerprint string `json:"address_fingerprint"`
	Network            string `json:"network"`
	Identity           string `json:"identity"`
	Eligible           bool   `json:"eligible"`
	LatencyMS          int64  `json:"latency_ms"`
	CheckedAt          int64  `json:"checked_at"`
	ReasonCode         string `json:"reason_code,omitempty"`
}

type ProbeScope struct {
	Kind           string   `json:"kind"`
	Tag            string   `json:"tag,omitempty"`
	TargetAgentIDs []string `json:"target_agent_ids,omitempty"`
}

type ProbeAck struct {
	ProbeID         string     `json:"probe_id"`
	ProbeGeneration uint64     `json:"probe_generation"`
	Scope           ProbeScope `json:"scope"`
	State           string     `json:"state"`
	TargetTotal     int        `json:"target_total"`
	SnapshotSeq     uint64     `json:"snapshot_seq"`
}

type ManualProbeProgress struct {
	ProbeID     string `json:"probe_id"`
	State       string `json:"state"`
	TargetTotal int    `json:"target_total"`
	Remaining   int    `json:"remaining"`
	StartedAt   int64  `json:"started_at,omitempty"`
	CompletedAt int64  `json:"completed_at,omitempty"`
}
