// Package ir defines the protocol-agnostic intermediate representation used
// by LLM protocol codecs.
package ir

// Protocol identifies a wire protocol supported by the gateway.
type Protocol string

const (
	ProtocolOpenAIChat      Protocol = "openai_chat"
	ProtocolOpenAIResponses Protocol = "openai_responses"
	ProtocolClaude          Protocol = "claude"
	ProtocolGemini          Protocol = "gemini"
	ProtocolUnknown         Protocol = "unknown"

	ProtocolClaudeMessages = ProtocolClaude
)
