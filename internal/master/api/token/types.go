package token

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

type Handler struct{}

type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
	UserID string `form:"user_id"`
	Status string `form:"status"`
}

type CreateRequest struct {
	UserID            uint    `json:"user_id"`
	Name              string  `json:"name" binding:"required"`
	Key               string  `json:"key"`
	TemplateID        *uint   `json:"template_id"`
	ExpiredAt         int64   `json:"expired_at"`
	Models            string  `json:"models"`
	TraceEnabled      bool    `json:"trace_enabled"`
	AllowedChannelIDs *[]uint `json:"allowed_channel_ids"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

func GenerateKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "sk-" + hex.EncodeToString(b)
}
