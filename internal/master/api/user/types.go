package user

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type Handler struct {
	Bus app.EventBus
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Token string `json:"token"`
}

type ListRequest struct {
	api.PaginationQuery
	Search  string `form:"search"`
	Role    string `form:"role"`
	GroupID string `form:"group_id"`
}

type CreateRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Role     int    `json:"role"`
	GroupID  *uint  `json:"group_id"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

type UpdateQuotaRequest struct {
	ID    string `uri:"id" binding:"required"`
	Delta *int64 `json:"delta" binding:"required"`
}
