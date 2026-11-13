package upstream

import (
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"golang.org/x/net/http/httpguts"
)

// ChannelAuthError 描述本次渠道认证配置或密钥错误。
type ChannelAuthError struct {
	Code    string
	Message string
}

// Error 返回不含认证值的错误文本。
func (err *ChannelAuthError) Error() string {
	// 1. 返回认证错误文本。
	return err.Message
}

// ApplyChannelAuth 按最终出站协议选择认证配置，展开渠道密钥并设置请求头。
func ApplyChannelAuth(request *http.Request, channel *models.Channel, outboundProtocol llmkit.Protocol) error {
	// 1. 校验渠道配置并选择整组协议认证。
	if channel == nil {
		return nil
	}
	auth := channel.Auth.Data()
	if err := models.ValidateChannelAuth(auth); err != nil {
		return &ChannelAuthError{Code: "invalid_upstream_auth", Message: err.Error()}
	}
	protocolKey := ""
	switch outboundProtocol {
	case llmkit.ProtocolOpenAIChat:
		protocolKey = "chat_completions"
	case llmkit.ProtocolOpenAIResponses:
		protocolKey = "responses"
	case llmkit.ProtocolClaudeMessages:
		protocolKey = "messages"
	case llmkit.ProtocolGemini:
		protocolKey = "generate_content"
	}
	selectedAuth := auth.Default
	if protocolAuth, ok := auth.Protocols[protocolKey]; ok {
		selectedAuth = &protocolAuth
	}
	if selectedAuth == nil {
		return nil
	}

	// 2. 使用本次渠道密钥展开认证值并校验结果。
	if strings.Contains(selectedAuth.Value, "${CHANNEL_KEY}") && channel.Key == "" {
		return &ChannelAuthError{Code: "upstream_auth_key_missing", Message: "upstream auth requires a channel key"}
	}
	expandedAuth := *selectedAuth
	expandedAuth.Value = strings.ReplaceAll(expandedAuth.Value, "${CHANNEL_KEY}", channel.Key)
	if expandedAuth.Value == "" || !httpguts.ValidHeaderFieldValue(expandedAuth.Value) {
		return &ChannelAuthError{Code: "invalid_upstream_auth", Message: "upstream auth value is invalid"}
	}

	// 3. 清除已有认证头并写入自定义认证头。
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	for name := range request.Header {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "X-Api-Key") || strings.EqualFold(name, "X-Goog-Api-Key") || strings.EqualFold(name, expandedAuth.Header) {
			delete(request.Header, name)
		}
	}
	request.Header.Set(expandedAuth.Header, expandedAuth.Value)
	return nil
}
