package private_channel

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
)

// Handler is the BYOK portal/admin private-channel handler.
// Constructed once at server startup; safe for concurrent use.
//
// App 提供 DB / EventBus 等通用容器依赖；Provider 单独承载 BYOK 关注点，
// 避免把 GetBYOKCipher 污染到 app.Application 顶层接口。
type Handler struct {
	App      app.Application
	Provider byokcrypto.Provider
}

// NewHandler wires the BYOK cipher provider explicitly. master 在启动时
// 应构造一次 byokcrypto.NewStaticProvider(cipher) 并复用。
func NewHandler(application app.Application, provider byokcrypto.Provider) *Handler {
	return &Handler{App: application, Provider: provider}
}
