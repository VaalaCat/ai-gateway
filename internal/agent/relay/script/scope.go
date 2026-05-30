// Package script 实现管理员动态 goja 脚本的编译与执行。
package script

import "github.com/VaalaCat/ai-gateway/internal/models"

// MatchScope 报告 scope 是否对目标请求生效。
// channelID 为 0 表示尚未路由（入站 onRequest 阶段），此时 channel 作用域不命中。
// ChannelIDs 与 ModelNames 均空 => 全局，恒命中。
func MatchScope(scope models.ScriptScope, channelID uint, model string) bool {
	if len(scope.ChannelIDs) == 0 && len(scope.ModelNames) == 0 {
		return true
	}
	for _, id := range scope.ChannelIDs {
		if channelID != 0 && id == channelID {
			return true
		}
	}
	for _, m := range scope.ModelNames {
		if m == model {
			return true
		}
	}
	return false
}
