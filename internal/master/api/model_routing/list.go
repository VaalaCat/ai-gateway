package model_routing

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.ModelRouting], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	filter, err := buildAdminListFilter(req)
	if err != nil {
		return api.PaginatedResponse[models.ModelRouting]{}, err
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	rs, total, err := q.ModelRouting().List(dao.ListOptions{Page: page, PageSize: pageSize}, filter)
	if err != nil {
		return api.PaginatedResponse[models.ModelRouting]{}, api.InternalError("list model routings", err)
	}
	return api.PaginatedResponse[models.ModelRouting]{Data: rs, Total: total, Page: page, PageSize: pageSize}, nil
}

func buildAdminListFilter(req ListRequest) (dao.ModelRoutingListFilter, error) {
	filter := dao.ModelRoutingListFilter{
		Scope:   req.Scope,
		UserID:  req.UserID,
		TokenID: req.TokenID,
		Q:       req.Q,
	}

	switch req.Scope {
	case "":
		if req.UserID != nil || req.TokenID != nil {
			return dao.ModelRoutingListFilter{}, api.BadRequestError("scope is required with an owner filter", nil)
		}
	case models.RoutingScopeGlobal:
		if req.UserID != nil || req.TokenID != nil {
			return dao.ModelRoutingListFilter{}, api.BadRequestError("global scope cannot have an owner filter", nil)
		}
	case models.RoutingScopeUser:
		if req.TokenID != nil {
			return dao.ModelRoutingListFilter{}, api.BadRequestError("user scope cannot have a token owner", nil)
		}
	case models.RoutingScopeToken:
		if req.UserID != nil {
			return dao.ModelRoutingListFilter{}, api.BadRequestError("token scope cannot have a user owner", nil)
		}
	default:
		return dao.ModelRoutingListFilter{}, api.BadRequestError("invalid model routing scope", nil)
	}

	return filter, nil
}
