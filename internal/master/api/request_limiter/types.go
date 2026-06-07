package request_limiter

import "github.com/VaalaCat/ai-gateway/internal/master/api"

type Handler struct{}

type ListRequest struct {
	api.PaginationQuery
}

// CreateRequest 是新建一条 limiter 策略的入参（绑定另走 binding 接口）。
type CreateRequest struct {
	Name         string `json:"name" binding:"required"`
	Enabled      bool   `json:"enabled"`
	Metric       string `json:"metric" binding:"required"`
	Capacity     int64  `json:"capacity"`
	WindowMs     int    `json:"window_ms"`
	KeyBy        string `json:"key_by" binding:"required"`
	ChannelScope string `json:"channel_scope"`
	Action       string `json:"action"`
	QueueSize    int    `json:"queue_size"`
	QueueTimeMs  int    `json:"queue_time_ms"`
	Priority     int    `json:"priority"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

// ListBindingsRequest 列出某 limiter 的绑定（limiter_id 必填）。
type ListBindingsRequest struct {
	LimiterID string `form:"limiter_id" binding:"required"`
}

// CreateBindingRequest 给某 limiter 加一条绑定。
type CreateBindingRequest struct {
	LimiterID  uint   `json:"limiter_id" binding:"required"`
	TargetType string `json:"target_type" binding:"required"`
	TargetID   uint   `json:"target_id"` // global 时为 0
	Enabled    bool   `json:"enabled"`
}
