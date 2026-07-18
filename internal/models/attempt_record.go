package models

type AgentPathKind string
type AgentPathResult string
type AgentPathStage string
type AgentPathCommitState string

const (
	AgentPathLocal  AgentPathKind = "local"
	AgentPathDirect AgentPathKind = "direct"
	AgentPathRelay  AgentPathKind = "relay"

	AgentPathSelected    AgentPathResult = "selected"
	AgentPathUnavailable AgentPathResult = "unavailable"
	AgentPathRejected    AgentPathResult = "rejected"
	AgentPathUncertain   AgentPathResult = "uncertain"

	AgentPathConnect      AgentPathStage = "connect"
	AgentPathAuthenticate AgentPathStage = "authenticate"
	AgentPathDispatch     AgentPathStage = "dispatch"
	AgentPathResponse     AgentPathStage = "response"

	AgentPathNotCommitted    AgentPathCommitState = "not_committed"
	AgentPathCommitted       AgentPathCommitState = "committed"
	AgentPathCommitUncertain AgentPathCommitState = "uncertain"
)

type AgentPathRecord struct {
	AgentID     string               `json:"agent_id"`
	Path        AgentPathKind        `json:"path"`
	Result      AgentPathResult      `json:"result"`
	Stage       AgentPathStage       `json:"stage"`
	CommitState AgentPathCommitState `json:"commit_state"`
	ReasonCode  string               `json:"reason_code,omitempty"`
	DurationMs  int                  `json:"duration_ms"`
}

// AttemptRecord 是 fallback 链路里的一条:一次候选 channel 尝试的摘要。
// 同 channel 的内层重试折叠成 Retries 计数,不单独成条。
// 定义在 models 包以便 state / protocol / settler 共用而不成环。
type AttemptRecord struct {
	Seq            int               `json:"seq"`        // 序号,从 1
	ChannelID      uint              `json:"channel_id"` // admin=Channel.ID, private=PrivateChannel.ID
	ChannelName    string            `json:"channel_name"`
	Source         string            `json:"source"` // admin / private
	RealModel      string            `json:"real_model"`
	Retries        int               `json:"retries"` // 同 channel 内层重试次数(0=首发即终)
	ByAffinity     bool              `json:"by_affinity,omitempty"`
	BreakerOpen    bool              `json:"breaker_open,omitempty"` // 该候选因熔断 open 被跳过
	HTTPStatus     int               `json:"http_status,omitempty"`
	Status         string            `json:"status"` // ok / fail
	ErrorType      string            `json:"error_type,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	DurationMs     int               `json:"duration_ms"`
	HasTrace       bool              `json:"has_trace,omitempty"` // 该候选是否写了 trace 行(= 快照 verbose),供前端按条目决定是否显示调试按钮
	AgentRouteID   uint              `json:"agent_route_id,omitempty"`
	AgentRouteKind string            `json:"agent_route_kind,omitempty"`
	AgentPaths     []AgentPathRecord `json:"agent_paths,omitempty"`
}
