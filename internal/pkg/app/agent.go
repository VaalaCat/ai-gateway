package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// AgentApplication 是 agent 端专用服务容器，跟 Application 并列存在于 RelayContext。
// Relay pipeline 通过此接口拿到 cache / forwarder / logger / config / transport pool，
// 避免把整个 *Handler 当参数到处传，方便测试时 stub 单个依赖。
type AgentApplication interface {
	GetCache() AgentCache
	GetRouteForwarder() RouteForwarder
	GetLogger() *zap.Logger
	GetConfig() *config.AgentRuntimeConfig
	GetTransportPool() TransportPool
}

// AgentCache 在 Store 基础上加 relay 需要的 route 查询能力。
// 嵌入 Store 是为了让 relay 路径既能查 Token / Channel / ModelConfig，
// 也能查 AgentRoute，不必持有两个对象。
type AgentCache interface {
	Store
	MatchRoute(tokenID uint, model string, channelIDs []uint) *models.AgentRoute
}

// RouteForwarder 抽象 agent → agent 转发能力。
// Relay 在解析到 AgentRoute 时调用 ForwardByRoute，把请求转给目标 agent。
type RouteForwarder interface {
	ForwardByRoute(c *gin.Context, route *models.AgentRoute) (forwarded bool, err error)
}

// TransportPool 抽象 channel → *http.Transport 缓存能力。
// 让 relay 在多次 upstream 请求间共享连接池，避免每次 new(http.Transport)。
//
// Invalidate 用于 channel.ProxyURL 变更时让旧 transport 失效；
// server.go 在装配阶段通过 Store.OnChannelChange 回调调用。
type TransportPool interface {
	Get(ch *models.Channel) *http.Transport
	Invalidate(channelID uint, oldProxyURL string)
}
