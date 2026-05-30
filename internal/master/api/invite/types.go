package invite

import "github.com/VaalaCat/ai-gateway/internal/master/api"

type Handler struct{}

type CreateRequest struct {
	Note      string `json:"note"`
	MaxUses   int    `json:"max_uses"`
	ExpiresAt int64  `json:"expires_at"`
}

type ListRequest struct {
	api.PaginationQuery
	Search    string `form:"search"`     // code 模糊搜索
	CreatorID string `form:"creator_id"` // 仅管理员列表使用,按建码人过滤
}
