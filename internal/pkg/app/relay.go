package app

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
)

// RelayHandler 请求中继处理器
// Agent 端的核心组件，负责接收客户端 API 请求，通过 Codec 转换协议后转发到上游服务商
type RelayHandler interface {
	Relay(c *gin.Context)
}

// AgentAddress is the shared address value used at application boundaries.
type AgentAddress = protocol.Address

// AgentSelector identifies exactly one agent or one agent tag.
type AgentSelector struct {
	AgentID  string
	AgentTag string
}
