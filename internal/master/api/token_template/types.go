package token_template

import "github.com/VaalaCat/ai-gateway/internal/master/api"

type Handler struct{}

type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
	Status string `form:"status"`
}

type CreateRequest struct {
	Name              string  `json:"name" binding:"required"`
	Models            string  `json:"models"`
	ExpiryDays        int     `json:"expiry_days"`
	Status            int     `json:"status"`
	AllowedChannelIDs *[]uint `json:"allowed_channel_ids"`
	AllowedGroupIDs   *[]uint `json:"allowed_group_ids"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

type PreviewItem struct {
	TokenID        uint   `json:"token_id"`
	TokenName      string `json:"token_name"`
	ModelsBefore   string `json:"models_before"`
	ModelsAfter    string `json:"models_after"`
	ChannelsBefore []uint `json:"channels_before"`
	ChannelsAfter  []uint `json:"channels_after"`
}

type PreviewResponse struct {
	TemplateID   uint          `json:"template_id"`
	TemplateName string        `json:"template_name"`
	Total        int           `json:"total"`
	Changed      int           `json:"changed"`
	Unchanged    int           `json:"unchanged"`
	Items        []PreviewItem `json:"items"`
}

type SyncResponse struct {
	TemplateID       uint `json:"template_id"`
	Synced           int  `json:"synced"`
	SkippedUnchanged int  `json:"skipped_unchanged"`
}
