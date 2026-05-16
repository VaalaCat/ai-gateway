package openai

import "encoding/json"

// ---------------------------------------------------------------------------
// Wire types for JSON (de)serialization — OpenAI Responses API
// ---------------------------------------------------------------------------

// respRequest is the OpenAI Responses API request JSON structure.
type respRequest struct {
	Model             string          `json:"model"`
	Input             json.RawMessage `json:"input"`
	Instructions      string          `json:"instructions,omitempty"`
	Stream            bool            `json:"stream"`
	MaxOutputTokens   int             `json:"max_output_tokens,omitempty"`
	Temperature       *float64        `json:"temperature,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
	Tools             json.RawMessage `json:"tools,omitempty"`
	ToolChoice        any             `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	Store             *bool           `json:"store,omitempty"`
	Reasoning         *respReasoning  `json:"reasoning,omitempty"`
	Include           []string        `json:"include,omitempty"`
	PromptCacheKey    string          `json:"prompt_cache_key,omitempty"`
	Text              any             `json:"text,omitempty"`
}

type respReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type respInputMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

// respFunctionCallInput represents a function_call input item (previous
// assistant tool invocation in multi-turn conversations).
type respFunctionCallInput struct {
	Type      string `json:"type"` // "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	ID        string `json:"id,omitempty"`
	Status    string `json:"status,omitempty"`
}

type respInputContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type respTool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

// Response wire types

type respResponse struct {
	ID        string           `json:"id"`
	Object    string           `json:"object"`
	Model     string           `json:"model,omitempty"`
	Status    string           `json:"status,omitempty"`
	CreatedAt int64            `json:"created_at,omitempty"`
	Output    []respOutputItem `json:"output"`
	Usage     *respUsage       `json:"usage,omitempty"`
	Error     *respError       `json:"error,omitempty"`
}

type respError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type respOutputItem struct {
	Type      string             `json:"type"`
	ID        string             `json:"id,omitempty"`
	Status    string             `json:"status,omitempty"`
	Role      string             `json:"role,omitempty"`
	Content   []respContentBlock `json:"content,omitempty"`
	Summary   []respContentBlock `json:"summary,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
}

// respFunctionCallOutputInput represents a function_call_output input item.
type respFunctionCallOutputInput struct {
	Type   string `json:"type"` // "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type respContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// R3: respUsage includes input_tokens_details for cached token reporting.
type respUsage struct {
	InputTokens        int              `json:"input_tokens"`
	OutputTokens       int              `json:"output_tokens"`
	TotalTokens        int              `json:"total_tokens"`
	InputTokensDetails *respTokenDetail `json:"input_tokens_details,omitempty"`
}

type respTokenDetail struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// Streaming event types

type respStreamEvent struct {
	Type           string            `json:"type"`
	SequenceNumber int               `json:"sequence_number"`
	Response       *respResponse     `json:"response,omitempty"`
	Item           *respOutputItem   `json:"item,omitempty"`
	Part           *respContentBlock `json:"part,omitempty"`
	Delta          *respDelta        `json:"delta,omitempty"`
	OutputIndex    *int              `json:"output_index,omitempty"`
	ContentIndex   *int              `json:"content_index,omitempty"`
	ItemID         string            `json:"item_id,omitempty"`
	Arguments      string            `json:"arguments,omitempty"`
}

type respDelta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
