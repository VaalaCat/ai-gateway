package model_routing

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.ModelRouting], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	// 未传 user_id 且未传 scope：返回 global 全部 + 当前 admin 自己的 user-scope
	// 因为 DAO 单层 filter 无法表达 OR 语义，此处合并两次查询。
	if req.UserID == nil && req.Scope == "" {
		if c.UserInfo == nil {
			return api.PaginatedResponse[models.ModelRouting]{}, api.InternalError("user info missing", nil)
		}
		adminID := c.UserInfo.UserID

		globalRs, totalG, err := q.ModelRouting().List(
			dao.ListOptions{Page: 1, PageSize: 10000},
			dao.ModelRoutingListFilter{Scope: models.RoutingScopeGlobal, Q: req.Q},
		)
		if err != nil {
			return api.PaginatedResponse[models.ModelRouting]{}, api.InternalError("list global routings", err)
		}

		userRs, totalU, err := q.ModelRouting().List(
			dao.ListOptions{Page: 1, PageSize: 10000},
			dao.ModelRoutingListFilter{Scope: models.RoutingScopeUser, UserID: &adminID, Q: req.Q},
		)
		if err != nil {
			return api.PaginatedResponse[models.ModelRouting]{}, api.InternalError("list user routings", err)
		}

		merged := append(globalRs, userRs...)
		total := totalG + totalU

		// 合并后做内存分页
		offset := (page - 1) * pageSize
		if offset >= len(merged) {
			merged = nil
		} else {
			end := offset + pageSize
			if end > len(merged) {
				end = len(merged)
			}
			merged = merged[offset:end]
		}
		return api.PaginatedResponse[models.ModelRouting]{Data: merged, Total: total, Page: page, PageSize: pageSize}, nil
	}

	// 显式传了 scope 或 user_id：直接按 filter 查询
	filter := dao.ModelRoutingListFilter{Scope: req.Scope, Q: req.Q, UserID: req.UserID}
	rs, total, err := q.ModelRouting().List(dao.ListOptions{Page: page, PageSize: pageSize}, filter)
	if err != nil {
		return api.PaginatedResponse[models.ModelRouting]{}, api.InternalError("list model routings", err)
	}
	return api.PaginatedResponse[models.ModelRouting]{Data: rs, Total: total, Page: page, PageSize: pageSize}, nil
}
