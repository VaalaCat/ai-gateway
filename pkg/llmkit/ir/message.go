package ir

import "encoding/json"

// Role represents the role of a message participant.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleDeveloper Role = "developer"
)

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
