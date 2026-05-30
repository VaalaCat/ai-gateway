package codec

import "fmt"

// legacyEndpointAliases maps non-canonical endpoint_type strings historically
// accepted by the channel-test paths (master public test + agent RPC) to
// canonical endpoint keys. Centralising them here — the single resolver — lets
// every test path keep accepting them without re-introducing per-call switch
// statements that drift (the original cause of `invalid endpoint_type: anthropic`).
var legacyEndpointAliases = map[string]string{
	"anthropic":       EndpointKeyMessages,
	"chat-completion": EndpointKeyChatCompletions,
	"chat_completion": EndpointKeyChatCompletions,
}

// protocolToEndpointKey is the inverse of endpointKeyToProtocol.
func protocolToEndpointKey(p Protocol) string {
	switch p {
	case ProtocolOpenAIResponses:
		return EndpointKeyResponses
	case ProtocolClaude:
		return EndpointKeyMessages
	default:
		return EndpointKeyChatCompletions
	}
}

// NormalizeEndpointType maps a request endpoint_type (canonical key or legacy
// alias) to a canonical endpoint key + Protocol. ok=false for empty input
// (caller derives a default from the channel's endpoints).
func NormalizeEndpointType(raw string) (key string, proto Protocol, ok bool) {
	if raw == "" {
		return "", ProtocolUnknown, false
	}
	if canon, isAlias := legacyEndpointAliases[raw]; isAlias {
		raw = canon
	}
	if p, found := endpointKeyToProtocol[raw]; found {
		return raw, p, true
	}
	return "", ProtocolUnknown, false
}

// primaryProtocol picks the channel's primary protocol from its declared
// endpoints / supportedAPITypes by fallbackPriority. Defaults to
// ProtocolOpenAIChat when nothing is declared. Never uses numeric channel type.
func primaryProtocol(endpoints, supportedAPITypes string) Protocol {
	if eps := ParseEndpoints(endpoints); len(eps) > 0 {
		for _, p := range fallbackPriority {
			if _, ok := eps[p]; ok {
				return p
			}
		}
	}
	if supported := parseSupportedTypes(supportedAPITypes); len(supported) > 0 {
		for _, p := range fallbackPriority {
			if supported[p] {
				return p
			}
		}
	}
	return ProtocolOpenAIChat
}

// ResolveTestEndpoint resolves the upstream path used to connectivity-test a
// channel, driven by the codec Protocol model (endpoints / supportedAPITypes) —
// never the deprecated numeric channel type.
func ResolveTestEndpoint(endpoints, supportedAPITypes, raw string) (key string, proto Protocol, path string, err error) {
	if raw == "" {
		proto = primaryProtocol(endpoints, supportedAPITypes)
		return protocolToEndpointKey(proto), proto, ResolveEndpointPath(endpoints, proto), nil
	}
	k, p, ok := NormalizeEndpointType(raw)
	if !ok {
		return "", ProtocolUnknown, "", fmt.Errorf("invalid endpoint_type: %s", raw)
	}
	return k, p, ResolveEndpointPath(endpoints, p), nil
}

// BuildConnectivityTestRequest returns the relay path and minimal request body
// for a connectivity test, shaped by the resolved Protocol. Shared by the
// master public-channel test and agent RPC test so the endpoint vocabulary and
// body shape live in one place.
func BuildConnectivityTestRequest(endpoints, supportedAPITypes, endpointType, model string, stream bool) (path string, body map[string]any, err error) {
	_, proto, path, err := ResolveTestEndpoint(endpoints, supportedAPITypes, endpointType)
	if err != nil {
		return "", nil, err
	}
	switch proto {
	case ProtocolOpenAIResponses:
		body = map[string]any{"model": model, "input": "Say 'ok' and nothing else."}
	default: // ProtocolOpenAIChat / ProtocolClaude — both use messages-shaped body
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
