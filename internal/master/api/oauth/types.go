package oauth

const (
	ErrUnknownProvider       = "unknown_provider"
	ErrProviderMisconfigured = "provider_misconfigured"
	ErrInvalidState          = "invalid_state"
	ErrIdPError              = "idp_error"
	ErrTokenExchangeFailed   = "token_exchange_failed"
	ErrUserinfoFailed        = "userinfo_failed"
	ErrMissingSub            = "missing_sub"
	ErrIDTokenInvalid        = "id_token_invalid"
	ErrMappingFailed         = "mapping_failed"
	ErrAlreadyLinked         = "already_linked"
	ErrMissingTicket         = "missing_ticket"
	ErrUntrustedOrigin       = "untrusted_origin"
)

const (
	cookieState   = "oauth_state"
	cookieMaxAge  = 300 // 5min
	stateTTL      = 300
	bindTicketTTL = 120 // 2min
	linkTicketTTL = 120
)

// PublicProvider 是 GET /api/oauth/providers 返回给登录页用的轻量 DTO。
type PublicProvider struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	IconURL     string `json:"icon_url"`
}
