package llmkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolSchemaProfile 指定发送给上游的工具定义兼容方式。
type ToolSchemaProfile string

const (
	ToolSchemaProfileNone         ToolSchemaProfile = "none"
	ToolSchemaProfileGeminiCompat ToolSchemaProfile = "gemini_compat"
)

// ToolSchemaChange 记录被移除字段的工具名和 IR JSON Pointer 路径。
type ToolSchemaChange struct {
	ToolName string `json:"tool_name"`
	Path     string `json:"path"`
}

// ConvertToolSchemas 生成兼容后的工具定义副本，并列出被移除的选项和约束。
func ConvertToolSchemas(tools []Tool, profile ToolSchemaProfile) ([]Tool, []ToolSchemaChange) {
	// 1. 选择兼容方式。
	if profile != ToolSchemaProfileGeminiCompat {
		return tools, nil
	}

	// 2. 转换函数工具的选项和参数 schema。
	converted := append([]Tool(nil), tools...)
	var changes []ToolSchemaChange
	for index := range converted {
		tool := &converted[index]
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		path := fmt.Sprintf("/tools/%d", index)
		if tool.Strict != nil {
			tool.Strict = nil
			changes = append(changes, ToolSchemaChange{ToolName: tool.Name, Path: path + "/strict"})
		}
		tool.InputSchema = convertGeminiToolSchema(tool.InputSchema, path+"/input_schema", tool.Name, &changes)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return converted, changes
}

// convertGeminiToolSchema 遍历 schema 节点，保留参数名、默认值和枚举数据。
func convertGeminiToolSchema(schema any, path, toolName string, changes *[]ToolSchemaChange) any {
	// 1. 解码命名空间函数保留的原始 schema JSON。
	if raw, ok := schema.(json.RawMessage); ok {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return schema
		}
		schema = decoded
	}

	// 2. 复制当前 schema 节点并移除不支持的数值约束。
	node, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	converted := make(map[string]any, len(node))
	for key, value := range node {
		childPath := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		switch key {
		case "exclusiveMinimum", "exclusiveMaximum":
			*changes = append(*changes, ToolSchemaChange{ToolName: toolName, Path: childPath})
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
			// 字典的键是参数名或定义名，只有值是 schema。
			if entries, ok := value.(map[string]any); ok {
				convertedEntries := make(map[string]any, len(entries))
				for name, entry := range entries {
					entryPath := childPath + "/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
					convertedEntries[name] = convertGeminiToolSchema(entry, entryPath, toolName, changes)
				}
				converted[key] = convertedEntries
			} else {
				converted[key] = value
			}
		case "items", "prefixItems", "allOf", "anyOf", "oneOf":
			// 遍历单个子 schema 或 schema 数组。
			if entries, ok := value.([]any); ok {
				convertedEntries := make([]any, len(entries))
				for index, entry := range entries {
					convertedEntries[index] = convertGeminiToolSchema(entry, fmt.Sprintf("%s/%d", childPath, index), toolName, changes)
				}
				converted[key] = convertedEntries
			} else {
				converted[key] = convertGeminiToolSchema(value, childPath, toolName, changes)
			}
		case "additionalProperties", "additionalItems", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "not", "if", "then", "else", "contentSchema":
			converted[key] = convertGeminiToolSchema(value, childPath, toolName, changes)
		default:
			converted[key] = value
		}
	}
	return converted
}
