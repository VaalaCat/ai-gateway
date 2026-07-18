// Package codec defines the Intermediate Representation (IR) types and Codec
// interfaces used by the native protocol conversion layer. The IR provides a
// unified data model that sits between inbound (client-facing) and outbound
// (provider-facing) protocols, enabling M x N protocol conversion via a shared
// intermediate form.
package codec

import "encoding/json"

// ---------------------------------------------------------------------------
// Protocol
// ---------------------------------------------------------------------------

// Protocol identifies a wire protocol supported by the gateway.
type Protocol string

const (
	ProtocolOpenAIChat      Protocol = "openai_chat"
	ProtocolOpenAIResponses Protocol = "openai_responses"
	ProtocolClaude          Protocol = "claude"
	ProtocolGemini          Protocol = "gemini"
	ProtocolUnknown         Protocol = "unknown"
)

// ---------------------------------------------------------------------------
// Role
// ---------------------------------------------------------------------------

// Role represents the role of a message participant.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleDeveloper Role = "developer"
)

// ---------------------------------------------------------------------------
// ContentType
// ---------------------------------------------------------------------------

// ContentType describes the kind of content carried by a ContentBlock.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeImage      ContentType = "image"
	ContentTypeAudio      ContentType = "audio"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeThinking   ContentType = "thinking"
	ContentTypeInputText  ContentType = "input_text"
	ContentTypeOutputText ContentType = "output_text"
	ContentTypeImageURL   ContentType = "image_url"
	ContentTypeFunction   ContentType = "function"
)

// Image source type 常量。
const (
	ImageSourceBase64 = "base64"
	ImageSourceURL    = "url"
)

// ---------------------------------------------------------------------------
// EventType
// ---------------------------------------------------------------------------

// EventType identifies the kind of streaming event in an IR event stream.
type EventType int

const (
	EventStreamStart            EventType = iota // stream has started
	EventContentDelta                            // incremental text content
	EventToolCallDelta                           // Deprecated: split into EventToolCallStart / EventToolCallArgumentsDelta / EventToolCallEnd. Will be removed once all stream codecs migrate.
	EventThinkingDelta                           // incremental thinking/reasoning content
	EventUsage                                   // token usage report
	EventDone                                    // stream finished normally
	EventError                                   // stream finished with an error
	EventRawPassthrough                          // raw SSE event that could not be parsed into a known IR event
	EventContentBlockStop                        // content block ended
	EventSignatureDelta                          // thinking block signature
	EventToolCallStart                           // streaming: a tool call began (call_id + name)
	EventToolCallArgumentsDelta                  // streaming: incremental arguments fragment
	EventToolCallEnd                             // streaming: tool call ended with full accumulated arguments
)

// ---------------------------------------------------------------------------
// Request
// ---------------------------------------------------------------------------

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
	Type string `json:"type"`           // "auto", "required", "none", "function", "custom"
	Name string `json:"name,omitempty"` // tool name, when Type="function" or "custom"
}

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

// Message represents a single conversation turn.
type Message struct {
	Role       Role           `json:"role"`
	Content    []ContentBlock `json:"content"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`

	// RawJSON preserves the original JSON for unknown input item types.
	// When set with empty Role, the encoder should emit this JSON as-is.
	RawJSON json.RawMessage `json:"raw_json,omitempty"`
}

// TextMessage is a convenience constructor for a simple text message.
func TextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: text},
		},
	}
}

// ---------------------------------------------------------------------------
// ContentBlock
// ---------------------------------------------------------------------------

// ContentBlock is a single piece of typed content within a message.
type ContentBlock struct {
	Type     ContentType    `json:"type"`
	Text     string         `json:"text,omitempty"`
	MediaURL string         `json:"media_url,omitempty"`
	MediaB64 string         `json:"media_b64,omitempty"`
	MimeType string         `json:"mime_type,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`

	// RawJSON preserves the original JSON for unknown content block types.
	// When set, the encoder should emit this JSON as-is instead of
	// constructing from typed fields.
	RawJSON json.RawMessage `json:"raw_json,omitempty"`
}

// ---------------------------------------------------------------------------
// Tool / ToolCall
// ---------------------------------------------------------------------------

// Tool describes a tool (function) that the model may invoke.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
	Type        string `json:"type,omitempty"` // "function", "web_search", "custom", etc.
	Strict      *bool  `json:"strict,omitempty"`
	RawConfig   any    `json:"raw_config,omitempty"` // full config for non-function tools
}

// ToolCall represents a model-initiated tool invocation.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ---------------------------------------------------------------------------
// Event (streaming)
// ---------------------------------------------------------------------------

// RawSSEEvent carries a raw SSE event that could not be parsed into a known IR event.
type RawSSEEvent struct {
	EventName string `json:"event_name"`
	Data      string `json:"data"`
}

// Event is a single element in the IR event stream produced when streaming
// responses from an upstream provider.
type Event struct {
	Type              EventType          `json:"type"`
	Delta             *DeltaPayload      `json:"delta,omitempty"`
	ToolCall          *StreamingToolCall `json:"tool_call,omitempty"`
	Usage             *Usage             `json:"usage,omitempty"`
	Error             *ErrorPayload      `json:"error,omitempty"`
	FinishReason      string             `json:"finish_reason,omitempty"`
	Model             string             `json:"model,omitempty"`   // upstream model name passthrough
	Created           int64              `json:"created,omitempty"` // upstream timestamp passthrough
	Metadata          map[string]any     `json:"metadata,omitempty"`
	RawPassthrough    *RawSSEEvent       `json:"raw_passthrough,omitempty"`
	Extras            map[string]any     `json:"extras,omitempty"` // non-streaming response unknown field passthrough
	ContentBlockIndex *int               `json:"content_block_index,omitempty"`
	StopSequence      string             `json:"stop_sequence,omitempty"`
}

// DeltaPayload carries incremental content for delta events.
type DeltaPayload struct {
	ContentType ContentType    `json:"content_type,omitempty"`
	Text        string         `json:"text,omitempty"`
	Refusal     string         `json:"refusal,omitempty"`
	Signature   string         `json:"signature,omitempty"`
	ToolCall    *ToolCallDelta `json:"tool_call,omitempty"`
}

// StreamingToolCall carries per-event state for the 3 streaming tool_call events.
// CallID is required on all 3 events to bind identity. Index mirrors the chat
// protocol's tool_calls[].index for parallel calls. Name is set on Start.
// Arguments holds a fragment on ArgumentsDelta and the full accumulated value on End.
type StreamingToolCall struct {
	CallID    string `json:"call_id"`
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolCallDelta carries incremental data for a streaming tool call.
type ToolCallDelta struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Usage reports token consumption for a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens"`

	// Detailed token breakdowns (OpenAI completion_tokens_details / prompt_tokens_details)
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	CachedTokens             int `json:"cached_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// ErrorPayload describes an error encountered during streaming.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
