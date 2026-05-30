package models

// AttemptRecord 是 fallback 链路里的一条:一次候选 channel 尝试的摘要。
// 同 channel 的内层重试折叠成 Retries 计数,不单独成条。
// 定义在 models 包以便 state / protocol / settler 共用而不成环。
type AttemptRecord struct {
	Seq          int    `json:"seq"`                    // 序号,从 1
	ChannelID    uint   `json:"channel_id"`             // admin=Channel.ID, private=PrivateChannel.ID
	ChannelName  string `json:"channel_name"`
	Source       string `json:"source"`                 // admin / private
	RealModel    string `json:"real_model"`
	Retries      int    `json:"retries"`                // 同 channel 内层重试次数(0=首发即终)
	ByAffinity   bool   `json:"by_affinity,omitempty"`
	BreakerOpen  bool   `json:"breaker_open,omitempty"` // 该候选因熔断 open 被跳过
	HTTPStatus   int    `json:"http_status,omitempty"`
	Status       string `json:"status"`                 // ok / fail
	ErrorType    string `json:"error_type,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	DurationMs   int    `json:"duration_ms"`
	HasTrace     bool   `json:"has_trace,omitempty"` // 该候选是否写了 trace 行(= 快照 verbose),供前端按条目决定是否显示调试按钮
}
