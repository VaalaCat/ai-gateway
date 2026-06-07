package consts

// HTTP 错误响应消息常量。
const (
	ErrMissingToken       = "missing token"
	ErrInvalidToken       = "invalid token"
	ErrAdminOnly          = "admin only"
	ErrNotFound           = "not found"
	ErrMissingAPIKey      = "missing api key"
	ErrInvalidAPIKey      = "invalid api key"
	ErrTokenDisabled      = "token disabled"
	ErrTokenExpired       = "token expired"
	ErrModelNotAllowed    = "model not allowed"
	ErrReadRequestBody    = "failed to read request body"
	ErrInvalidRequestBody = "invalid request body"
	ErrModelRequired      = "model is required"
	ErrNoChannelAvailable = "no available channels"
	ErrInsufficientQuota  = "insufficient quota"
)
