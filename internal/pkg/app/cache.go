package app

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// Store 本地配置缓存
// Agent 端用于缓存从 Master 同步的 Token、Channel、Model 等配置数据，
// 支持增量更新和全量加载
type Store interface {
	// --- Token 操作 ---
	GetToken(ctx context.Context, key string) *models.Token
	SetToken(token *models.Token)
	DeleteToken(key string)
	GetTokenByID(ctx context.Context, id uint) *models.Token
	DeleteTokenByID(id uint)
	LoadTokens(tokens []models.Token)
	TokenCount() int

	// --- Channel 操作 ---
	GetChannel(id uint) *models.Channel
	SetChannel(ch *models.Channel)
	DeleteChannel(id uint)
	LoadChannels(channels []models.Channel)
	ChannelCount() int

	// --- ModelConfig 操作 ---
	GetModelConfig(modelName string) *models.ModelConfig
	SetModelConfig(mc *models.ModelConfig)
	DeleteModelConfig(modelName string)
	LoadModelConfigs(configs []models.ModelConfig)
	ModelConfigCount() int

	// --- 模型路由 ---
	GetChannelsForModel(model string) []*models.Channel
	RebuildModelIndex()
	GetAllModelNames() []string

	// --- BYOK 私有 channel ---
	GetVisiblePrivateChannelsForUser(userID uint, model string) []*protocol.SyncedPrivateChannel
	ListVisibleBYOKModelNamesForUser(userID uint) []string // 列举 user 全部 enabled BYOK channel 的 Models 并集（去重）

	// --- Agent 操作 ---
	GetAgent(agentID string) *models.Agent
	SetAgent(agent *models.Agent)
	UpdateAgentAutoAddresses(agentID string, addrs []agentproxy.Address)
	DeleteAgent(agentID string)
	GetAgentsByTag(tag string) []*models.Agent
	GetAllAgents() []*models.Agent
	LoadAgents(agents []models.Agent)
	AgentCount() int

	// --- 版本与设置 ---
	Version() int64
	SetVersion(v int64)
	LoadSettings(s []models.Setting)
	Settings() settings.AgentSettings
	TraceMaxBodySize() int
	FallbackSleepMs() int

	// --- 系统 Token ---
	GetSystemTestToken() *models.Token

	// --- 事件处理 ---
	HandleSyncEvent(entity, action string, data []byte)
}

// Syncer 缓存同步器
// 负责与 Master 建立连接，拉取全量配置并订阅增量事件，保持本地缓存与 Master 一致
type Syncer interface {
	SetClient(client WSClient)
	FullSync(ctx context.Context) error
	SubscribeEvents()
	StartPeriodicCheck(ctx context.Context)
}

// WSBridge WebSocket 同步桥
// 监听 WebSocket 连接上的消息，将 Master 推送的增量更新转发给 Store 处理
type WSBridge interface {
	Start()
}
