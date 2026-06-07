package plan

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// ChannelConsumesQuota 返回该候选这次请求是否会从用户额度扣费。
// 与 settler 零成本判定同源:Channel.Free / BYOK 免费模式 / 模型未定价 → 不扣。
func ChannelConsumesQuota(ch *models.Channel, source state.ChannelSource, byokBillingMode string, mc *models.ModelConfig) bool {
	if ch.Free {
		return false
	}
	if source == state.SourcePrivate && byokBillingMode != consts.BYOKBillingModeServiceFee {
		return false
	}
	return modelIsPriced(mc)
}

func modelIsPriced(mc *models.ModelConfig) bool {
	return mc != nil && (mc.InputPrice > 0 || mc.OutputPrice > 0 || mc.CacheReadPrice > 0 || mc.CacheWritePrice > 0)
}
