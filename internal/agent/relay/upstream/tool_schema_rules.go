package upstream

import (
	"encoding/json"
	"regexp"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"go.uber.org/zap"
)

type toolSchemaRule struct {
	pattern *regexp.Regexp
	profile llmkit.ToolSchemaProfile
}

// ToolSchemaRules 保存某渠道已编译的工具定义兼容规则。
type ToolSchemaRules struct {
	rules []toolSchemaRule
}

// NewToolSchemaRules 读取渠道配置，按配置顺序编译有效规则。
func NewToolSchemaRules(ch *models.Channel) ToolSchemaRules {
	// 1. 读取规则列表。
	var other map[string]any
	if ch == nil || json.Unmarshal([]byte(ch.OtherSettings), &other) != nil {
		return ToolSchemaRules{}
	}
	entries, _ := other["model_tool_schema_compatibility"].([]any)
	var rules []toolSchemaRule
	// 2. 编译模型正则，保留有效的兼容方式。
	for index, entry := range entries {
		obj, _ := entry.(map[string]any)
		pattern, _ := obj["model_pattern"].(string)
		profile, _ := obj["profile"].(string)
		compiled, err := regexp.Compile(pattern)
		if pattern == "" || err != nil || (profile != string(llmkit.ToolSchemaProfileNone) && profile != string(llmkit.ToolSchemaProfileGeminiCompat)) {
			zap.L().Warn("model_tool_schema_compatibility: invalid rule",
				zap.Uint("channel_id", ch.ID), zap.Int("index", index))
			continue
		}
		rules = append(rules, toolSchemaRule{pattern: compiled, profile: llmkit.ToolSchemaProfile(profile)})
	}
	return ToolSchemaRules{rules: rules}
}

// Profile 返回映射后模型名命中的首条规则；未命中时不处理。
func (r ToolSchemaRules) Profile(model string) llmkit.ToolSchemaProfile {
	// 1. 按顺序匹配模型名。
	for _, rule := range r.rules {
		if rule.pattern.MatchString(model) {
			return rule.profile
		}
	}
	return llmkit.ToolSchemaProfileNone
}

// Len 返回有效规则数。
func (r ToolSchemaRules) Len() int { return len(r.rules) }
