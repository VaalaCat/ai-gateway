package user_group

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
	Status string `form:"status"`
}

type CreateRequest struct {
	Name              string  `json:"name" binding:"required,max=64"`
	Description       string  `json:"description" binding:"max=255"`
	Status            int     `json:"status"`
	AllowedChannelIDs *[]uint `json:"allowed_channel_ids"`
	Models            string  `json:"models"`
	BYOKEnabled       *bool   `json:"byok_enabled"`
	BYOKMaxChannels   *int    `json:"byok_max_channels"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) { r.Fields = fields }

type GetRequest struct {
	ID string `uri:"id" binding:"required"`
}

type DeleteRequest struct {
	ID string `uri:"id" binding:"required"`
}

type Item struct {
	models.UserGroup
	UserCount int64 `json:"user_count"`
}
