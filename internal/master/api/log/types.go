package log

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

type Handler struct{}

type ListRequest struct {
	api.PaginationQuery
	UserID    string `form:"user_id"`
	TokenID   string `form:"token_id"`
	ChannelID string `form:"channel_id"`
	ModelName string `form:"model_name"`
	Status    string `form:"status"`
}
