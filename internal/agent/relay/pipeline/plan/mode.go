package plan

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
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
	if shouldUseLegacy(ch, inboundProto, realModel) {
		return state.ModeLegacy
	}
	if shouldPassthrough(ch, inboundProto, realModel) {
		return state.ModePassthrough
	}
	return state.ModeNative
}

// shouldUseLegacy 由 ModePicker 调用，预判 channel 是否需要走 legacy adaptor。
func shouldUseLegacy(ch *models.Channel, inboundProto codec.Protocol, modelName string) bool {
	if ch == nil {
		return false
	}
	// 显式声明走 legacy
	if ch.UseLegacyAdaptor {
		return true
	}
	// 未知 inbound 协议没有 native codec，回退 legacy
	if inboundProto == codec.ProtocolUnknown {
		return true
	}
	// inbound / outbound codec 未注册同样走 legacy
	rules := upstream.ChannelOverrideRulesFor(ch)
	override := upstream.ResolveOverride(rules, modelName)
	outboundProto := codec.NegotiateOutboundProtocol(inboundProto, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override)
	if codec.GetInbound(inboundProto) == nil || codec.GetOutbound(outboundProto) == nil {
		return true
	}
	return false
}

// shouldPassthrough 是 (*Handler).shouldPassthrough 的包级版——逻辑 1:1。
// passthrough 必须 channel 显式 PassthroughEnabled，且 inbound == outbound 协议。
func shouldPassthrough(ch *models.Channel, inboundProto codec.Protocol, modelName string) bool {
	if ch == nil || !ch.PassthroughEnabled {
		return false
	}
	rules := upstream.ChannelOverrideRulesFor(ch)
	override := upstream.ResolveOverride(rules, modelName)
	outboundProto := codec.NegotiateOutboundProtocol(inboundProto, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override)
	return inboundProto == outboundProto
}
