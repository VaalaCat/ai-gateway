package oauth_provider_admin

import "github.com/VaalaCat/ai-gateway/internal/pkg/app"

type Handler struct {
	Bus app.EventBus
}

const SecretMask = "***"
