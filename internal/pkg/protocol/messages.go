package protocol

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type SyncPushParams struct {
	Entity  string `json:"entity"`
	Action  string `json:"action"`
	Data    []byte `json:"data"`
	Version int64  `json:"version"`
}

type FullSyncRequest struct {
	Entity   string `json:"entity"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

type FullSyncResponse struct {
	Items   []byte `json:"items"`
	Total   int64  `json:"total"`
	Page    int    `json:"page"`
	HasMore bool   `json:"has_more"`
	Version int64  `json:"version"`
}

type GetVersionResponse struct {
	Version int64 `json:"version"`
}

type ForceFullSyncResponse struct {
	Version    int64 `json:"version"`
	DurationMs int64 `json:"duration_ms"`
}

type UsageReport struct {
	AgentID string          `json:"agent_id"`
	Logs    []UsageLogEntry `json:"logs"`
}

type UsageLogEntry struct {
	RequestID        string `json:"request_id"`
	UserID           uint   `json:"user_id"`
	TokenID          uint   `json:"token_id"`
	ChannelID        uint   `json:"channel_id"`
	PrivateChannelID uint   `json:"private_channel_id"`
	OwnerType        string `json:"owner_type"`
	ModelName        string `json:"model_name"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	IsStream         bool   `json:"is_stream"`
	Duration         int    `json:"duration"`
	ClientIP         string `json:"client_ip"`
	Timestamp        int64  `json:"timestamp"`

	// Enhanced logging fields
	TokenName        string `json:"token_name"`
	UpstreamModel    string `json:"upstream_model"`
	FirstResponseMs  int    `json:"first_response_ms"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
	InboundProtocol  string `json:"inbound_protocol"`
	OutboundProtocol string `json:"outbound_protocol"`
	UseLegacy        bool   `json:"use_legacy"`
	Status           int    `json:"status"`
	ErrorMessage     string `json:"error_message,omitempty"`
	TraceData        string `json:"trace_data,omitempty"`
	Other            string `json:"other"`
	TokenSource      string `json:"token_source,omitempty"`
	RoutingName      string `json:"routing_name,omitempty"`

	// Channel affinity（渠道缓存粘性）
	AffinityStatus   string `json:"affinity_status,omitempty"` // "hit" | "fallback" | "none" | ""
	AffinityRecorded bool   `json:"affinity_recorded,omitempty"`

	ErrorStage         string `json:"error_stage,omitempty"`
	InboundDecodeMs    int    `json:"inbound_decode_ms,omitempty"`
	OutboundEncodeMs   int    `json:"outbound_encode_ms,omitempty"`
	UpstreamDispatchMs int    `json:"upstream_dispatch_ms,omitempty"`
	UpstreamDecodeMs   int    `json:"upstream_decode_ms,omitempty"`
	ClientEncodeMs     int    `json:"client_encode_ms,omitempty"`

	// PriceRatio 是 relay 选中的公共 channel 在请求时刻的计费倍率快照。
	// 零值 0(旧 agent 未发 / channel 未配)= 原价,settler 归一到 1.0。
	PriceRatio float64 `json:"price_ratio,omitempty"`

	// Free 是 relay 选中的公共 channel 在请求时刻的"免费渠道"标记快照。
	// private 行不填,保持 false。settler 见 true 则四桶成本清零。
	Free bool `json:"free,omitempty"`

	// FallbackChain 是本次请求的候选链路（每候选一条）；空表示无链路信息。
	FallbackChain []models.AttemptRecord `json:"fallback_chain,omitempty"`
	// AttemptTraces 是逐候选 mask 过的 header/body trace，index 对应 FallbackChain 的 Seq-1。
	// AttemptIndex assigned by settler from slice order until Task 5 adds the field.
	AttemptTraces []models.UsageLogTrace `json:"attempt_traces,omitempty"`

	// 限流决策（请求级 limiter）：Decision/WaitMs/Reason 标量 + Hits 命中明细。
	// 无命中时全为零值/空，omitempty 不上报。
	RateLimitDecision string                `json:"rate_limit_decision,omitempty"`
	RateLimitWaitMs   int                   `json:"rate_limit_wait_ms,omitempty"`
	RateLimitReason   string                `json:"rate_limit_reason,omitempty"`
	RateLimitHits     []models.RateLimitHit `json:"rate_limit_hits,omitempty"`
}

type HeartbeatParams struct {
	Uptime               int64 `json:"uptime"`
	CachedTokens         int   `json:"cached_tokens"`
	CachedChannels       int   `json:"cached_channels"`
	CachedModels         int   `json:"cached_models"`
	CachedGlobalRoutings int   `json:"cached_global_routings"`
	CachedUserRoutings   int   `json:"cached_user_routings"`
	ActiveConnections    int   `json:"active_connections"`
	Version              int64 `json:"version"`

	HTTPAddresses json.RawMessage `json:"http_addresses,omitempty"`
	Tags          string          `json:"tags,omitempty"`
	ProxyURL      string          `json:"proxy_url,omitempty"`
	ListenPort    int             `json:"listen_port,omitempty"`

	CacheStats map[string]CacheEntityStats `json:"cache_stats,omitempty"`
}

// CacheEntityStats 是单个实体缓存的运行统计。
// LRU 模式实体上报全字段；Full 模式实体仅 Size 有意义、其他字段为 0。
type CacheEntityStats struct {
	Kind          string           `json:"kind,omitempty"` // "lru" | "index"；旧 agent 上报空串，前端按 lru 兜底
	Hits          int64            `json:"hits"`
	Misses        int64            `json:"misses"`
	Evictions     int64            `json:"evictions"`
	NegativeHits  int64            `json:"negative_hits"`
	LoadErrors    int64            `json:"load_errors"`
	Invalidations int64            `json:"invalidations"`
	Size          int              `json:"size"`
	Capacity      int              `json:"capacity"`
	Extra         map[string]int64 `json:"extra,omitempty"` // 索引细分，如 {"limiters":12,"bindings":30}
}

// LimiterBucketStat 是一个 (限流器,桶) 的实时仪表读数。agent 采样 + join 规则元数据后上报。
type LimiterBucketStat struct {
	LimiterID   uint   `json:"limiter_id"`
	Name        string `json:"name"`
	Bucket      string `json:"bucket"`
	Metric      string `json:"metric"`        // concurrency | rate
	KeyBy       string `json:"key_by"`
	Occupied    int64  `json:"occupied"`      // 并发:当前占用; 速率:当前窗口已用
	Capacity    int64  `json:"capacity"`
	Waiters     int64  `json:"waiters"`
	WindowEndMs int64  `json:"window_end_ms"` // 速率桶本窗口复位时刻(unix ms);并发为 0
}

// FetchEntityRequest 是 sync.fetchEntity 的入参。
// Entity 取 events.Entity* 常量；Key 由各实体 handler 解读
// （token: API key 字符串；user: id 字符串）。
type FetchEntityRequest struct {
	Entity string `json:"entity"`
	Key    string `json:"key"`
}

// FetchEntityResponse 是 sync.fetchEntity 的响应。
// Found=false 表示 master 也未查到；调用方应进入负缓存。
// Side 是可选旁路负载（如 token 响应附带的 SyncedUser），由 agent 端 fetcher 解读。
type FetchEntityResponse struct {
	Found bool            `json:"found"`
	Data  json.RawMessage `json:"data,omitempty"`
	Side  json.RawMessage `json:"side,omitempty"`
}
