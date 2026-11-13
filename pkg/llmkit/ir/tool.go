package ir

import "encoding/json"

// Tool describes a tool (function) that the model may invoke.
type Tool struct {
	Name                 string  `json:"name"`
	Namespace            string  `json:"namespace,omitempty"`
	NamespaceGroupID     string  `json:"-"` // original Responses namespace container; preserves same-protocol boundaries
	NamespaceDescription *string `json:"-"` // original Responses namespace description; shared by its expanded functions
	Description          string  `json:"description"`
	InputSchema          any     `json:"input_schema"`
	Type                 string  `json:"type,omitempty"` // "function", "web_search", "custom", etc.
	Strict               *bool   `json:"strict,omitempty"`
	RawConfig            any     `json:"raw_config,omitempty"` // full config for non-function tools
}

// ToolCall represents a model-initiated tool invocation.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Arguments string `json:"arguments"`

	ProviderData *ToolCallProviderData `json:"provider_data,omitempty"`
}

// ProviderData 保存厂商附在工具调用或内容块上的签名等原始字段。
type ProviderData struct {
	Protocol Protocol                   `json:"protocol"`         // 产生这些字段的协议
	Models   []string                   `json:"models,omitempty"` // 每经过一层追加该层发给上游的模型名，最里层在前；为空表示使用者自带
	Fields   map[string]json.RawMessage `json:"fields"`           // 原生字段原样保存
}

// ToolCallProviderData 保留工具调用厂商数据的公开类型名称。
type ToolCallProviderData = ProviderData
