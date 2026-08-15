package token

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type Handler struct {
	clock func() time.Time
}

type ListRequest struct {
	api.PaginationQuery
	Search       string `form:"search"`
	UserID       string `form:"user_id"`
	TokenID      *uint  `form:"token_id"`
	Status       string `form:"status"`
	UsableOnly   bool   `form:"usable_only"`
	APIRoleMode  string `form:"api_role_mode"`
	APIServiceID *uint  `form:"api_service_id"`
	APIRouteID   *uint  `form:"api_route_id"`
}

type CreateRequest struct {
	UserID            uint    `json:"user_id"`
	Name              string  `json:"name" binding:"required"`
	Key               string  `json:"key"`
	TemplateID        *uint   `json:"template_id"`
	ExpiredAt         int64   `json:"expired_at"`
	Models            string  `json:"models"`
	TraceEnabled      bool    `json:"trace_enabled"`
	TraceMode         string  `json:"trace_mode"`
	BYOKOnly          bool    `json:"byok_only"`
	AllowedChannelIDs *[]uint `json:"allowed_channel_ids"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

type APIRoleUpdateRequest struct {
	Mode    models.APIRoleMode `json:"api_role_mode"`
	RoleIDs []uint             `json:"api_role_ids"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

func GenerateKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "sk-" + hex.EncodeToString(b)
}
