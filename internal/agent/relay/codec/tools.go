package codec

import (
	"errors"
	"fmt"
)

// BuiltinToolFallbackPolicy 定义在目标协议无法表达某个内置工具时的处理策略。
type BuiltinToolFallbackPolicy string

const (
	BuiltinToolFallbackDrop        BuiltinToolFallbackPolicy = "drop"
	BuiltinToolFallbackError       BuiltinToolFallbackPolicy = "error"
	BuiltinToolFallbackPassthrough BuiltinToolFallbackPolicy = "passthrough"
	BuiltinToolFallbackFunction    BuiltinToolFallbackPolicy = "function"
)

// NormalizeBuiltinToolFallback 把配置里的字符串归一到合法策略。
// 未知值（含空串）一律回落到 BuiltinToolFallbackDrop。
func NormalizeBuiltinToolFallback(s string) BuiltinToolFallbackPolicy {
	switch BuiltinToolFallbackPolicy(s) {
	case BuiltinToolFallbackDrop, BuiltinToolFallbackError, BuiltinToolFallbackPassthrough, BuiltinToolFallbackFunction:
		return BuiltinToolFallbackPolicy(s)
	default:
		return BuiltinToolFallbackDrop
	}
}

// ErrFunctionToolMissingName 表示一个声称 type=function 的工具缺少 name。
// 这是 IR 的结构性错误，与 policy 无关，必须 fail-loud。
var ErrFunctionToolMissingName = errors.New("codec: function tool has empty name")

// ErrBuiltinToolUnsupported 表示目标协议无法表达该内置工具，且渠道策略为 error。
var ErrBuiltinToolUnsupported = errors.New("codec: built-in tool not supported by target protocol")

// DroppedTool 描述被 ResolveTool 丢弃的工具，供可观测层记录。
type DroppedTool struct {
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

const (
	DroppedToolReasonCrossProtocolIncompatible   = "cross_protocol_incompatible"
	DroppedToolReasonFunctionFallbackUnsupported = "function_fallback_unsupported"
)

// FunctionFallbackTool records a custom tool exposed to a limited upstream as
// a function tool. The response path uses this mapping to restore the original
// custom_tool_call shape expected by the client.
type FunctionFallbackTool struct {
	Name         string `json:"name"`
	ArgumentName string `json:"argument_name"`
}

// ResolvedTool 是 ResolveTool 的返回值。Emit 与 Dropped 恰有一个被填充。
type ResolvedTool struct {
	Emit             any
	Dropped          *DroppedTool
	FunctionFallback *FunctionFallbackTool
}

// TargetEmitFuncs 由调用方（编码器）提供，集中目标协议特有的 wire shape 构造。
type TargetEmitFuncs struct {
	// Function 把 IR 的 function tool 构造成目标协议的 wire 形状。
	Function func(t Tool) any
}

// ResolveTool 是把 IR Tool 映射到目标协议 wire 表达的唯一入口。
// 所有出站 codec 必须委托给它，不得自行构造工具记录。
//
// 决策顺序（自上而下、首个命中为准）：
//
//	A. Type == "function"（含空）且 Name != "" → Emit = targetEmit.Function(t)
//	B. Type == "function"（含空）且 Name == "" → ErrFunctionToolMissingName（policy 无关）
//	C. Responses 目标 && policy = function && named custom tool → 作为 function 发出并记录反向映射
//	D. policy = function && 其它非 function 工具 → Dropped
//	E. 内置 && source == target && RawConfig != nil → Emit = RawConfig（同协议透传）
//	F. 内置 && 跨协议 && policy = drop → Dropped
//	G. 内置 && 跨协议 && policy = error → ErrBuiltinToolUnsupported
//	H. 内置 && 跨协议 && policy = passthrough → Emit = RawConfig（运维自担）
//
// 说明：source 为零值 Protocol("") 被视作"未知入站"，等同于"源 != 目标"。
func ResolveTool(
	t Tool,
	source, target Protocol,
	policy BuiltinToolFallbackPolicy,
	targetEmit TargetEmitFuncs,
) (ResolvedTool, error) {
	isFunction := t.Type == "" || t.Type == "function"

	// 分支 A / B —— function tool
	if isFunction {
		if t.Name == "" {
			return ResolvedTool{}, ErrFunctionToolMissingName
		}
		return ResolvedTool{Emit: targetEmit.Function(t)}, nil
	}
	if policy == BuiltinToolFallbackFunction {
		if target == ProtocolOpenAIResponses && t.Type == "custom" && t.Name != "" {
			functionTool := functionFallbackTool(t)
			return ResolvedTool{
				Emit: targetEmit.Function(functionTool),
				FunctionFallback: &FunctionFallbackTool{
					Name:         t.Name,
					ArgumentName: "input",
				},
			}, nil
		}
		return ResolvedTool{Dropped: &DroppedTool{
			Type:   t.Type,
			Name:   t.Name,
			Reason: DroppedToolReasonFunctionFallbackUnsupported,
		}}, nil
	}

	// 内置工具分支
	sameProtocol := source != "" && source == target
	if sameProtocol && t.RawConfig != nil {
		// 分支 C
		return ResolvedTool{Emit: t.RawConfig}, nil
	}

	// 跨协议（含 RawConfig nil 或 source 未知）
	switch policy {
	case BuiltinToolFallbackError:
		// 分支 E
		return ResolvedTool{}, ErrBuiltinToolUnsupported
	case BuiltinToolFallbackPassthrough:
		// 分支 F
		return ResolvedTool{Emit: t.RawConfig}, nil
	default:
		// 分支 D —— drop（NormalizeBuiltinToolFallback 保证未知值归一到 drop）
		return ResolvedTool{
			Dropped: &DroppedTool{
				Type:   t.Type,
				Name:   t.Name,
				Reason: DroppedToolReasonCrossProtocolIncompatible,
			},
		}, nil
	}
}

func functionFallbackTool(t Tool) Tool {
	t.Type = "function"
	if t.InputSchema == nil {
		if raw, ok := t.RawConfig.(map[string]any); ok {
			t.InputSchema = raw["parameters"]
		}
	}
	t.RawConfig = nil
	if t.InputSchema == nil {
		t.InputSchema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": "Raw input for the original custom tool.",
				},
			},
			"required":             []string{"input"},
			"additionalProperties": false,
		}
	}
	return t
}

// AssertToolsInvariant 扫描已构造好的出站 tools 数组，若任何 function 形状的
// 条目 name 为空则返回 ErrFunctionToolMissingName。这是编码器序列化前的最后
// 一道绊马索：即使未来有其它路径往 raw.Tools 里塞东西，也能兜住结构性缺陷。
//
// 识别的三种 function 形状：
//
//	OpenAI Chat:      {"type":"function", "function":{"name":"..."}}
//	OpenAI Responses: {"type":"function", "name":"..."}
//	Claude:           {"name":"..."}（顶层 name，且无 type 字段或 type 非 function）
//
// 非 function 形状（如内置工具的 RawConfig passthrough）一律跳过。
func AssertToolsInvariant(tools []any) error {
	for i, t := range tools {
		if functionToolHasEmptyName(t) {
			return fmt.Errorf("%w (index %d/%d)", ErrFunctionToolMissingName, i, len(tools))
		}
	}
	return nil
}

// functionToolHasEmptyName 仅在给定条目形如"function tool 且 name 为空"时返回 true。
// 内置工具 / 不认识的形状返回 false（让调用方自行处理）。
func functionToolHasEmptyName(t any) bool {
	m, ok := t.(map[string]any)
	if !ok {
		return false
	}

	// OpenAI Chat：type="function" + function.name 存在且为空
	if typ, _ := m["type"].(string); typ == "function" {
		if inner, ok := m["function"].(map[string]any); ok {
			if name, hasName := inner["name"].(string); hasName && name == "" {
				return true
			}
			// 没 name key 也算违规
			if _, hasName := inner["name"]; !hasName {
				return true
			}
			return false
		}
		// OpenAI Responses：type="function" 但顶层 name 为空
		if name, hasName := m["name"].(string); hasName && name == "" {
			return true
		}
		if _, hasName := m["name"]; !hasName {
			return true
		}
		return false
	}

	// Claude：顶层 name 为空；但 Claude 工具本身没 type 字段。
	// 为避免把内置工具 passthrough（可能只有 type）误判，只在同时缺 type 字段时适用。
	if _, hasType := m["type"]; !hasType {
		if name, hasName := m["name"].(string); hasName && name == "" {
			return true
		}
	}

	return false
}

// RecordDroppedTools 把被丢弃的工具列表写到 req.Metadata["dropped_tools"]，
// 以便 relay 层做日志 / trace 导出。空切片 / nil 为 no-op。
func RecordDroppedTools(req *Request, dropped []DroppedTool) {
	if len(dropped) == 0 {
		return
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any)
	}
	req.Metadata["dropped_tools"] = dropped
}

const functionFallbackToolsMetadataKey = "function_fallback_tools"

func RecordFunctionFallbackTools(req *Request, tools []FunctionFallbackTool) {
	if len(tools) == 0 {
		return
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any)
	}
	req.Metadata[functionFallbackToolsMetadataKey] = tools
}

func FunctionFallbackTools(req *Request) map[string]FunctionFallbackTool {
	if req == nil || req.Metadata == nil {
		return nil
	}
	tools, ok := req.Metadata[functionFallbackToolsMetadataKey].([]FunctionFallbackTool)
	if !ok || len(tools) == 0 {
		return nil
	}
	out := make(map[string]FunctionFallbackTool, len(tools))
	for _, tool := range tools {
		out[tool.Name] = tool
	}
	return out
}
