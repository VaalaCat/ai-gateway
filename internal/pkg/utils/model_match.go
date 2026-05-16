package utils

import (
	"fmt"
	"regexp"
)

// ModelMatches 检查 modelName 是否匹配给定的模式列表。
// 空模式列表表示匹配所有模型。先尝试精确匹配，再尝试正则匹配（自动添加 ^$）。
func ModelMatches(modelName string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		// 精确匹配
		if p == modelName {
			return true
		}
		// 正则匹配：自动锚定
		re, err := regexp.Compile("^" + p + "$")
		if err != nil {
			continue
		}
		if re.MatchString(modelName) {
			return true
		}
	}
	return false
}

// ValidateModelPatterns 校验模式列表的合法性。
func ValidateModelPatterns(patterns []string) error {
	if len(patterns) > 100 {
		return fmt.Errorf("too many model patterns: %d (max 100)", len(patterns))
	}
	for _, p := range patterns {
		if len(p) > 200 {
			return fmt.Errorf("model pattern too long: %d chars (max 200)", len(p))
		}
		if _, err := regexp.Compile("^" + p + "$"); err != nil {
			return fmt.Errorf("invalid model pattern %q: %w", p, err)
		}
	}
	return nil
}
