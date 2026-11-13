package protocolconfig

import (
	"fmt"
	"net/url"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/endpoint"
)

var legacyEndpointAliases = map[string]string{
	"anthropic":       EndpointKeyMessages,
	"chat-completion": EndpointKeyChatCompletions,
	"chat_completion": EndpointKeyChatCompletions,
	"gemini":          EndpointKeyGenerateContent,
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
	body, err = buildConnectivityTestBody(protocol, model, stream)
	if err == nil && protocol == llmkit.ProtocolGemini {
		action := endpoint.GenerateContent
		if stream {
			action = endpoint.StreamGenerateContent
		}
		path, err = endpoint.GeminiPath(path, model, action)
		if err == nil && stream {
			u, parseErr := url.Parse(path)
			if parseErr != nil {
				return "", nil, parseErr
			}
			query := u.Query()
			query.Set("alt", "sse")
			u.RawQuery = query.Encode()
			path = u.String()
		}
	}
	return path, body, err
}

func BuildRelayConnectivityTestRequest(protocol llmkit.Protocol, model string, stream bool) (path string, body map[string]any, err error) {
	if !IsSupportedProtocol(protocol) {
		return "", nil, fmt.Errorf("unsupported connectivity test protocol: %s", protocol)
	}
	return BuildConnectivityTestRequest("", "", protocolToEndpointKey(protocol), model, stream)
}

func buildConnectivityTestBody(protocol llmkit.Protocol, model string, stream bool) (map[string]any, error) {
	if !IsSupportedProtocol(protocol) {
		return nil, fmt.Errorf("unsupported connectivity test protocol: %s", protocol)
	}
	var body map[string]any
	switch protocol {
	case llmkit.ProtocolGemini:
		return map[string]any{
			"contents": []any{map[string]any{
				"role": "user", "parts": []any{map[string]any{"text": "Say 'ok' and nothing else."}},
			}},
		}, nil
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
	return body, nil
}

func protocolToEndpointKey(protocol llmkit.Protocol) string {
	switch protocol {
	case llmkit.ProtocolOpenAIResponses:
		return EndpointKeyResponses
	case llmkit.ProtocolClaudeMessages:
		return EndpointKeyMessages
	case llmkit.ProtocolGemini:
		return EndpointKeyGenerateContent
	default:
		return EndpointKeyChatCompletions
	}
}
