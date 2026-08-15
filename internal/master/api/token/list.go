package token

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[ListResponse], error) {
	now := h.now()
	writeServerTimeHeader(c, now)
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	scope := middleware.GetScope(c.Context)

	var reqUserID *uint
	if req.UserID != "" {
		u, _ := strconv.ParseUint(req.UserID, 10, 64)
		uid := uint(u)
		reqUserID = &uid
	}

	userIDFilter := middleware.ScopedUserID(scope, reqUserID)

	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	var usableAt *int64
	if req.UsableOnly {
		seconds := now.Unix()
		usableAt = &seconds
	}
	invokeScope, err := listInvokeScope(req)
	if err != nil {
		return api.PaginatedResponse[ListResponse]{}, api.BadRequestError("invalid API invoke scope", err)
	}
	if invokeScope.enabled {
		seconds := now.Unix()
		usableAt = &seconds
	}
	apiRoleMode, err := listAPIRoleMode(req.APIRoleMode)
	if err != nil {
		return api.PaginatedResponse[ListResponse]{}, api.BadRequestError("invalid api_role_mode", err)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	listOptions := dao.ListOptions{Page: page, PageSize: pageSize}
	if invokeScope.enabled {
		listOptions = dao.ListOptions{Page: 1, PageSize: maxTokenListCandidates}
	}
	tokens, total, err := q.Token().List(
		listOptions,
		dao.TokenListFilter{Search: req.Search, ID: req.TokenID, UserID: userIDFilter, Status: statusFilter, UsableAt: usableAt, APIRoleMode: apiRoleMode},
	)
	if err != nil {
		return api.PaginatedResponse[ListResponse]{}, api.InternalError("list tokens failed", err)
	}
	if invokeScope.enabled {
		tokens, err = apirbac.NewTokenInvokeFinder(q).Filter(c.RequestContext(), tokens, invokeScope.serviceID, invokeScope.routeID)
		if err != nil {
			switch {
			case errors.Is(err, apirbac.ErrTokenInvokeRouteNotFound):
				return api.PaginatedResponse[ListResponse]{}, api.NotFoundError("API invoke route not found")
			case errors.Is(err, apirbac.ErrTokenInvokeRouteServiceMismatch):
				return api.PaginatedResponse[ListResponse]{}, api.BadRequestError("API invoke route does not belong to service", err)
			default:
				return api.PaginatedResponse[ListResponse]{}, api.InternalError("filter invokable tokens failed", err)
			}
		}
		total = int64(len(tokens))
		tokens = tokenListPage(tokens, page, pageSize)
	}
	owners, err := q.User().ListByIDs(tokenOwnerIDs(tokens))
	if err != nil {
		return api.PaginatedResponse[ListResponse]{}, api.InternalError("list token owners failed", err)
	}
	if missingOwnerID, missing := missingTokenOwnerID(tokens, owners); missing {
		return api.PaginatedResponse[ListResponse]{}, api.InternalError("token owner is missing", fmt.Errorf("user %d", missingOwnerID))
	}
	return api.PaginatedResponse[ListResponse]{
		Data:     projectListResponses(tokens, owners),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func tokenOwnerIDs(tokens []models.Token) []uint {
	ids := make([]uint, 0, len(tokens))
	for _, token := range tokens {
		if token.UserID != 0 {
			ids = append(ids, token.UserID)
		}
	}
	return ids
}

func listAPIRoleMode(raw string) (*models.APIRoleMode, error) {
	if raw == "" {
		return nil, nil
	}
	mode := models.APIRoleMode(raw)
	if mode != models.APIRoleModeInherit && mode != models.APIRoleModeExplicit {
		return nil, fmt.Errorf("api_role_mode must be inherit or explicit")
	}
	return &mode, nil
}

const maxTokenListCandidates = int(^uint(0) >> 1)

type tokenInvokeScope struct {
	enabled   bool
	serviceID uint
	routeID   uint
}

func listInvokeScope(req ListRequest) (tokenInvokeScope, error) {
	if req.APIServiceID == nil && req.APIRouteID == nil {
		return tokenInvokeScope{}, nil
	}
	if req.APIServiceID == nil || req.APIRouteID == nil || *req.APIServiceID == 0 || *req.APIRouteID == 0 {
		return tokenInvokeScope{}, fmt.Errorf("api_service_id and api_route_id must be provided together and be non-zero")
	}
	return tokenInvokeScope{enabled: true, serviceID: *req.APIServiceID, routeID: *req.APIRouteID}, nil
}

func tokenListPage(tokens []models.Token, page, pageSize int) []models.Token {
	pageCount := (len(tokens) + pageSize - 1) / pageSize
	if page > pageCount {
		return []models.Token{}
	}
	start := (page - 1) * pageSize
	end := min(start+pageSize, len(tokens))
	return tokens[start:end]
}
