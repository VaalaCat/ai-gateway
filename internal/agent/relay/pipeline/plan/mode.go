package plan

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/executionmode"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// ModePicker 决定一次 relay attempt 走哪条路径：native / legacy / passthrough。
// Solver 在选完 channel 后调用它给 AttemptPlan 填 Mode 字段。
// 与 Handler 解耦——预判仅依赖 channel + inboundProto + realModel，不读 Handler 字段。
type ModePicker interface {
	Pick(ch *models.Channel, realModel string, inboundProto codec.Protocol) state.RelayMode
}

// defaultModePicker 是 ModePicker 的默认实现。
// Pick 严格复刻原 (*Handler).shouldUseLegacy / (*Handler).shouldPassthrough 的优先级：
//
//	legacy 优先（含 UseLegacyAdaptor / ProtocolUnknown / codec 未注册）→
//	passthrough（inbound == outbound 协议且 PassthroughEnabled）→
//	否则 native
type defaultModePicker struct{}

// Pick 见接口文档。
func (defaultModePicker) Pick(ch *models.Channel, realModel string, inboundProto codec.Protocol) state.RelayMode {
	return executionmode.ForChannel(ch, realModel, inboundProto)
}
