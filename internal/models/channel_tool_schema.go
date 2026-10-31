package models

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// ValidateToolSchemaCompatibility 校验渠道工具定义兼容规则的模型正则和兼容方式。
func ValidateToolSchemaCompatibility(otherSettings string) error {
	// 1. 读取工具定义兼容规则。
	var other map[string]json.RawMessage
	if json.Unmarshal([]byte(otherSettings), &other) != nil {
		return nil
	}
	raw, ok := other["model_tool_schema_compatibility"]
	if !ok {
		return nil
	}
	var rules []struct {
		ModelPattern string                   `json:"model_pattern"`
		Profile      llmkit.ToolSchemaProfile `json:"profile"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return fmt.Errorf("model_tool_schema_compatibility: %w", err)
	}

	// 2. 使用 Go 正则语法校验每条规则。
	for index, rule := range rules {
		if rule.ModelPattern == "" {
			return fmt.Errorf("model_tool_schema_compatibility rule %d: model_pattern is required", index+1)
		}
		if _, err := regexp.Compile(rule.ModelPattern); err != nil {
			return fmt.Errorf("model_tool_schema_compatibility rule %d: %w", index+1, err)
		}
		if rule.Profile != llmkit.ToolSchemaProfileNone && rule.Profile != llmkit.ToolSchemaProfileGeminiCompat {
			return fmt.Errorf("model_tool_schema_compatibility rule %d: unsupported profile %q", index+1, rule.Profile)
		}
	}
	return nil
}
