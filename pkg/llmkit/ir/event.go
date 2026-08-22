package ir

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
	Namespace string `json:"namespace,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolCallDelta carries incremental data for a streaming tool call.
type ToolCallDelta struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
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
