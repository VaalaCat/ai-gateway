package protocolconfig

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"go.uber.org/zap"
)

const (
	EndpointKeyChatCompletions = "chat_completions"
	EndpointKeyResponses       = "responses"
	EndpointKeyMessages        = "messages"
)

var defaultEndpointPaths = map[llmkit.Protocol]string{
	llmkit.ProtocolOpenAIChat:      "/v1/chat/completions",
	llmkit.ProtocolOpenAIResponses: "/v1/responses",
	llmkit.ProtocolClaudeMessages:  "/v1/messages",
}

var endpointKeyToProtocol = map[string]llmkit.Protocol{
	EndpointKeyChatCompletions: llmkit.ProtocolOpenAIChat,
	EndpointKeyResponses:       llmkit.ProtocolOpenAIResponses,
	EndpointKeyMessages:        llmkit.ProtocolClaudeMessages,
}

var fallbackPriority = []llmkit.Protocol{
	llmkit.ProtocolOpenAIResponses,
	llmkit.ProtocolOpenAIChat,
	llmkit.ProtocolClaudeMessages,
}

var apiTypeToProtocol = map[string]llmkit.Protocol{
	consts.APITypeChatCompletion: llmkit.ProtocolOpenAIChat,
	consts.APITypeResponses:      llmkit.ProtocolOpenAIResponses,
	consts.APITypeClaude:         llmkit.ProtocolClaudeMessages,
}

func DefaultEndpointPath(protocol llmkit.Protocol) string {
	return defaultEndpointPaths[protocol]
}

func PathToProtocol(path string) llmkit.Protocol {
	switch path {
	case consts.RouteChatCompletions:
		return llmkit.ProtocolOpenAIChat
	case consts.RouteResponses:
		return llmkit.ProtocolOpenAIResponses
	case consts.RouteMessages:
		return llmkit.ProtocolClaudeMessages
	default:
		return llmkit.ProtocolUnknown
	}
}

func ChannelTypeToProtocol(channelType int) llmkit.Protocol {
	switch channelType {
	case consts.ChannelTypeAnthropic:
		return llmkit.ProtocolClaudeMessages
	case consts.ChannelTypeGemini:
		return llmkit.ProtocolGemini
	default:
		return llmkit.ProtocolOpenAIChat
	}
}

func IsSupportedProtocol(protocol llmkit.Protocol) bool {
	switch protocol {
	case llmkit.ProtocolOpenAIChat, llmkit.ProtocolOpenAIResponses, llmkit.ProtocolClaudeMessages:
		return true
	default:
		return false
	}
}

func ParseEndpoints(raw string) map[llmkit.Protocol]string {
	if raw == "" {
		return nil
	}
	var endpoints map[string]string
	if err := json.Unmarshal([]byte(raw), &endpoints); err != nil {
		return nil
	}
	result := make(map[llmkit.Protocol]string, len(endpoints))
	for key, path := range endpoints {
		if protocol, ok := endpointKeyToProtocol[key]; ok {
			result[protocol] = path
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func ResolveEndpointPath(endpoints string, protocol llmkit.Protocol) string {
	if path, ok := ParseEndpoints(endpoints)[protocol]; ok {
		return path
	}
	return DefaultEndpointPath(protocol)
}

func JoinUpstreamURL(baseURL, path string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || base.Scheme == "" {
		return "", fmt.Errorf("invalid upstream base url %q", baseURL)
	}
	joined := strings.TrimRight(baseURL, "/") + path
	upstream, err := url.Parse(joined)
	if err != nil {
		return "", fmt.Errorf("invalid upstream url: %w", err)
	}
	if !strings.EqualFold(upstream.Host, base.Host) || !strings.EqualFold(upstream.Scheme, base.Scheme) {
		return "", fmt.Errorf(
			"upstream host mismatch: endpoint path would redirect %s://%s to %s://%s",
			base.Scheme, base.Host, upstream.Scheme, upstream.Host,
		)
	}
	return joined, nil
}

func NegotiateOutboundProtocol(
	inbound llmkit.Protocol,
	channelType int,
	supportedAPITypes string,
	endpoints string,
	override map[llmkit.Protocol]llmkit.Protocol,
) llmkit.Protocol {
	if target, ok := override[inbound]; ok && IsSupportedProtocol(target) {
		if isReachable(target, endpoints, supportedAPITypes) {
			return target
		}
		zap.L().Warn("protocol_override: target not in channel endpoints/supportedAPITypes, falling back to default",
			zap.String("inbound", string(inbound)),
			zap.String("target", string(target)),
		)
	}

	if configured := ParseEndpoints(endpoints); len(configured) > 0 {
		if _, ok := configured[inbound]; ok {
			return inbound
		}
		for _, protocol := range fallbackPriority {
			if _, ok := configured[protocol]; ok {
				return protocol
			}
		}
		return ChannelTypeToProtocol(channelType)
	}

	supported := parseSupportedTypes(supportedAPITypes)
	if len(supported) == 0 {
		return ChannelTypeToProtocol(channelType)
	}
	if supported[inbound] {
		return inbound
	}
	for _, protocol := range fallbackPriority {
		if supported[protocol] {
			return protocol
		}
	}
	return ChannelTypeToProtocol(channelType)
}

func PrimaryOutboundProtocol(endpoints, supportedAPITypes string) llmkit.Protocol {
	if configured := ParseEndpoints(endpoints); len(configured) > 0 {
		for _, protocol := range fallbackPriority {
			if _, ok := configured[protocol]; ok {
				return protocol
			}
		}
	}
	if supported := parseSupportedTypes(supportedAPITypes); len(supported) > 0 {
		for _, protocol := range fallbackPriority {
			if supported[protocol] {
				return protocol
			}
		}
	}
	return llmkit.ProtocolOpenAIChat
}

func parseSupportedTypes(raw string) map[llmkit.Protocol]bool {
	if raw == "" {
		return nil
	}
	var apiTypes []string
	if err := json.Unmarshal([]byte(raw), &apiTypes); err != nil {
		return nil
	}
	result := make(map[llmkit.Protocol]bool, len(apiTypes))
	for _, apiType := range apiTypes {
		if protocol, ok := apiTypeToProtocol[apiType]; ok {
			result[protocol] = true
		}
	}
	return result
}

func isReachable(target llmkit.Protocol, endpoints, supportedAPITypes string) bool {
	if configured := ParseEndpoints(endpoints); len(configured) > 0 {
		_, ok := configured[target]
		return ok
	}
	if supported := parseSupportedTypes(supportedAPITypes); len(supported) > 0 {
		return supported[target]
	}
	return true
}
