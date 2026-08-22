// Package llmkit provides the public LLM access API.
package llmkit

import "github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"

type Protocol = ir.Protocol
type Request = ir.Request
type Message = ir.Message
type ContentBlock = ir.ContentBlock
type Tool = ir.Tool
type ToolCall = ir.ToolCall
type ToolChoice = ir.ToolChoice
type Event = ir.Event
type Usage = ir.Usage

type Role = ir.Role
type ContentType = ir.ContentType
type EventType = ir.EventType
type RawSSEEvent = ir.RawSSEEvent
type DeltaPayload = ir.DeltaPayload
type StreamingToolCall = ir.StreamingToolCall
type ToolCallDelta = ir.ToolCallDelta
type ErrorPayload = ir.ErrorPayload

const (
	ProtocolOpenAIChat      = ir.ProtocolOpenAIChat
	ProtocolOpenAIResponses = ir.ProtocolOpenAIResponses
	ProtocolClaudeMessages  = ir.ProtocolClaudeMessages
	ProtocolClaude          = ir.ProtocolClaude
	ProtocolGemini          = ir.ProtocolGemini
	ProtocolUnknown         = ir.ProtocolUnknown

	RoleSystem    = ir.RoleSystem
	RoleUser      = ir.RoleUser
	RoleAssistant = ir.RoleAssistant
	RoleTool      = ir.RoleTool
	RoleDeveloper = ir.RoleDeveloper

	ContentTypeText       = ir.ContentTypeText
	ContentTypeImage      = ir.ContentTypeImage
	ContentTypeAudio      = ir.ContentTypeAudio
	ContentTypeToolUse    = ir.ContentTypeToolUse
	ContentTypeToolResult = ir.ContentTypeToolResult
	ContentTypeThinking   = ir.ContentTypeThinking
	ContentTypeInputText  = ir.ContentTypeInputText
	ContentTypeOutputText = ir.ContentTypeOutputText
	ContentTypeImageURL   = ir.ContentTypeImageURL
	ContentTypeFunction   = ir.ContentTypeFunction

	ImageSourceBase64 = ir.ImageSourceBase64
	ImageSourceURL    = ir.ImageSourceURL

	EventStreamStart            = ir.EventStreamStart
	EventContentDelta           = ir.EventContentDelta
	EventToolCallDelta          = ir.EventToolCallDelta
	EventThinkingDelta          = ir.EventThinkingDelta
	EventUsage                  = ir.EventUsage
	EventDone                   = ir.EventDone
	EventError                  = ir.EventError
	EventRawPassthrough         = ir.EventRawPassthrough
	EventContentBlockStop       = ir.EventContentBlockStop
	EventSignatureDelta         = ir.EventSignatureDelta
	EventToolCallStart          = ir.EventToolCallStart
	EventToolCallArgumentsDelta = ir.EventToolCallArgumentsDelta
	EventToolCallEnd            = ir.EventToolCallEnd
)
