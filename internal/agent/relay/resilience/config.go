// Package resilience 给单次 channel dispatch 套重试/熔断/超时。叶子包,只依赖
// state / utils / failsafe-go,不反向 import plan/exec/publish。
package resilience

import "github.com/VaalaCat/ai-gateway/internal/models"

// Config 是某 channel 最终生效的韧性参数(全局默认 ⊕ 每 channel 覆盖后的结果)。
type Config struct {
	MaxRetries        int  // 单 channel 内最大重试次数(不含首发)
	BackoffBaseMs     int  // 指数退避初始间隔
	BackoffMaxMs      int  // 指数退避上限
	BreakerThreshold  int  // 连续失败多少次后 open
	BreakerCooldownMs int  // open 后多久转 half-open
	BreakerEnabled    bool // false = retry/fallback only; no circuit breaker state
}

// ChannelResilience 复用 models 定义,避免循环 import。
// resilience→state→models 若 models 也 import resilience 则成环;
// 把 struct 定义移到 models,这里以类型别名透出供调用方直接用。
type ChannelResilience = models.ChannelResilience

// 注:全局韧性默认值不在此处。它们走管理后台 Settings(internal/settings 的
// AgentSettings tag 默认),Runner 每请求从 cache.Settings() 实时读取后再 Resolve
// 每 channel 覆盖。这样 admin 后台改参数即时生效,无需改 config 或重启。

// Resolve 把全局默认与每 channel 覆盖逐字段合并:override 为 nil 的字段用 global。
func Resolve(global Config, o *ChannelResilience) Config {
	if o == nil {
		return global
	}
	out := global
	if o.MaxRetries != nil {
		out.MaxRetries = *o.MaxRetries
	}
	if o.BackoffBaseMs != nil {
		out.BackoffBaseMs = *o.BackoffBaseMs
	}
	if o.BackoffMaxMs != nil {
		out.BackoffMaxMs = *o.BackoffMaxMs
	}
	if o.BreakerThreshold != nil {
		out.BreakerThreshold = *o.BreakerThreshold
	}
	if o.BreakerCooldownMs != nil {
		out.BreakerCooldownMs = *o.BreakerCooldownMs
	}
	if o.BreakerEnabled != nil {
		out.BreakerEnabled = *o.BreakerEnabled
	}
	return out
}
