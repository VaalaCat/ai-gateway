// Package script 实现管理员动态 goja 脚本的编译与执行。
package script

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

// MatchInput 是脚本 scope 匹配所需的请求身份与路由信息。
type MatchInput struct {
	Source    attemptproxy.ChannelSource
	ChannelID uint
	Model     string
	UserID    uint
	GroupID   uint
}

// MatchScope 报告 scope 是否对目标请求生效。
// 五类条件均空时全局命中；非空条件按 OR 匹配。
func MatchScope(scope models.ScriptScope, input MatchInput) bool {
	if len(scope.ChannelIDs) == 0 && len(scope.PrivateChannelIDs) == 0 &&
		len(scope.ModelNames) == 0 && len(scope.GroupIDs) == 0 && len(scope.UserIDs) == 0 {
		return true
	}
	if input.Source == attemptproxy.SourceAdmin && containsID(scope.ChannelIDs, input.ChannelID) {
		return true
	}
	if input.Source == attemptproxy.SourcePrivate && containsID(scope.PrivateChannelIDs, input.ChannelID) {
		return true
	}
	if input.Model != "" {
		for _, model := range scope.ModelNames {
			if model == input.Model {
				return true
			}
		}
	}
	return containsID(scope.GroupIDs, input.GroupID) || containsID(scope.UserIDs, input.UserID)
}

func containsID(ids []uint, target uint) bool {
	if target == 0 {
		return false
	}
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
