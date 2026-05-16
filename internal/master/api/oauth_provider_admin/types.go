package oauth_provider_admin

import "github.com/VaalaCat/ai-gateway/internal/master/api"

type CreateRequest struct {
	Name                  string `json:"name" binding:"required,max=32"`
	Protocol              string `json:"protocol" binding:"max=16"`
	DisplayName           string `json:"display_name" binding:"max=64"`
	Issuer                string `json:"issuer" binding:"max=255"`
	AuthorizationEndpoint string `json:"authorization_endpoint" binding:"required,max=512"`
	TokenEndpoint         string `json:"token_endpoint" binding:"required,max=512"`
	UserinfoEndpoint      string `json:"userinfo_endpoint" binding:"required,max=512"`
	JWKSURI               string `json:"jwks_uri" binding:"max=512"`
	ClientID              string `json:"client_id" binding:"required,max=255"`
	ClientSecret          string `json:"client_secret" binding:"required,max=255"`
	Scopes                string `json:"scopes" binding:"max=255"`
	IconURL               string `json:"icon_url" binding:"max=512"`
	Enabled               bool   `json:"enabled"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) { r.Fields = fields }

type GetRequest = api.IDPathRequest
type DeleteRequest = api.IDPathRequest
