package executionmode

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/protocolconfig"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// ForChannel returns the provider execution mode for one selected channel.
func ForChannel(ch *models.Channel, realModel string, inbound llmkit.Protocol, toolCalls llmkit.ToolCallRequestInfo) attemptproxy.ExecutionMode {
	if shouldUseLegacy(ch, inbound, realModel) {
		return attemptproxy.ModeLegacy
	}
	if shouldPassthrough(ch, inbound, realModel, toolCalls) {
		return attemptproxy.ModePassthrough
	}
	return attemptproxy.ModeNative
}

func shouldUseLegacy(ch *models.Channel, inbound llmkit.Protocol, modelName string) bool {
	if ch == nil {
		return false
	}
	if ch.UseLegacyAdaptor {
		return true
	}
	if inbound == llmkit.ProtocolUnknown {
		return true
	}
	rules := upstream.ChannelOverrideRulesFor(ch)
	override := upstream.ResolveOverride(rules, modelName)
	outbound := protocolconfig.NegotiateOutboundProtocol(inbound, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override)
	return !protocolconfig.IsSupportedProtocol(inbound) || !protocolconfig.IsSupportedProtocol(outbound)
}

func shouldPassthrough(ch *models.Channel, inbound llmkit.Protocol, modelName string, toolCalls llmkit.ToolCallRequestInfo) bool {
	if ch == nil || !ch.PassthroughEnabled {
		return false
	}
	rules := upstream.ChannelOverrideRulesFor(ch)
	override := upstream.ResolveOverride(rules, modelName)
	outbound := protocolconfig.NegotiateOutboundProtocol(inbound, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override)
	channelConfig := upstream.BuildChannelConfig(ch, modelName, outbound)
	compatibility := upstream.NewToolCompatibilityRules(ch).Match(state.ApplyModelMapping(ch, modelName))
	// 历史里有打包的工具调用 ID，或出口是 Chat 且请求带工具时，必须走协议转换。
	needsNative := toolCalls.HasPackedToolCallID ||
		(outbound == llmkit.ProtocolOpenAIChat && (toolCalls.HasTools || toolCalls.HasToolHistory))
	if needsNative || compatibility.Profile != llmkit.ToolSchemaProfileNone {
		return false
	}
	if outbound == llmkit.ProtocolOpenAIResponses &&
		llmkit.NormalizeBuiltinToolFallback(channelConfig.BuiltinToolFallback) == llmkit.BuiltinToolFallbackFunction {
		return false
	}
	return inbound == outbound
}
