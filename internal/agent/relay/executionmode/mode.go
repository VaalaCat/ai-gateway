package executionmode

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

// ForChannel returns the provider execution mode for one selected channel.
func ForChannel(ch *models.Channel, realModel string, inbound codec.Protocol) attemptproxy.ExecutionMode {
	if shouldUseLegacy(ch, inbound, realModel) {
		return attemptproxy.ModeLegacy
	}
	if shouldPassthrough(ch, inbound, realModel) {
		return attemptproxy.ModePassthrough
	}
	return attemptproxy.ModeNative
}

func shouldUseLegacy(ch *models.Channel, inbound codec.Protocol, modelName string) bool {
	if ch == nil {
		return false
	}
	if ch.UseLegacyAdaptor {
		return true
	}
	if inbound == codec.ProtocolUnknown {
		return true
	}
	rules := upstream.ChannelOverrideRulesFor(ch)
	override := upstream.ResolveOverride(rules, modelName)
	outbound := codec.NegotiateOutboundProtocol(inbound, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override)
	return codec.GetInbound(inbound) == nil || codec.GetOutbound(outbound) == nil
}

func shouldPassthrough(ch *models.Channel, inbound codec.Protocol, modelName string) bool {
	if ch == nil || !ch.PassthroughEnabled {
		return false
	}
	rules := upstream.ChannelOverrideRulesFor(ch)
	override := upstream.ResolveOverride(rules, modelName)
	outbound := codec.NegotiateOutboundProtocol(inbound, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override)
	channelConfig := upstream.BuildChannelConfig(ch, modelName, outbound)
	if outbound == codec.ProtocolOpenAIResponses &&
		codec.NormalizeBuiltinToolFallback(channelConfig.BuiltinToolFallback) == codec.BuiltinToolFallbackFunction {
		return false
	}
	return inbound == outbound
}
