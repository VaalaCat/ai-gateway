package sse

// Claude Messages API SSE event names.
const (
	MessageStart      = "message_start"
	ContentBlockStart = "content_block_start"
	ContentBlockDelta = "content_block_delta"
	ContentBlockStop  = "content_block_stop"
	MessageDelta      = "message_delta"
	MessageStop       = "message_stop"
)

// Claude delta type values.
const (
	ClaudeTextDelta      = "text_delta"
	ClaudeInputJSONDelta = "input_json_delta"
	ClaudeThinkingDelta  = "thinking_delta"
	ClaudeSignatureDelta = "signature_delta"
)
