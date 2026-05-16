package model_routing

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

type ListRequest struct {
	api.PaginationQuery
	Scope  string `form:"scope"`
	UserID *uint  `form:"user_id"`
	Q      string `form:"q"`
}

type CreateRequest struct {
	Name    string        `json:"name" binding:"required"`
	Scope   string        `json:"scope" binding:"required"`
	UserID  uint          `json:"user_id"`
	Members []MemberInput `json:"members" binding:"required"`
	Enabled bool          `json:"enabled"`
	Remark  string        `json:"remark"`
}

type MemberInput struct {
	Ref      string `json:"ref"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fs map[string]any) { r.Fields = fs }

// CandidatesResponse 是 admin 端 GET /admin/model-routings/candidates 的响应。
// user portal 改用 RoutingNamesResp（见 PortalGlobalRoutingNames）。
type CandidatesResponse struct {
	Models         []string `json:"models"`
	GlobalRoutings []string `json:"global_routings"`
}

type PreviewRequest struct {
	Members    []MemberInput `json:"members" binding:"required"`
	SelfName   string        `json:"self_name"`
	SelfScope  string        `json:"self_scope"`
	SelfUserID uint          `json:"self_user_id"`
}

// RoutingNamesResp 是 PortalGlobalRoutingNames 的响应：仅含 enabled global routing 名称（升序）。
type RoutingNamesResp struct {
	Names []string `json:"names"`
}
