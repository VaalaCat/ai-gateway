package app

import (
	"context"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// AgentApplication 是 agent 端专用服务容器，跟 Application 并列存在于 RelayContext。
// Relay pipeline 通过此接口拿到 cache / logger / config / transport pool，
// 避免把整个 *Handler 当参数到处传，方便测试时 stub 单个依赖。
type AgentApplication interface {
	GetCache() AgentCache
	GetBodyStore() BodyStore
	GetLogger() *zap.Logger
	GetConfig() *config.AgentRuntimeConfig
	GetTransportPool() TransportPool
	RelayTimeout() time.Duration // 非流式请求的总超时；0 表示不限
}

type BodyLimits struct {
	MemoryThreshold int64
	HardLimit       int64
}

type ReplayBody interface {
	Size() int64
	Open() (io.ReadCloser, error)
	Bytes(limit int64) ([]byte, error)
	Close() error
}

type BodyStore interface {
	Capture(ctx context.Context, src io.Reader, limits BodyLimits) (ReplayBody, error)
}

// AgentCache 在 Store 基础上加 relay 需要的 route 查询能力。
// 嵌入 Store 是为了让 relay 路径既能查 Token / Channel / ModelConfig，
// 也能查 AgentRoute，不必持有两个对象。
type AgentCache interface {
	Store
	FindTokenRoute(tokenID uint, realModel string) *models.AgentRoute
	FindAdminChannelRoute(channelID uint, realModel string) *models.AgentRoute
	EffectiveRequestLimiters(userID, groupID uint) []*models.RequestLimiter
	EffectiveAttemptLimiters(userID, groupID uint, src string, channelID uint) []*models.RequestLimiter
}

type RoutePath string

const (
	RoutePathDirect RoutePath = "direct"
	RoutePathRelay  RoutePath = "relay"
	RoutePathLocal  RoutePath = "local"
)

// TransportPool 抽象 channel → *http.Transport 缓存能力。
// 让 relay 在多次 upstream 请求间共享连接池，避免每次 new(http.Transport)。
//
// Invalidate 用于 channel.ProxyURL 变更时让旧 transport 失效；
// server.go 在装配阶段通过 Store.OnChannelChange 回调调用。
type TransportPool interface {
	Get(ch *models.Channel) *http.Transport
	Invalidate(channelID uint, oldProxyURL string)
	CloseIdleConnections()
}
