package claude

import "encoding/json"

// ---------------------------------------------------------------------------
// Wire types for JSON (de)serialization
// ---------------------------------------------------------------------------

// claudeRequest is the Claude Messages API request JSON structure.
type claudeRequest struct {
	Model        string            `json:"model"`
	MaxTokens    int               `json:"max_tokens"`
	System       json.RawMessage   `json:"system,omitempty"`
	Messages     []claudeMessage   `json:"messages"`
	Stream       bool              `json:"stream,omitempty"`
	Tools        []any             `json:"tools,omitempty"`
	ToolChoice   *claudeToolChoice `json:"tool_choice,omitempty"`
	Temperature  *float64          `json:"temperature,omitempty"`
	TopP         *float64          `json:"top_p,omitempty"`
	TopK         *int              `json:"top_k,omitempty"`
	StopSeqs     []string          `json:"stop_sequences,omitempty"`
	ServiceTier  string            `json:"service_tier,omitempty"`
	InferenceGeo string            `json:"inference_geo,omitempty"`
	Thinking     *claudeThinking   `json:"thinking,omitempty"`
}

// claudeToolChoice represents the tool_choice field in Claude's API.
type claudeToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}

// claudeThinking represents the thinking configuration in Claude's API.
type claudeThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// claudeMessage represents a Claude message. Content can be a string or array.
type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeContentBlock struct {
	Type string `json:"type"`

	// text block
	Text string `json:"text,omitempty"`

	// thinking block
	Thinking string `json:"thinking,omitempty"`

	// image block
	Source *claudeImageSource `json:"source,omitempty"`

	// tool_use block
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`

	// tool_result block
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type claudeImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type claudeTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// Response wire types

type claudeResponse struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"`
	Role         string              `json:"role"`
	Content      []claudeRespContent `json:"content"`
	StopReason   string              `json:"stop_reason,omitempty"`
	StopSequence *string             `json:"stop_sequence,omitempty"`
	Usage        *claudeUsage        `json:"usage,omitempty"`
}

type claudeRespContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Input    any    `json:"input,omitempty"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

type claudeErrorResponse struct {
	Type  string        `json:"type"`
	Error claudeErrBody `json:"error"`
}

type claudeErrBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Streaming event types

type claudeSSEMessageStart struct {
	Type    string         `json:"type"`
	Message claudeResponse `json:"message"`
}

type claudeSSEContentBlockStart struct {
	Type         string            `json:"type"`
	Index        int               `json:"index"`
	ContentBlock claudeRespContent `json:"content_block"`
}

type claudeSSEContentBlockDelta struct {
	Type  string                `json:"type"`
	Index int                   `json:"index"`
	Delta claudeSSEDeltaPayload `json:"delta"`
}

type claudeSSEDeltaPayload struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Signature   string `json:"signature,omitempty"`
}

type claudeSSEMessageDelta struct {
	Type  string                    `json:"type"`
	Delta claudeSSEMessageDeltaBody `json:"delta"`
	Usage *claudeMessageDeltaUsage  `json:"usage,omitempty"`
}

type claudeSSEMessageDeltaBody struct {
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

type claudeMessageDeltaUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}
