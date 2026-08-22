package ir

// Tool describes a tool (function) that the model may invoke.
type Tool struct {
	Name             string `json:"name"`
	Namespace        string `json:"namespace,omitempty"`
	NamespaceGroupID string `json:"-"` // original Responses namespace container; preserves same-protocol boundaries
	Description      string `json:"description"`
	InputSchema      any    `json:"input_schema"`
	Type             string `json:"type,omitempty"` // "function", "web_search", "custom", etc.
	Strict           *bool  `json:"strict,omitempty"`
	RawConfig        any    `json:"raw_config,omitempty"` // full config for non-function tools
}

// ToolCall represents a model-initiated tool invocation.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Arguments string `json:"arguments"`
}
