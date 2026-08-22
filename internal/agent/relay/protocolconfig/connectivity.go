package protocolconfig

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

var legacyEndpointAliases = map[string]string{
	"anthropic":       EndpointKeyMessages,
	"chat-completion": EndpointKeyChatCompletions,
	"chat_completion": EndpointKeyChatCompletions,
}

func NormalizeEndpointType(raw string) (key string, protocol llmkit.Protocol, ok bool) {
	if raw == "" {
		return "", llmkit.ProtocolUnknown, false
	}
	if canonical, alias := legacyEndpointAliases[raw]; alias {
		raw = canonical
	}
	protocol, found := endpointKeyToProtocol[raw]
	if !found {
		return "", llmkit.ProtocolUnknown, false
	}
	return raw, protocol, true
}

func ResolveTestEndpoint(endpoints, supportedAPITypes, raw string) (key string, protocol llmkit.Protocol, path string, err error) {
	if raw == "" {
		protocol = PrimaryOutboundProtocol(endpoints, supportedAPITypes)
		return protocolToEndpointKey(protocol), protocol, ResolveEndpointPath(endpoints, protocol), nil
	}
	key, protocol, ok := NormalizeEndpointType(raw)
	if !ok {
		return "", llmkit.ProtocolUnknown, "", fmt.Errorf("invalid endpoint_type: %s", raw)
	}
	return key, protocol, ResolveEndpointPath(endpoints, protocol), nil
}

func BuildConnectivityTestRequest(endpoints, supportedAPITypes, endpointType, model string, stream bool) (path string, body map[string]any, err error) {
	_, protocol, path, err := ResolveTestEndpoint(endpoints, supportedAPITypes, endpointType)
	if err != nil {
		return "", nil, err
	}
	switch protocol {
	case llmkit.ProtocolOpenAIResponses:
		body = map[string]any{"model": model, "input": "Say 'ok' and nothing else."}
	default:
		body = map[string]any{
			"model": model, "max_tokens": 10,
			"messages": []map[string]string{{"role": "user", "content": "Say 'ok' and nothing else."}},
		}
	}
	if stream {
		body["stream"] = true
	}
	return path, body, nil
}

func protocolToEndpointKey(protocol llmkit.Protocol) string {
	switch protocol {
	case llmkit.ProtocolOpenAIResponses:
		return EndpointKeyResponses
	case llmkit.ProtocolClaudeMessages:
		return EndpointKeyMessages
	default:
		return EndpointKeyChatCompletions
	}
}
