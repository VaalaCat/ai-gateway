package upstream

import (
	"encoding/json"
	"regexp"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

// thinkingPassthroughRule is a compiled rule from model_thinking_passthrough config.
type thinkingPassthroughRule struct {
	pattern          *regexp.Regexp
	sendBackThinking bool
}

// ThinkingRules 是某 channel 已编译的 model_thinking_passthrough 规则集合。
type ThinkingRules struct {
	rules []thinkingPassthroughRule
}

// Len 返回已解析的 thinking 规则数量。
func (t ThinkingRules) Len() int {
	// 1. 读取已编译规则的数量。
	return len(t.rules)
}

// NewThinkingRules 从 channel 的 OtherSettings 解析出 thinking 规则。
func NewThinkingRules(ch *models.Channel) ThinkingRules {
	if ch == nil || ch.OtherSettings == "" {
		return ThinkingRules{}
	}
	var other map[string]any
	if err := json.Unmarshal([]byte(ch.OtherSettings), &other); err != nil {
		return ThinkingRules{}
	}
	rawTP, ok := other["model_thinking_passthrough"].([]any)
	if !ok {
		return ThinkingRules{}
	}
	return ThinkingRules{rules: parseModelThinkingPassthrough(rawTP, ch.ID)}
}

// SendBack 返回任一模型名命中的首条规则的 send_back_thinking；未命中返回 false。
func (t ThinkingRules) SendBack(modelNames ...string) bool {
	// 1. 按配置顺序匹配规则，每条规则检查映射前后的模型名。
	for _, r := range t.rules {
		for _, modelName := range modelNames {
			if r.pattern.MatchString(modelName) {
				return r.sendBackThinking
			}
		}
	}
	return false
}

// parseModelThinkingPassthrough 把 OtherSettings.model_thinking_passthrough 的
// 原始 JSON 列表编译为规则。非法 entry 跳过 + warn，参考 parseModelProtocolOverride。
func parseModelThinkingPassthrough(raw []any, channelID uint) []thinkingPassthroughRule {
	rules := make([]thinkingPassthroughRule, 0, len(raw))
	for i, entry := range raw {
		obj, ok := entry.(map[string]any)
		if !ok {
			zap.L().Warn("model_thinking_passthrough: entry is not an object",
				zap.Int("index", i), zap.Uint("channel_id", channelID))
			continue
		}
		patternStr, _ := obj["model_pattern"].(string)
		if patternStr == "" {
			zap.L().Warn("model_thinking_passthrough: empty model_pattern",
				zap.Int("index", i), zap.Uint("channel_id", channelID))
			continue
		}
		re, err := regexp.Compile(patternStr)
		if err != nil {
			zap.L().Warn("model_thinking_passthrough: invalid regex",
				zap.Int("index", i), zap.Uint("channel_id", channelID),
				zap.String("pattern", patternStr), zap.Error(err))
			continue
		}
		sb, _ := obj["send_back_thinking"].(bool)
		rules = append(rules, thinkingPassthroughRule{pattern: re, sendBackThinking: sb})
	}
	return rules
}
