package ir

// Request is the protocol-agnostic representation of an AI completion request.
type Request struct {
	Model       string         `json:"model"`
	Messages    []Message      `json:"messages"`
	Tools       []Tool         `json:"tools,omitempty"`
	Stream      bool           `json:"stream"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	StopWords   []string       `json:"stop,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`

	// Model behavior fields (commonly used across protocols)
	ToolChoice        *ToolChoice `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort   string      `json:"reasoning_effort,omitempty"`
	ThinkingEnabled   bool        `json:"thinking_enabled,omitempty"`
	ThinkingBudget    int         `json:"thinking_budget,omitempty"`
	Store             *bool       `json:"store,omitempty"`

	// Sampling / generation parameters
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	Seed             *int64         `json:"seed,omitempty"`
	N                int            `json:"n,omitempty"`
	TopK             *int           `json:"top_k,omitempty"`
	LogitBias        map[string]int `json:"logit_bias,omitempty"`
	Logprobs         *bool          `json:"logprobs,omitempty"`
	TopLogprobs      *int           `json:"top_logprobs,omitempty"`
	User             string         `json:"user,omitempty"`
	ServiceTier      string         `json:"service_tier,omitempty"`
	InferenceGeo     string         `json:"inference_geo,omitempty"`
	SafetyIdentifier string         `json:"safety_identifier,omitempty"`
	ResponseFormat   any            `json:"response_format,omitempty"`
	StreamOptions    map[string]any `json:"stream_options,omitempty"`

	// Protocol-specific passthrough container
	Extras map[string]any `json:"extras,omitempty"`

	// InboundProtocol 由 InboundCodec.DecodeRequest 在成功解析后填写，标识
	// 该请求的原始入站协议。编码器据此判断"源协议 == 目标协议"做 passthrough。
	// 零值 "" 表示未知，ResolveTool 会退化到跨协议分支。
	InboundProtocol Protocol `json:"inbound_protocol,omitempty"`
}

// ToolChoice defines the canonical IR form for tool selection.
type ToolChoice struct {
	Type      string `json:"type"`                // "auto", "required", "none", "function", "custom"
	Name      string `json:"name,omitempty"`      // tool name, when Type="function" or "custom"
	Namespace string `json:"namespace,omitempty"` // Responses namespace, when Type="function"
}
