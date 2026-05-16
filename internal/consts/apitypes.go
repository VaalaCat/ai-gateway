package consts

// API type strings stored in database / used by frontend.
const (
	APITypeChatCompletion = "chat-completion"
	APITypeResponses      = "responses"
	APITypeClaude         = "claude"
)

// Finish reason values (normalized across protocols).
const (
	FinishReasonStop          = "stop"
	FinishReasonLength        = "length"
	FinishReasonToolCalls     = "tool_calls"
	FinishReasonContentFilter = "content_filter"
)

// Claude-specific stop reason values.
const (
	ClaudeStopEndTurn   = "end_turn"
	ClaudeStopMaxTokens = "max_tokens"
	ClaudeStopToolUse   = "tool_use"
)
