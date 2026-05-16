package consts

// HTTP header names.
const (
	HeaderAuthorization    = "Authorization"
	HeaderContentType      = "Content-Type"
	HeaderConnection       = "Connection"
	HeaderCacheControl     = "Cache-Control"
	HeaderHost             = "Host"
	HeaderOpenAIOrg        = "OpenAI-Organization"
	HeaderXAPIKey          = "x-api-key"
	HeaderAnthropicVersion = "anthropic-version"

	// X-Vaala-* custom headers
	HeaderXRequestID       = "X-Vaala-Request-ID"
	HeaderXChannelID       = "X-Vaala-Channel-ID"
	HeaderXAgentID         = "X-Vaala-Agent-ID"
	HeaderXAgentSecret     = "X-Vaala-Agent-Secret"
	HeaderXAgentTag        = "X-Vaala-Agent-Tag"
	HeaderXAgentAddressTag = "X-Vaala-Agent-Address-Tag"
	HeaderXAgentHop        = "X-Vaala-Agent-Hop"
)

// HTTP header values / prefixes.
const (
	ContentTypeJSON       = "application/json"
	ContentTypeSSE        = "text/event-stream"
	BearerPrefix          = "Bearer "
	CacheControlNoCache   = "no-cache"
	ConnectionKeepAlive   = "keep-alive"
	AnthropicVersionValue = "2023-06-01"
)
