package upstream

import (
	"regexp"

	"go.uber.org/zap"
)

// thinkingPassthroughRule is a compiled rule from model_thinking_passthrough config.
type thinkingPassthroughRule struct {
	pattern          *regexp.Regexp
	sendBackThinking bool
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
