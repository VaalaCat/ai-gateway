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
	if inbound == llmkit.ProtocolGemini {
		return attemptproxy.ModePassthrough
	}
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
	mappedModel := state.ApplyModelMapping(ch, modelName)
	compatibility := upstream.NewToolCompatibilityRules(ch).Match(modelName, mappedModel)
	// 历史里有打包的工具调用 ID，或出口是 Chat 且请求带工具时，必须走协议转换。
	needsNative := toolCalls.HasPackedToolCallID ||
		(outbound == llmkit.ProtocolOpenAIChat && (toolCalls.HasTools || toolCalls.HasToolHistory)) ||
		(outbound == llmkit.ProtocolClaudeMessages && toolCalls.HasToolHistory &&
			upstream.NewThinkingRules(ch).SendBack(modelName, mappedModel))
	if needsNative || compatibility.Profile != llmkit.ToolSchemaProfileNone {
		return false
	}
	if outbound == llmkit.ProtocolOpenAIResponses &&
		llmkit.NormalizeBuiltinToolFallback(channelConfig.BuiltinToolFallback) == llmkit.BuiltinToolFallbackFunction {
		return false
	}
	return inbound == outbound
}

// SupportsGeminiPassthrough 渠道是否能执行当前模型的原生 Gemini 请求？
func SupportsGeminiPassthrough(ch *models.Channel, model string) bool {
	// 1. 排除 legacy，再按当前模型的协议覆盖选择实际出站。
	if ch == nil || ch.UseLegacyAdaptor {
		return false
	}
	override := upstream.ResolveOverride(upstream.ChannelOverrideRulesFor(ch), model)
	return protocolconfig.NegotiateOutboundProtocol(llmkit.ProtocolGemini, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override) == llmkit.ProtocolGemini
}
