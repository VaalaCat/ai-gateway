package oauth

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

// 支持的 OAuth/OIDC protocol 取值，OAuthProvider.Protocol 字段与
// create.go 校验、IDPRouter 分发均以此为准。
const (
	ProtocolOIDC   = "oidc"
	ProtocolFeishu = "feishu"
)

// TokenResponse 是 IDPAdapter.ExchangeCode 的规范化返回。
// 不同 IdP 的原始字段被各自的实现拍扁到这套通用字段上。
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

// IDPAdapter 抽象不同 OAuth/OIDC IdP 的两次出站调用。
// 实现需要从 *models.OAuthProvider 取所需 endpoint / client 凭据，
// 并把响应规范化为通用 TokenResponse / UserinfoPayload。
type IDPAdapter interface {
	ExchangeCode(ctx context.Context, p *models.OAuthProvider, code, redirectURI string) (*TokenResponse, error)
	FetchUserinfo(ctx context.Context, p *models.OAuthProvider, accessToken string) (*UserinfoPayload, error)
}

// IDPRouter 按 p.Protocol 分发到具体适配器。未知 / 空值回落到 OIDC；
// 对应分支的 adapter 为 nil（中间状态：Task 2 接 Feishu 前）也回落到 OIDC，
// 避免 callback 在调用 nil.ExchangeCode 时 panic。
type IDPRouter struct {
	OIDC   IDPAdapter
	Feishu IDPAdapter
}

func (r *IDPRouter) For(p *models.OAuthProvider) IDPAdapter {
	switch p.Protocol {
	case ProtocolFeishu:
		if r.Feishu == nil {
			return r.OIDC
		}
		return r.Feishu
	default:
		return r.OIDC
	}
}
