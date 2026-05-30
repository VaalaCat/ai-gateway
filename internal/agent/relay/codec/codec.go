package codec

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// ChannelConfig
// ---------------------------------------------------------------------------

// ChannelConfig holds provider-specific configuration used when encoding an
// outbound request to an upstream AI provider.
type ChannelConfig struct {
	BaseURL             string         `json:"base_url"`
	APIKey              string         `json:"api_key"`
	Model               string         `json:"model"`         // upstream model after model_mapping
	InboundModel        string         `json:"inbound_model"` // model as the client sent it (before mapping)
	Organization        string         `json:"organization,omitempty"`
	APIVersion          string         `json:"api_version,omitempty"`
	ParamOverride       map[string]any `json:"param_override,omitempty"`
	HeaderOverride      map[string]any `json:"header_override,omitempty"`
	SystemPrompt        string         `json:"system_prompt,omitempty"`
	EndpointPath        string         `json:"endpoint_path,omitempty"`
	RoleMapping         string         `json:"role_mapping,omitempty"`
	SystemPromptInInput bool           `json:"system_prompt_in_input,omitempty"`
	// BuiltinToolFallback 控制"入站带内置工具但出站协议无法表达"时的处理策略。
	// 合法值："drop"（默认）、"error"、"passthrough"。空串归一到 "drop"。
	// 从 Channel.OtherSettings["builtin_tool_fallback"] 读入，由 ResolveTool 消费。
	BuiltinToolFallback string `json:"builtin_tool_fallback,omitempty"`
	// SendBackThinking 控制是否把 IR 中 ContentTypeThinking 块作为
	// reasoning_content 回填到上游请求的 history。仅 openai_chat 出站时生效。
	// 由 upstream/channelconfig.go 在 BuildChannelConfig 时根据 OtherSettings 中的
	// model_thinking_passthrough 规则匹配 cfg.Model 后填入。
	SendBackThinking bool `json:"send_back_thinking,omitempty"`
}

// defaultEndpointPaths maps each protocol to its standard endpoint path.
var defaultEndpointPaths = map[Protocol]string{
	ProtocolOpenAIChat:      "/v1/chat/completions",
	ProtocolOpenAIResponses: "/v1/responses",
	ProtocolClaude:          "/v1/messages",
}

// DefaultEndpointPath returns the standard endpoint path for a protocol.
func DefaultEndpointPath(p Protocol) string {
	return defaultEndpointPaths[p]
}

// ---------------------------------------------------------------------------
// Codec interfaces
// ---------------------------------------------------------------------------

// InboundCodec handles the client-facing side of a protocol: it decodes
// incoming requests and encodes outgoing responses back to the client.
type InboundCodec interface {
	// DecodeRequest parses an incoming HTTP request into the IR Request.
	DecodeRequest(r *http.Request) (*Request, error)

	// EncodeResponse writes an IR event stream back to the client.
	// When stream is true the response is sent as server-sent events;
	// otherwise a single JSON payload is returned.
	EncodeResponse(events <-chan Event, w http.ResponseWriter, stream bool) error

	// EncodeError writes an error response to the client.
	EncodeError(w http.ResponseWriter, statusCode int, err error)
}

// OutboundCodec handles the provider-facing side of a protocol: it encodes
// IR requests into upstream HTTP requests and decodes upstream responses back
// into the IR event stream.
type OutboundCodec interface {
	// EncodeRequest converts an IR Request into an HTTP request suitable for
	// the upstream provider described by cfg.
	EncodeRequest(req *Request, cfg *ChannelConfig) (*http.Request, error)

	// DecodeResponse reads the upstream HTTP response and returns a channel
	// of IR events. When stream is true the response body is expected to be
	// a server-sent event stream.
	DecodeResponse(resp *http.Response, stream bool) (<-chan Event, error)
}

// ---------------------------------------------------------------------------
// Protocol detection helpers
// ---------------------------------------------------------------------------

// PathToProtocol maps a well-known HTTP path to the corresponding Protocol.
func PathToProtocol(path string) Protocol {
	switch path {
	case consts.RouteChatCompletions:
		return ProtocolOpenAIChat
	case consts.RouteResponses:
		return ProtocolOpenAIResponses
	case consts.RouteMessages:
		return ProtocolClaude
	default:
		return ProtocolUnknown
	}
}

// ChannelTypeToProtocol converts a numeric channel type (as used by the
// existing channel/model configuration) to a Protocol.
func ChannelTypeToProtocol(channelType int) Protocol {
	switch channelType {
	case consts.ChannelTypeAnthropic:
		return ProtocolClaude
	case consts.ChannelTypeGemini:
		return ProtocolGemini
	default:
		return ProtocolOpenAIChat
	}
}

// ---------------------------------------------------------------------------
// Protocol negotiation
// ---------------------------------------------------------------------------

// Endpoint JSON keys used in Channel.Endpoints field.
const (
	EndpointKeyChatCompletions = "chat_completions"
	EndpointKeyResponses       = "responses"
	EndpointKeyMessages        = "messages"
)

var endpointKeyToProtocol = map[string]Protocol{
	EndpointKeyChatCompletions: ProtocolOpenAIChat,
	EndpointKeyResponses:       ProtocolOpenAIResponses,
	EndpointKeyMessages:        ProtocolClaude,
}

func ParseEndpoints(raw string) map[Protocol]string {
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	result := make(map[Protocol]string, len(m))
	for k, v := range m {
		if p, ok := endpointKeyToProtocol[k]; ok {
			result[p] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func ResolveEndpointPath(endpoints string, proto Protocol) string {
	eps := ParseEndpoints(endpoints)
	if path, ok := eps[proto]; ok {
		return path
	}
	return DefaultEndpointPath(proto)
}

// JoinUpstreamURL appends path to baseURL and verifies the path did not rewrite
// the scheme/host (e.g. via "@evil" userinfo or a non-rooted ".evil.com" suffix).
// This is the egress-side backstop for the BYOK endpoints SSRF defense: even a
// malicious endpoints row already in the DB cannot redirect the request — and
// the BYOK key — to a host other than the allowlisted base_url host.
func JoinUpstreamURL(baseURL, path string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || base.Scheme == "" {
		return "", fmt.Errorf("invalid upstream base url %q", baseURL)
	}
	joined := strings.TrimRight(baseURL, "/") + path
	u, err := url.Parse(joined)
	if err != nil {
		return "", fmt.Errorf("invalid upstream url: %w", err)
	}
	if !strings.EqualFold(u.Host, base.Host) || !strings.EqualFold(u.Scheme, base.Scheme) {
		return "", fmt.Errorf("upstream host mismatch: endpoint path would redirect %s://%s to %s://%s",
			base.Scheme, base.Host, u.Scheme, u.Host)
	}
	return joined, nil
}

// fallbackPriority defines the order in which protocols are tried when the
// inbound protocol is not directly supported by the channel.
var fallbackPriority = []Protocol{
	ProtocolOpenAIResponses,
	ProtocolOpenAIChat,
	ProtocolClaude,
}

// apiTypeToProtocol maps the string identifiers stored in the database /
// used by the frontend to Protocol constants.
var apiTypeToProtocol = map[string]Protocol{
	consts.APITypeChatCompletion: ProtocolOpenAIChat,
	consts.APITypeResponses:      ProtocolOpenAIResponses,
	consts.APITypeClaude:         ProtocolClaude,
}

// NegotiateOutboundProtocol selects the best outbound protocol for a request
// based on the inbound protocol and the channel's declared supported API types.
//
// Rules:
//  0. If override[inbound] is a valid Protocol and target is reachable
//     (in endpoints / supportedAPITypes, or both unset), use override target
//     (highest priority).
//  1. If endpoints is configured, use it.
//  2. Parse supportedAPITypes JSON array into a Protocol set.
//  3. If the set is empty, fall back to ChannelTypeToProtocol.
//  4. If the inbound protocol is in the set, use it.
//  5. Otherwise pick the first supported protocol by priority: responses > chat > claude.
//  6. If none match, fall back to ChannelTypeToProtocol.
func NegotiateOutboundProtocol(
	inbound Protocol,
	channelType int,
	supportedAPITypes string,
	endpoints string,
	override map[Protocol]Protocol,
) Protocol {
	// Priority 0: Explicit override
	if target, ok := override[inbound]; ok && isValidOverrideTarget(target) {
		if isReachable(target, endpoints, supportedAPITypes) {
			return target
		}
		// target unreachable, log warn and fall through to default negotiation
		zap.L().Warn("protocol_override: target not in channel endpoints/supportedAPITypes, falling back to default",
			zap.String("inbound", string(inbound)),
			zap.String("target", string(target)),
		)
	}

	// Priority 1: Endpoints (new)
	eps := ParseEndpoints(endpoints)
	if len(eps) > 0 {
		if _, ok := eps[inbound]; ok {
			return inbound
		}
		for _, p := range fallbackPriority {
			if _, ok := eps[p]; ok {
				return p
			}
		}
		return ChannelTypeToProtocol(channelType)
	}

	// Priority 2: SupportedAPITypes (legacy)
	supported := parseSupportedTypes(supportedAPITypes)
	if len(supported) == 0 {
		return ChannelTypeToProtocol(channelType)
	}
	if supported[inbound] {
		return inbound
	}
	for _, p := range fallbackPriority {
		if supported[p] {
			return p
		}
	}
	return ChannelTypeToProtocol(channelType)
}

// isValidOverrideTarget reports whether the value is one of the codec-supported
// Protocol constants. ProtocolUnknown / ProtocolGemini / empty are rejected.
func isValidOverrideTarget(p Protocol) bool {
	switch p {
	case ProtocolOpenAIChat, ProtocolOpenAIResponses, ProtocolClaude:
		return true
	}
	return false
}

// isReachable reports whether the target is in the channel's declared protocol
// set. If both endpoints and supportedAPITypes are unset, returns true (allow
// override to drive ChannelTypeToProtocol fallback).
func isReachable(target Protocol, endpoints, supportedAPITypes string) bool {
	if eps := ParseEndpoints(endpoints); len(eps) > 0 {
		_, ok := eps[target]
		return ok
	}
	if supported := parseSupportedTypes(supportedAPITypes); len(supported) > 0 {
		return supported[target]
	}
	return true
}

// parseSupportedTypes parses a JSON array of API type strings into a set of Protocols.
func parseSupportedTypes(raw string) map[Protocol]bool {
	if raw == "" {
		return nil
	}
	var types []string
	if err := json.Unmarshal([]byte(raw), &types); err != nil {
		return nil
	}
	m := make(map[Protocol]bool, len(types))
	for _, t := range types {
		if p, ok := apiTypeToProtocol[t]; ok {
			m[p] = true
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

var (
	inboundMu   sync.RWMutex
	outboundMu  sync.RWMutex
	inboundReg  = make(map[Protocol]InboundCodec)
	outboundReg = make(map[Protocol]OutboundCodec)
)

// RegisterInbound registers an InboundCodec for the given protocol.
func RegisterInbound(p Protocol, c InboundCodec) {
	inboundMu.Lock()
	defer inboundMu.Unlock()
	if c == nil {
		panic(fmt.Sprintf("codec: RegisterInbound called with nil codec for protocol %q", p))
	}
	inboundReg[p] = c
}

// RegisterOutbound registers an OutboundCodec for the given protocol.
func RegisterOutbound(p Protocol, c OutboundCodec) {
	outboundMu.Lock()
	defer outboundMu.Unlock()
	if c == nil {
		panic(fmt.Sprintf("codec: RegisterOutbound called with nil codec for protocol %q", p))
	}
	outboundReg[p] = c
}

// GetInbound returns the registered InboundCodec for the given protocol, or
// nil if none has been registered.
func GetInbound(p Protocol) InboundCodec {
	inboundMu.RLock()
	defer inboundMu.RUnlock()
	return inboundReg[p]
}

// GetOutbound returns the registered OutboundCodec for the given protocol, or
// nil if none has been registered.
func GetOutbound(p Protocol) OutboundCodec {
	outboundMu.RLock()
	defer outboundMu.RUnlock()
	return outboundReg[p]
}
