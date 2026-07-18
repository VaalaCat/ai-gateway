package model_routing

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) TokenList(c *app.Context, req TokenRoutingListRequest) (api.PaginatedResponse[models.ModelRouting], error) {
	tokenID, _, err := loadRoutingToken(c, req.TokenID)
	if err != nil {
		return api.PaginatedResponse[models.ModelRouting]{}, err
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	routings, total, listErr := q.ModelRouting().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.ModelRoutingListFilter{Scope: models.RoutingScopeToken, TokenID: &tokenID, Q: req.Q},
	)
	if listErr != nil {
		return api.PaginatedResponse[models.ModelRouting]{}, api.InternalError("list token model routings", listErr)
	}
	return api.PaginatedResponse[models.ModelRouting]{Data: routings, Total: total, Page: page, PageSize: pageSize}, nil
}

func (h *Handler) TokenCreate(c *app.Context, req TokenRoutingCreateRequest) (models.ModelRouting, error) {
	tokenID, _, err := loadRoutingToken(c, req.TokenID)
	if err != nil {
		return models.ModelRouting{}, err
	}
	if req.BodyScope != nil || req.BodyUserID != nil || req.BodyTokenID != nil {
		return models.ModelRouting{}, tokenOwnerFieldError()
	}
	members, marshalErr := json.Marshal(req.Members)
	if marshalErr != nil {
		return models.ModelRouting{}, api.BadRequestError("invalid members", marshalErr)
	}
	return h.createRouting(c, &models.ModelRouting{
		Name: req.Name, Scope: models.RoutingScopeToken, TokenID: tokenID,
		Members: string(members), Enabled: req.Enabled, Remark: req.Remark,
	})
}

func (h *Handler) TokenGet(c *app.Context, req TokenRoutingPathRequest) (GetResponse, error) {
	tokenID, _, err := loadRoutingToken(c, req.TokenID)
	if err != nil {
		return GetResponse{}, err
	}
	routing, err := loadTokenRouting(c, tokenID, req.ID)
	if err != nil {
		return GetResponse{}, err
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	return GetResponse{ModelRouting: *routing, ExpandedModels: expandRoutingToModels(routing, q)}, nil
}

func (h *Handler) TokenUpdate(c *app.Context, req TokenRoutingUpdateRequest) (models.ModelRouting, error) {
	tokenID, _, err := loadRoutingToken(c, req.TokenID)
	if err != nil {
		return models.ModelRouting{}, err
	}
	routing, err := loadTokenRouting(c, tokenID, req.ID)
	if err != nil {
		return models.ModelRouting{}, err
	}
	for _, field := range []string{"id", "scope", "user_id", "token_id"} {
		if _, exists := req.Fields[field]; exists {
			return models.ModelRouting{}, tokenOwnerFieldError()
		}
	}
	return h.updateRouting(c, routing.ID, req.Fields)
}

func (h *Handler) TokenDelete(c *app.Context, req TokenRoutingPathRequest) (api.StatusResponse, error) {
	tokenID, _, err := loadRoutingToken(c, req.TokenID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	routing, err := loadTokenRouting(c, tokenID, req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	return h.deleteRouting(c, routing)
}

func (h *Handler) TokenPreview(c *app.Context, req TokenRoutingPreviewRequest) (PreviewResponse, error) {
	if _, _, err := loadRoutingToken(c, req.TokenID); err != nil {
		return PreviewResponse{}, err
	}
	if req.BodyScope != nil || req.BodyUserID != nil || req.BodyTokenID != nil {
		return PreviewResponse{}, tokenOwnerFieldError()
	}
	req.PreviewRequest.SelfScope = models.RoutingScopeToken
	req.PreviewRequest.SelfUserID = 0
	return h.Preview(c, req.PreviewRequest)
}

func loadRoutingToken(c *app.Context, rawTokenID string) (uint, *models.Token, error) {
	parsed, err := strconv.ParseUint(rawTokenID, 10, 64)
	if err != nil || parsed == 0 {
		return 0, nil, api.NotFoundError(consts.ErrNotFound)
	}
	tokenID := uint(parsed)
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	token, loadErr := q.Token().GetByID(tokenID)
	if loadErr != nil || token == nil {
		return 0, nil, api.NotFoundError(consts.ErrNotFound)
	}
	scope := middleware.GetScope(c.Context)
	if scope == nil {
		return 0, nil, api.UnauthorizedError("not authenticated")
	}
	if !scope.IsAdmin && scope.UserID != token.UserID {
		return 0, nil, api.NotFoundError(consts.ErrNotFound)
	}
	return tokenID, token, nil
}

func loadTokenRouting(c *app.Context, tokenID uint, rawRoutingID string) (*models.ModelRouting, error) {
	parsed, err := strconv.ParseUint(rawRoutingID, 10, 64)
	if err != nil || parsed == 0 {
		return nil, tokenRoutingNotFoundError()
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	routing, loadErr := q.ModelRouting().GetByID(uint(parsed))
	if loadErr != nil || routing == nil || routing.Scope != models.RoutingScopeToken || routing.TokenID != tokenID {
		return nil, tokenRoutingNotFoundError()
	}
	return routing, nil
}

func tokenRoutingNotFoundError() error {
	return api.ErrorWithCode(http.StatusNotFound, "token_routing_not_found", "token model routing not found", nil)
}

func tokenOwnerFieldError() error {
	return api.ErrorWithCode(http.StatusBadRequest, dao.ErrCodeInvalidScopeOwner, "routing owner is defined by the token path", nil)
}
