package script

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type Handler struct{}

type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
}

type CreateRequest struct {
	Name     string             `json:"name" binding:"required"`
	Code     string             `json:"code" binding:"required"`
	Enabled  *bool              `json:"enabled"`
	Priority int                `json:"priority"`
	Scope    models.ScriptScope `json:"scope"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) { r.Fields = fields }
