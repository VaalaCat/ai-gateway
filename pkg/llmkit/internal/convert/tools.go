package convert

import (
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type BuiltinToolFallbackPolicy string

const (
	BuiltinToolFallbackDrop        BuiltinToolFallbackPolicy = "drop"
	BuiltinToolFallbackError       BuiltinToolFallbackPolicy = "error"
	BuiltinToolFallbackPassthrough BuiltinToolFallbackPolicy = "passthrough"
	BuiltinToolFallbackFunction    BuiltinToolFallbackPolicy = "function"
)

func NormalizeBuiltinToolFallback(value string) BuiltinToolFallbackPolicy {
	switch BuiltinToolFallbackPolicy(value) {
	case BuiltinToolFallbackDrop, BuiltinToolFallbackError, BuiltinToolFallbackPassthrough, BuiltinToolFallbackFunction:
		return BuiltinToolFallbackPolicy(value)
	default:
		return BuiltinToolFallbackDrop
	}
}

var (
	ErrFunctionToolMissingName = errors.New("codec: function tool has empty name")
	ErrBuiltinToolUnsupported  = errors.New("codec: built-in tool not supported by target protocol")
)

type DroppedTool struct {
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

const (
	DroppedToolReasonCrossProtocolIncompatible   = "cross_protocol_incompatible"
	DroppedToolReasonFunctionFallbackUnsupported = "function_fallback_unsupported"
)

type FunctionFallbackTool struct {
	Name         string `json:"name"`
	ArgumentName string `json:"argument_name"`
}

type ResolvedTool struct {
	Emit             any
	Dropped          *DroppedTool
	FunctionFallback *FunctionFallbackTool
}

type TargetEmitFuncs struct {
	Function func(ir.Tool) any
}

type RequestFieldPermissions struct {
	AllowServiceTier        bool `json:"allow_service_tier,omitempty"`
	AllowInferenceGeo       bool `json:"allow_inference_geo,omitempty"`
	AllowStore              bool `json:"allow_store"`
	AllowSafetyIdentifier   bool `json:"allow_safety_identifier,omitempty"`
	AllowIncludeObfuscation bool `json:"allow_include_obfuscation,omitempty"`
}

type Options struct {
	BuiltinToolFallback BuiltinToolFallbackPolicy
	RequestFields       RequestFieldPermissions
}

func ResolveTool(tool ir.Tool, source, target ir.Protocol, policy BuiltinToolFallbackPolicy, emit TargetEmitFuncs) (ResolvedTool, error) {
	if tool.Type == "" || tool.Type == "function" {
		if tool.Name == "" {
			return ResolvedTool{}, ErrFunctionToolMissingName
		}
		return ResolvedTool{Emit: emit.Function(tool)}, nil
	}
	if policy == BuiltinToolFallbackFunction {
		if target == ir.ProtocolOpenAIResponses && tool.Type == "custom" && tool.Name != "" {
			fallback := functionFallbackTool(tool)
			return ResolvedTool{
				Emit:             emit.Function(fallback),
				FunctionFallback: &FunctionFallbackTool{Name: tool.Name, ArgumentName: "input"},
			}, nil
		}
		return ResolvedTool{Dropped: &DroppedTool{Type: tool.Type, Name: tool.Name, Reason: DroppedToolReasonFunctionFallbackUnsupported}}, nil
	}
	if source != "" && source == target && tool.RawConfig != nil {
		return ResolvedTool{Emit: tool.RawConfig}, nil
	}
	switch policy {
	case BuiltinToolFallbackError:
		return ResolvedTool{}, ErrBuiltinToolUnsupported
	case BuiltinToolFallbackPassthrough:
		return ResolvedTool{Emit: tool.RawConfig}, nil
	default:
		return ResolvedTool{Dropped: &DroppedTool{Type: tool.Type, Name: tool.Name, Reason: DroppedToolReasonCrossProtocolIncompatible}}, nil
	}
}

func functionFallbackTool(tool ir.Tool) ir.Tool {
	tool.Type = "function"
	if tool.InputSchema == nil {
		if raw, ok := tool.RawConfig.(map[string]any); ok {
			tool.InputSchema = raw["parameters"]
		}
	}
	tool.RawConfig = nil
	if tool.InputSchema == nil {
		tool.InputSchema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string", "description": "Raw input for the original custom tool."},
			},
			"required": []string{"input"}, "additionalProperties": false,
		}
	}
	return tool
}

func AssertToolsInvariant(tools []any) error {
	for index, tool := range tools {
		if functionToolHasEmptyName(tool) {
			return fmt.Errorf("%w (index %d/%d)", ErrFunctionToolMissingName, index, len(tools))
		}
	}
	return nil
}

func functionToolHasEmptyName(tool any) bool {
	value, ok := tool.(map[string]any)
	if !ok {
		return false
	}
	if toolType, _ := value["type"].(string); toolType == "function" {
		if function, ok := value["function"].(map[string]any); ok {
			if name, ok := function["name"].(string); ok && name == "" {
				return true
			}
			_, exists := function["name"]
			return !exists
		}
		if name, ok := value["name"].(string); ok && name == "" {
			return true
		}
		_, exists := value["name"]
		return !exists
	}
	if _, typed := value["type"]; !typed {
		if name, exists := value["name"].(string); exists && name == "" {
			return true
		}
	}
	return false
}

const (
	droppedToolsMetadataKey          = "dropped_tools"
	functionFallbackToolsMetadataKey = "function_fallback_tools"
)

func RecordDroppedTools(request *ir.Request, dropped []DroppedTool) {
	if request == nil || len(dropped) == 0 {
		return
	}
	if request.Metadata == nil {
		request.Metadata = make(map[string]any)
	}
	request.Metadata[droppedToolsMetadataKey] = append([]DroppedTool(nil), dropped...)
}

func DroppedTools(request *ir.Request) []DroppedTool {
	if request == nil || request.Metadata == nil {
		return nil
	}
	dropped, ok := request.Metadata[droppedToolsMetadataKey].([]DroppedTool)
	if !ok || len(dropped) == 0 {
		return nil
	}
	return append([]DroppedTool(nil), dropped...)
}

func RecordFunctionFallbackTools(request *ir.Request, tools []FunctionFallbackTool) {
	if request == nil || len(tools) == 0 {
		return
	}
	if request.Metadata == nil {
		request.Metadata = make(map[string]any)
	}
	request.Metadata[functionFallbackToolsMetadataKey] = append([]FunctionFallbackTool(nil), tools...)
}

func FunctionFallbackTools(request *ir.Request) map[string]FunctionFallbackTool {
	if request == nil || request.Metadata == nil {
		return nil
	}
	tools, ok := request.Metadata[functionFallbackToolsMetadataKey].([]FunctionFallbackTool)
	if !ok || len(tools) == 0 {
		return nil
	}
	out := make(map[string]FunctionFallbackTool, len(tools))
	for _, tool := range tools {
		out[tool.Name] = tool
	}
	return out
}
