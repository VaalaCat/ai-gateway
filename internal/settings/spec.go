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
}
