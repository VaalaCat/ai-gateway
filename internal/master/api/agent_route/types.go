package agent_route

import "github.com/VaalaCat/ai-gateway/internal/master/api"

type Handler struct{}

type ListRequest struct {
	api.PaginationQuery
	SourceType string `form:"source_type"`
	SourceID   string `form:"source_id"`
}

type CreateRequest struct {
	SourceType string `json:"source_type" binding:"required"`
	SourceID   uint   `json:"source_id" binding:"required"`
	Model      string `json:"model"`
	AgentID    string `json:"agent_id"`
	AgentTag   string `json:"agent_tag"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

type OverviewItem struct {
	ID         uint   `json:"id"`
	SourceType string `json:"source_type"`
	SourceID   uint   `json:"source_id"`
	SourceName string `json:"source_name"`
	Model      string `json:"model"`
	AgentID    string `json:"agent_id"`
	AgentName  string `json:"agent_name"`
	AgentTag   string `json:"agent_tag"`
	Priority   int    `json:"priority"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}
