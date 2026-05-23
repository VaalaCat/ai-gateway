package insights

import "github.com/VaalaCat/ai-gateway/internal/dao"

// GetRequest 是 /v1/insights 入参。
// type=agent|channel|model|user|token; id 是该 entity 的主键 (agent_id, channel_id, ...)。
// start/end 缺省 now-86400 / now;gran 缺省 "day"。
type GetRequest struct {
	Type  string `form:"type"`
	ID    string `form:"id"`
	Start int64  `form:"start"`
	End   int64  `form:"end"`
	Gran  string `form:"gran"`
}

// GetResponse 是 /v1/insights 返回结构。
// 字段保持稳定:即便某个 provider 不填 StageLatency,也返回 null 而不是 omitempty 缺失;
// 这样前端不用判断字段存在性,只判 null。
type GetResponse struct {
	Meta         EntityMeta        `json:"meta"`
	Summary      SummaryKpis       `json:"summary"`
	Trend        TrendBlock        `json:"trend"`
	StageLatency *dao.StageLatency `json:"stage_latency"`
	Breakdown    Breakdown         `json:"breakdown"`
	Errors       []ErrorSample     `json:"errors"`
}

// TrendBlock 是 /v1/insights 的时间序列块。
// Metrics 列出 buckets 中含哪些字段 (cost/requests/tokens),给前端选择 y-axis。
type TrendBlock struct {
	Buckets []dao.TimeBucket `json:"buckets"`
	Metrics []string         `json:"metrics"`
}

// EntityMeta 是 entity 自身的描述信息。
// 字段语义按 type 不同:
//   agent:   ID=agent_id, Name=agent.Name, Online/LastSeen 由 agents 表填
//   其它:   暂未实现 (stub)
type EntityMeta struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Online   bool   `json:"online,omitempty"`
	LastSeen int64  `json:"last_seen,omitempty"`
	Region   string `json:"region,omitempty"`
	Version  string `json:"version,omitempty"`
	JoinedAt int64  `json:"joined_at,omitempty"`
}

// SummaryKpis 是 entity 在窗口期内的统计概览。
// 这些数字对应前端右侧 "KPI 速览" 区域,与 stage_latency / breakdown 是不同抽屉。
type SummaryKpis struct {
	Requests    int64   `json:"requests"`
	Cost        int64   `json:"cost"`
	Tokens      int64   `json:"tokens"`
	SuccessRate float64 `json:"success_rate"`
	TTFTP95Ms   int64   `json:"ttft_p95_ms"`
	TPSAvg      float64 `json:"tps_avg"`
}

// Breakdown 是 entity 的下钻维度。不同 provider 填不同子集:
//   agent provider → ByModel + ByChannel (一个 agent 内 model 用量、channel 用量)
//   其它 provider → 自行选择
// 空 slice 用 omitempty 隐藏。
type Breakdown struct {
	ByModel   []dao.LeaderRow `json:"by_model,omitempty"`
	ByChannel []dao.LeaderRow `json:"by_channel,omitempty"`
	ByAgent   []dao.LeaderRow `json:"by_agent,omitempty"`
	ByUser    []dao.LeaderRow `json:"by_user,omitempty"`
	ByToken   []dao.LeaderRow `json:"by_token,omitempty"`
}

// ErrorSample 是 entity 最近的失败样本 (按时间倒序前 N 条)。
// 字段集尽量稳定:前端用来展示一张 "最近错误" 表,允许部分字段为空。
type ErrorSample struct {
	Ts      int64  `json:"ts"`
	Stage   string `json:"stage,omitempty"`
	Channel string `json:"channel,omitempty"`
	Model   string `json:"model,omitempty"`
	Message string `json:"message"`
}
