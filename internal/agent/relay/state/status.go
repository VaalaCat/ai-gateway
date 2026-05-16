package state

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// StatusFromState 把当前 FailPhase + Err 映射到 (HTTP status, error message)。
// 行为对齐 legacy handler.go 主流程：
//   - read body / invalid JSON / missing model → 400；
//   - invalid X-Channel-ID / no routable model / no channel available / model not allowed → 404；
//   - ErrRoutingFallback / Executor 阶段失败 → 502 BadGateway。
//
// 该函数同时是 user-facing error message 的单一来源：HTTP body / UsageLog.ErrorMessage /
// Trace fail 都应该取这里返回的 msg，避免出现 "HTTP body 带 model 名但 UsageLog 拿到 sentinel
// 短文本" 的漂移。
func StatusFromState(rctx *RelayContext) (int, string) {
	err := rctx.State.Err
	if err == nil {
		return http.StatusInternalServerError, ""
	}

	switch {
	case errors.Is(err, ErrReadBody),
		errors.Is(err, ErrInvalidBody),
		errors.Is(err, ErrModelRequired):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrInvalidForcedChannelID):
		// 老行为：malformed X-Channel-ID 也走 "no channel available for model <name>" 文案。
		return http.StatusNotFound, fmt.Sprintf("no channel available for model %s", rctx.Input.Model)
	case errors.Is(err, ErrNoRoutableModel):
		return http.StatusNotFound, "no available real model after routing: " + rctx.Input.Model
	case errors.Is(err, ErrModelNotAllowed):
		return http.StatusNotFound, fmt.Sprintf("model not allowed: %s", rctx.Input.Model)
	case errors.Is(err, ErrNoChannelAvailable):
		// "whitelist active" 后缀在 ErrNoChannelAvailable 路径上由 user info 推断附加，
		// 复刻老主循环 "no channel available" 文案末尾 (token whitelist active) 的拼接行为。
		msg := fmt.Sprintf("no channel available for model %s", rctx.Input.Model)
		if WhitelistActiveFor(rctx.Input.UserInfo) {
			msg += " (token whitelist active)"
		}
		return http.StatusNotFound, msg
	case errors.Is(err, ErrRoutingFallback):
		// 老主循环 routing 兜底分支：routing 整链耗尽 + lastErr==nil → 502 + "no available channels"。
		// err.Error() 本身就是 consts.ErrNoChannelAvailable。
		return http.StatusBadGateway, err.Error()
	}

	// 其它（含 Executor 阶段冒上来的 upstream/codec 错误）→ 502。
	return http.StatusBadGateway, err.Error()
}

// UserFacingErrorMessage 返回与 HTTP body 一致的、面向用户的错误文本。
// 老 handler.go 在 publishUsage / rec.WithFail / c.JSON 三处都使用同一份带 model 名的 errMsg，
// publish.Publisher 和 Trace 必须用本函数取，确保 UsageLog.ErrorMessage 和 trace.fail.err 不丢 model 名。
func UserFacingErrorMessage(rctx *RelayContext) string {
	if rctx == nil || rctx.State == nil || rctx.State.Err == nil {
		return ""
	}
	_, msg := StatusFromState(rctx)
	return msg
}

// WhitelistActiveFor 判定 UserInfo 是否启用了任一层 channel 白名单。
// 这里只看 channel 层；model 白名单（TokenModels / GroupModels）不影响这个标记。
func WhitelistActiveFor(ui *app.UserInfo) bool {
	if ui == nil {
		return false
	}
	return len(ui.AllowedChannelIDs) > 0 || len(ui.GroupAllowedChannelIDs) > 0
}

// MsOf 把 time.Duration 截成整毫秒，用于 UsageLog 的各 *Ms 字段。
func MsOf(d time.Duration) int {
	return int(d / time.Millisecond)
}
