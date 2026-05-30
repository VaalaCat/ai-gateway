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
}
