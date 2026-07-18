package openai

import "encoding/json"

// ---------------------------------------------------------------------------
// Wire types for JSON (de)serialization
// ---------------------------------------------------------------------------

// oaiRequest is the OpenAI Chat Completions request JSON structure.
type oaiRequest struct {
	Model             string         `json:"model"`
	Messages          []oaiMessage   `json:"messages"`
	Tools             []any          `json:"tools,omitempty"`
	Stream            bool           `json:"stream"`
	MaxTokens         int            `json:"max_tokens,omitempty"`
	Temperature       *float64       `json:"temperature,omitempty"`
	TopP              *float64       `json:"top_p,omitempty"`
	Stop              any            `json:"stop,omitempty"`
	ToolChoice        any            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	Store             *bool          `json:"store,omitempty"`
	FrequencyPenalty  *float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty   *float64       `json:"presence_penalty,omitempty"`
	Seed              *int64         `json:"seed,omitempty"`
	N                 int            `json:"n,omitempty"`
	User              string         `json:"user,omitempty"`
	LogitBias         map[string]int `json:"logit_bias,omitempty"`
	Logprobs          *bool          `json:"logprobs,omitempty"`
	TopLogprobs       *int           `json:"top_logprobs,omitempty"`
	ServiceTier       string         `json:"service_tier,omitempty"`
	SafetyIdentifier  string         `json:"safety_identifier,omitempty"`
	ResponseFormat    any            `json:"response_format,omitempty"`
	StreamOptions     map[string]any `json:"stream_options,omitempty"`
	ReasoningEffort   string         `json:"reasoning_effort,omitempty"`
}

// oaiMessage represents an OpenAI message. Content can be a string or array.
type oaiMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	ToolCalls        []oaiToolCall   `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"` // 仅请求路径
}

type oaiContentBlock struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiImageURL struct {
	URL string `json:"url"`
}

// oaiToolCall represents a tool call in both request and response messages.
// The Index field is used in streaming responses to identify which tool call
// a delta belongs to. It uses `json:"index"` (not omitempty) so it always
// appears in stream chunk JSON per the OpenAI spec.
type oaiToolCall struct {
	Index    int             `json:"index"`
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type,omitempty"`
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string         `json:"type"`
	Function oaiToolDefFunc `json:"function"`
}

type oaiToolDefFunc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

// Response wire types

type oaiResponse struct {
	ID          string      `json:"id"`
	Object      string      `json:"object"`
	Created     int64       `json:"created,omitempty"`
	Model       string      `json:"model,omitempty"`
	ServiceTier string      `json:"service_tier,omitempty"`
	Choices     []oaiChoice `json:"choices"`
	Usage       *oaiUsage   `json:"usage,omitempty"`
	// Timings 是 llama.cpp 系上游的非标准 token 计数(无 usage 时用)。
	Timings *oaiTimings `json:"timings,omitempty"`
}

// oaiTimings 是 llama.cpp OpenAI 兼容上游放 token 数的非标准字段。
// prompt_n=本轮实际评估的 prompt token(非缓存后缀),cache_n=命中 KV cache 复用的
// token(缓存前缀,未重算),二者互斥;predicted_n=生成的 completion token。
type oaiTimings struct {
	PromptN    int `json:"prompt_n"`
	PredictedN int `json:"predicted_n"`
	CacheN     int `json:"cache_n,omitempty"`
}

type oaiChoice struct {
	Index        int           `json:"index"`
	Message      *oaiRespMsg   `json:"message,omitempty"`
	Delta        *oaiRespDelta `json:"delta,omitempty"`
	FinishReason *string       `json:"finish_reason"`
}

type oaiRespMsg struct {
	Role             string        `json:"role"`
	Content          string        `json:"content,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	Refusal          string        `json:"refusal,omitempty"`
	ToolCalls        []oaiToolCall `json:"tool_calls,omitempty"`
}

type oaiRespDelta struct {
	Role             string        `json:"role,omitempty"`
	Content          string        `json:"content,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	Refusal          string        `json:"refusal,omitempty"`
	ToolCalls        []oaiToolCall `json:"tool_calls,omitempty"`
}

type oaiUsage struct {
	PromptTokens            int                    `json:"prompt_tokens"`
	CompletionTokens        int                    `json:"completion_tokens"`
	TotalTokens             int                    `json:"total_tokens"`
	CompletionTokensDetails *oaiTokenDetails       `json:"completion_tokens_details,omitempty"`
	PromptTokensDetails     *oaiPromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

type oaiTokenDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

type oaiPromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type oaiErrorResponse struct {
	Error oaiErrorBody `json:"error"`
}

type oaiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}
