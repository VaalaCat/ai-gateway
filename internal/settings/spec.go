// Package settings 定义 master 与 agent 共享的全局同步配置 schema。
//
// 单一职责:AgentSettings struct 是所有"会从 master 同步到 agent 内存"配置项的
// source of truth。加新同步配置 = 给 struct 加一个字段配 tag,master 端的
// default seed / 校验、agent 端的 apply / snapshot 都通过反射自动生效。
//
// 这个包不依赖 models / dao / events,任何模块都能 import,无循环风险。
package settings

// AgentSettings 是 agent 从 master 同步过来的全局设置快照。
//
// 字段添加规则:
//   - 字段类型: int(其他类型需扩展 reflect.go 的 parse/assign 分支)
//   - tag 格式: setting:"<key>,<default>,<min>,<max>"
//   - key 在整个 struct 内唯一
//
// 该 struct 通过 atomic.Pointer 在 agent cache.Store 内保存当前快照,
// 调用方走 cache.Settings() 拿到 value copy(immutable,无需锁)。
type AgentSettings struct {
	TraceMaxBodySize int `setting:"trace_max_body_size,65536,4096,16777216"`
	FallbackSleepMs  int `setting:"fallback_sleep_ms,1000,0,60000"`
	AffinityEnabled  int `setting:"affinity_enabled,1,0,1"`
	AffinityTTLSec   int `setting:"affinity_ttl_sec,300,0,86400"`

	// Channel 韧性(failsafe)默认参数;每 channel 可在渠道表单覆盖。
	MaxRetriesPerChannel int `setting:"max_retries_per_channel,2,0,10"`
	RetryBackoffBaseMs   int `setting:"retry_backoff_base_ms,200,0,60000"`
	RetryBackoffMaxMs    int `setting:"retry_backoff_max_ms,2000,0,60000"`
	BreakerThreshold     int `setting:"breaker_threshold,5,1,1000"`
	BreakerCooldownMs    int `setting:"breaker_cooldown_ms,30000,0,3600000"`

	// RetryMaxChannels 是跨 channel 降级轮数(attempt 总预算);plan 按此截断候选链。
	RetryMaxChannels int `setting:"retry_max_channels,5,1,100"`

	// BYOKBillingMode 同步自全局 byok_billing_mode,供闸门判定 BYOK 渠道是否扣额度。
	BYOKBillingMode string `setting:"byok_billing_mode,free"`

	// MinQuotaReserve 付费渠道放行所需的最低剩余额度;Quota<=此值拦付费、只放免费。
	MinQuotaReserve int64 `setting:"min_quota_reserve,0,0,9223372036854775807"`

	// 请求级限流器（RequestLimiter）
	RateLimiterEnabled int `setting:"rate_limiter_enabled,1,0,1"`        // 全局总开关，0=整体 bypass
	SSEKeepaliveMs     int `setting:"sse_keepalive_ms,15000,1000,60000"` // stream 排队保活帧间隔
	QueueTimeMs        int `setting:"queue_time_ms,120000,0,600000"`     // 默认最长排队（limiter QueueTimeMs=0 时取此）

	// 集群健康状态阈值（前端红绿灯 / 管理员后台可配）
	HealthErrorRateYellowPct  int `setting:"health_error_rate_yellow_pct,2,0,100"`    // 错误率黄阈(%)
	HealthErrorRateRedPct     int `setting:"health_error_rate_red_pct,10,0,100"`      // 错误率红阈(%)
	HealthSaturationYellowPct int `setting:"health_saturation_yellow_pct,80,0,100"`   // 限流饱和黄阈(%)
	HealthSaturationRedPct    int `setting:"health_saturation_red_pct,95,0,100"`      // 限流饱和红阈(%)
	HealthOfflineSeconds      int `setting:"health_offline_seconds,90,10,3600"`       // 超此 last_seen 算掉线
	HealthWindowSeconds       int `setting:"health_window_seconds,300,60,3600"`       // 错误率/QPS 近窗

	// 实体缓存 stale-while-revalidate(详见 2026-06-06-ws-call-hang-swr-cache spec)
	CacheLoadTimeoutMs        int `setting:"cache_load_timeout_ms,5000,1000,60000"`            // 冷 miss 阻塞加载超时
	CacheRefreshAfterMs       int `setting:"cache_refresh_after_ms,172800000,0,604800000"`     // 条目多旧触发后台刷新(48h),0=关
	CacheRefreshTimeoutMs     int `setting:"cache_refresh_timeout_ms,5000,1000,60000"`         // 单次后台刷新尝试超时
	CacheRefreshMaxRetries    int `setting:"cache_refresh_max_retries,3,0,10"`                 // 单次触发内重试次数
	CacheRefreshBackoffBaseMs int `setting:"cache_refresh_backoff_base_ms,200,0,60000"`        // 退避初始
	CacheRefreshBackoffMaxMs  int `setting:"cache_refresh_backoff_max_ms,5000,0,60000"`        // 退避上限
}
