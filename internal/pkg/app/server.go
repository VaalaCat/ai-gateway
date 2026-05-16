package app

import (
	"context"

	"github.com/gin-gonic/gin"
)

// MasterServer Master 控制面服务器
// 负责管理 API、WebSocket Hub、计费结算等控制面功能
type MasterServer interface {
	Run() error
	Shutdown(ctx context.Context) error
	InitAdminUser(username, password string) error
}

// AgentServer Agent 数据面服务器
// 负责请求中继、配置缓存同步、指标上报等数据面功能
type AgentServer interface {
	Run() error
	RunBackground(ctx context.Context)
	Shutdown(ctx context.Context) error
	MountRoutes(router *gin.Engine)
}
