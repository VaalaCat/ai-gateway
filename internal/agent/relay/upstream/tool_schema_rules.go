package upstream

import (
	"encoding/json"
	"regexp"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"go.uber.org/zap"
)

type toolCompatibilityRule struct {
	pattern       *regexp.Regexp
	compatibility ToolCompatibility
}

// ToolCompatibility 保存一条模型规则的工具定义和签名兼容方式。
type ToolCompatibility struct {
	Profile           llmkit.ToolSchemaProfile
	SignatureFallback llmkit.ToolSignatureFallback
}

// ToolCompatibilityRules 保存某渠道已编译的工具兼容规则。
type ToolCompatibilityRules struct {
	rules []toolCompatibilityRule
}

// NewToolCompatibilityRules 读取渠道配置，按配置顺序编译有效规则。
func NewToolCompatibilityRules(ch *models.Channel) ToolCompatibilityRules {
	// 1. 读取规则列表。
	var other map[string]any
	if ch == nil || json.Unmarshal([]byte(ch.OtherSettings), &other) != nil {
		return ToolCompatibilityRules{}
	}
	entries, _ := other["model_tool_schema_compatibility"].([]any)
	var rules []toolCompatibilityRule
	// 2. 编译模型正则，保留有效的兼容方式。
	for index, entry := range entries {
		obj, _ := entry.(map[string]any)
		pattern, _ := obj["model_pattern"].(string)
		profile, _ := obj["profile"].(string)
		fallback, _ := obj["signature_fallback"].(string)
		if fallback == "" {
			fallback = string(llmkit.ToolSignatureFallbackNone)
		}
		compiled, err := regexp.Compile(pattern)
		if pattern == "" || err != nil ||
			(profile != string(llmkit.ToolSchemaProfileNone) && profile != string(llmkit.ToolSchemaProfileGeminiCompat)) ||
			(fallback != string(llmkit.ToolSignatureFallbackNone) && fallback != string(llmkit.ToolSignatureFallbackGemini)) {
			zap.L().Warn("model_tool_schema_compatibility: invalid rule",
				zap.Uint("channel_id", ch.ID), zap.Int("index", index))
			continue
		}
		rules = append(rules, toolCompatibilityRule{
			pattern: compiled,
			compatibility: ToolCompatibility{
				Profile:           llmkit.ToolSchemaProfile(profile),
				SignatureFallback: llmkit.ToolSignatureFallback(fallback),
			},
		})
	}
	return ToolCompatibilityRules{rules: rules}
}

// Match 返回任一模型名命中的首条完整规则；未命中时不处理。
func (r ToolCompatibilityRules) Match(modelNames ...string) ToolCompatibility {
	// 1. 按配置顺序匹配规则，每条规则检查映射前后的模型名。
	for _, rule := range r.rules {
		for _, modelName := range modelNames {
			if rule.pattern.MatchString(modelName) {
				return rule.compatibility
			}
		}
	}
	return ToolCompatibility{Profile: llmkit.ToolSchemaProfileNone, SignatureFallback: llmkit.ToolSignatureFallbackNone}
}

// Len 返回有效规则数。
func (r ToolCompatibilityRules) Len() int { return len(r.rules) }
