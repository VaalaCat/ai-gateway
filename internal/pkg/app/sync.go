package app

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
)

// AgentRuntime Agent 运行时信息
// 包含 Agent 的缓存状态、连接数、版本等运行时数据
type AgentRuntime struct {
	Uptime            int64 `json:"uptime"`
	CachedTokens      int   `json:"cached_tokens"`
	CachedChannels    int   `json:"cached_channels"`
	CachedModels      int   `json:"cached_models"`
	ActiveConnections int   `json:"active_connections"`
	Version           int64 `json:"version"`
	MasterVersion     int64 `json:"master_version"`
}

// Hub WebSocket 连接管理中心
// Master 端用于管理所有 Agent 的 WebSocket 连接，支持 RPC 调用、广播和在线状态查询
type Hub interface {
	// HandleWS 处理 Agent 的 WebSocket 连接请求
	HandleWS(c *gin.Context)
	// Call 向指定 Agent 发起 RPC 调用并等待响应
	Call(agentID string, method string, params any, timeout time.Duration) (json.RawMessage, error)
	// Broadcast 向所有在线 Agent 广播通知
	Broadcast(method string, params any)
	// GetOnlineAgentIDs 获取所有在线 Agent 的 ID 列表
	GetOnlineAgentIDs() []string
	// GetRuntime 获取指定 Agent 的运行时信息（心跳、延迟等）
	GetRuntime(agentID string) *AgentRuntime
	// IsOnline 检查指定 Agent 是否在线
	IsOnline(agentID string) bool
	// GetAgentAddresses 获取 Agent 的合并地址列表（配置地址 + 自动探测地址）
	GetAgentAddresses(agentID string, dbHTTPAddrs string) []AgentAddress
	// ConnectedAgents 返回当前在线的 Agent 数量
	ConnectedAgents() int
}

// Publisher 配置变更推送器
// 监听事件总线上的配置变更事件，通过 Hub 将增量更新推送给所有在线 Agent
type Publisher interface {
	// Start 启动推送监听
	Start()
}
