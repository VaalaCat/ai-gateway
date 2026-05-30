package resilience

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

// Decision 是分类表对一次 attempt 结果的判定。
type Decision struct {
	RetrySameChannel bool // 是否对同一 channel 重试
	CountToBreaker   bool // 是否计入该 channel 熔断失败
	AbortAll         bool // 是否整体放弃(不再重试也不转下一 channel)
}

// Classify 按 spec §1.3 错误分类表判定。无错误 → 全 false(成功)。
func Classify(res state.AttemptResult) Decision {
	if res.Err == nil {
		return Decision{}
	}
	// 已写出 partial / 客户端断连 → 整体放弃,不记熔断(channel 是通的 / 用户主动断)。
	if res.Written || errors.Is(res.Err, context.Canceled) {
		return Decision{AbortAll: true}
	}
	var upErr *common.UpstreamError
	if !errors.As(res.Err, &upErr) {
		// 非结构化错误,当作网络层可重试。
		return Decision{RetrySameChannel: true, CountToBreaker: true}
	}
	switch {
	case upErr.Status == 400 && upErr.ProviderErrorType == "invalid_request_error":
		return Decision{AbortAll: true} // 用户的错,绝不开熔断
	case upErr.Status == 0 || upErr.Status >= 500 || upErr.Status == 429:
		return Decision{RetrySameChannel: true, CountToBreaker: true}
	case upErr.Status == 401 || upErr.Status == 403:
		return Decision{CountToBreaker: true} // 凭证坏,不重试但转下一 channel,计熔断
	default:
		return Decision{} // 其它 4xx:转下一 channel,不重试不熔断
	}
}
