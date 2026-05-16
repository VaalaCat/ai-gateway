package oauth

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type Handler struct {
	Bus        app.EventBus
	App        app.Application
	JWTSecret  string
	Allowlist  *Allowlist
	StateStore *StateStore
	IDP        *IDPRouter
	Verifier   *IDTokenVerifier
}

func NewHandler(application app.Application, bus app.EventBus, jwtSecret string, allowlist *Allowlist) *Handler {
	httpClient := http.DefaultClient
	return &Handler{
		Bus:        bus,
		App:        application,
		JWTSecret:  jwtSecret,
		Allowlist:  allowlist,
		StateStore: NewStateStore(),
		IDP: &IDPRouter{
			OIDC:   NewOIDCAdapter(httpClient),
			Feishu: NewFeishuAdapter(httpClient),
		},
		Verifier: NewIDTokenVerifier(),
	}
}
