package exec

import (
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

// logAttemptFailed 复刻 main:handler.go 老主循环 attempt 失败分支的 "relay attempt failed" Warn 日志。
// 字段对齐 main：channel_id / attempts_left / path / error。
//
// 注意：attempts_left 语义对齐 Planner truncate 后的剩余 attempts。
// main 老主循环用全局 RetryMax 累减；新流程因为 Plan.Attempts 已被 Planner 按
// RetryMax 截断（multi-model 链共享同一全局预算），剩余 attempt 数就是真实
// 剩余尝试机会——比老语义更准确，但若 ops 监控基于具体值需更新。
// path 字段对齐 main 的 nativeOrLegacy(useLegacy)：legacy → "legacy"，其它 → "native"
// （main 没区分 passthrough，与 native 同 label）。
// rctx.Agent.GetLogger() == nil 时静默跳过（测试 stub 可能返回 nil）。
func logAttemptFailed(rctx *state.RelayContext, a state.Attempt, err error, attemptsLeft int) {
	logger := rctx.Agent.GetLogger()
	if logger == nil {
		return
	}
	path := "native"
	if a.Mode == state.ModeLegacy {
		path = "legacy"
	}
	channelID := uint(0)
	if a.Channel != nil {
		channelID = a.Channel.ID
	}
	logger.Warn("relay attempt failed",
		zap.Uint("channel_id", channelID),
		zap.Int("attempts_left", attemptsLeft),
		zap.String("path", path),
		zap.Error(err),
	)
}
