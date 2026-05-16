package models

type OAuthProvider struct {
	ID                    uint   `gorm:"primaryKey" json:"id"`
	Name                  string `gorm:"uniqueIndex;size:32" json:"name"`
	DisplayName           string `gorm:"size:64" json:"display_name"`
	Issuer                string `gorm:"size:255" json:"issuer"`
	AuthorizationEndpoint string `gorm:"size:512" json:"authorization_endpoint"`
	TokenEndpoint         string `gorm:"size:512" json:"token_endpoint"`
	UserinfoEndpoint      string `gorm:"size:512" json:"userinfo_endpoint"`
	JWKSURI               string `gorm:"size:512" json:"jwks_uri"`
	ClientID              string `gorm:"size:255" json:"client_id"`
	ClientSecret          string `gorm:"size:255" json:"client_secret,omitempty"`
	Scopes                string `gorm:"size:255" json:"scopes"`
	IconURL               string `gorm:"size:512" json:"icon_url"`
	Enabled               bool   `gorm:"default:true" json:"enabled"`
	Protocol              string `gorm:"size:16;default:oidc;not null" json:"protocol"`
	CreatedAt             int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt             int64  `gorm:"autoUpdateTime" json:"updated_at"`
}
