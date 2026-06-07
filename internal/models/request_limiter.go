package models

// RequestLimiter 是一条请求级限流策略：限什么资源、限多少、额度怎么分、超了怎么办。
// 绑定关系另见 LimiterBinding；执行见 internal/agent/relay/limiter。
type RequestLimiter struct {
	ID      uint   `json:"id" gorm:"primaryKey"`
	Name    string `json:"name" gorm:"size:128;index"`
	Enabled bool   `json:"enabled" gorm:"default:true"`

	// 限什么资源
	Metric   string `json:"metric" gorm:"size:16"` // "concurrency" | "rate"
	Capacity int64  `json:"capacity"`              // 并发上限 / 每窗口请求数上限
	WindowMs int    `json:"window_ms"`             // 仅 rate；concurrency 填 0

	// 额度怎么分 + 作用于哪类渠道
	KeyBy        string `json:"key_by" gorm:"size:24"`       // shared|per_user|per_group|per_channel|per_channel_user
	ChannelScope string `json:"channel_scope" gorm:"size:8"` // admin|private|all（仅对 channel-keyed 生效）

	// 超了怎么办
	Action      string `json:"action" gorm:"size:8"` // "reject" | "wait"
	QueueSize   int    `json:"queue_size"`           // wait: 超容量后最多排多少个; 0=不排
	QueueTimeMs int    `json:"queue_time_ms"`        // wait: 最长排队; 0=用全局默认设置

	Priority  int   `json:"priority" gorm:"index"` // 同精细度并列 tie-break + 列表排序
	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

// 枚举取值（用 const 防手滑拼错）。
//
// 扩展契约：新增一个 KeyBy 不只是加常量——必须同步下游 3 处，否则会静默错误：
//  1. (*RequestLimiter).ChannelKeyed() —— 决定该规则落请求级还是尝试级闸门；
//  2. cache.LimiterIndex.Effective{Request,Attempt}Limiters 的入参 —— 新分桶维度
//     (如 per_model) 需要新数据从热路径透传进来；
//  3. limiter.bucketOf() 的 switch —— 其 default 会把未识别 KeyBy 静默并入 "shared" 桶。
const (
	LimiterMetricConcurrency = "concurrency"
	LimiterMetricRate        = "rate"

	LimiterKeyShared         = "shared"
	LimiterKeyPerUser        = "per_user"
	LimiterKeyPerGroup       = "per_group"
	LimiterKeyPerChannel     = "per_channel"
	LimiterKeyPerChannelUser = "per_channel_user"

	LimiterScopeAdmin   = "admin"
	LimiterScopePrivate = "private"
	LimiterScopeAll     = "all"

	LimiterActionReject = "reject"
	LimiterActionWait   = "wait"
)

// ChannelKeyed 报告该 KeyBy 是否依赖具体渠道（决定落请求级还是尝试级闸门）。
func (l *RequestLimiter) ChannelKeyed() bool {
	return l.KeyBy == LimiterKeyPerChannel || l.KeyBy == LimiterKeyPerChannelUser
}

// ValidBindingTarget 报告某 KeyBy 的 limiter 能否绑到某 TargetType（§5.1 硬约束）。
// 这是领域规则：分桶维度(KeyBy)限定了绑定挂载点(TargetType)的合法集合，前后端共用判断。
//   - shared            → 只能 global（无分桶维度，挂全局）
//   - per_user          → global / user_group / user（按用户分桶，可在这三层覆盖）
//   - per_group         → global / user_group（按用户组分桶，挂到具体用户无意义）
//   - per_channel       → global / channel（按渠道分桶）
//   - per_channel_user  → global / channel（按渠道×用户分桶，挂载点仍是渠道）
func ValidBindingTarget(keyBy, targetType string) bool {
	switch keyBy {
	case LimiterKeyShared:
		return targetType == LimiterTargetGlobal
	case LimiterKeyPerUser:
		return targetType == LimiterTargetGlobal || targetType == LimiterTargetUserGroup || targetType == LimiterTargetUser
	case LimiterKeyPerGroup:
		return targetType == LimiterTargetGlobal || targetType == LimiterTargetUserGroup
	case LimiterKeyPerChannel, LimiterKeyPerChannelUser:
		return targetType == LimiterTargetGlobal || targetType == LimiterTargetChannel
	}
	return false
}

// ValidAction 报告超容量处置方式是否合法（reject 拒绝 / wait 排队）。
func ValidAction(action string) bool {
	return action == LimiterActionReject || action == LimiterActionWait
}

// ValidChannelScope 报告渠道作用域是否合法。空串视为 admin（向后兼容默认），
// 仅 channel-keyed 规则真正使用此值，但写入侧统一拦脏枚举防后续静默漂移。
func ValidChannelScope(scope string) bool {
	switch scope {
	case "", LimiterScopeAdmin, LimiterScopePrivate, LimiterScopeAll:
		return true
	}
	return false
}

// RateLimitHit 是一条 limiter 在某次请求里的命中明细（落 usage_log 的 JSONSlice 列）。
type RateLimitHit struct {
	LimiterID uint   `json:"limiter_id"`
	Name      string `json:"name"`
	Dimension string `json:"dimension"` // metric/key_by，如 "concurrency/per_channel"
	Bucket    string `json:"bucket"`    // 命中的分桶键（bucketOf），如 "c:channel:42"，排障定位到具体桶
	Decision  string `json:"decision"`  // allow|queued|rejected
	WaitMs    int    `json:"wait_ms"`
}
