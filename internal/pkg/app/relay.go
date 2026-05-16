package app

import "github.com/gin-gonic/gin"

// RelayHandler 请求中继处理器
// Agent 端的核心组件，负责接收客户端 API 请求，通过 Codec 转换协议后转发到上游服务商
type RelayHandler interface {
	Relay(c *gin.Context)
}
