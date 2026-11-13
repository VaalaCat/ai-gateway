// Package gemini 转换 Gemini 原生生成请求与响应。
package gemini

import (
	"encoding/json"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text                *string           `json:"text,omitempty"`
	Thought             bool              `json:"thought,omitempty"`
	ThoughtSignature    json.RawMessage   `json:"thoughtSignature,omitempty"`
	InlineData          *media            `json:"inlineData,omitempty"`
	FileData            *fileData         `json:"fileData,omitempty"`
	FunctionCall        *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse    *functionResponse `json:"functionResponse,omitempty"`
	ExecutableCode      json.RawMessage   `json:"executableCode,omitempty"`
	CodeExecutionResult json.RawMessage   `json:"codeExecutionResult,omitempty"`
}

type media struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}
type fileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}
type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}
type functionResponse struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Response any    `json:"response"`
	Parts    []part `json:"parts,omitempty"`
}
type response struct {
	Candidates     []candidate    `json:"candidates"`
	UsageMetadata  *usageMetadata `json:"usageMetadata,omitempty"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	ModelVersion string `json:"modelVersion"`
	Error        *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}
type candidate struct {
	Content      content `json:"content"`
	Index        int     `json:"index"`
	FinishReason string  `json:"finishReason"`
}
type usageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ToolUsePromptTokenCount int `json:"toolUsePromptTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}
type contentPosition struct {
	PartIndex  int `json:"part_index"`
	TextOffset int `json:"text_offset"`
}
type pendingCall struct {
	Call      ir.StreamingToolCall
	PartIndex int
}

type pendingEvent struct {
	Event     ir.Event
	CallIndex int
}
