package billing

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

func (h *Handler) ListTokens(c *app.Context, req ListTokensRequest) (api.PaginatedResponse[dao.TokenBillingListItem], error) {
	scope := middleware.GetScope(c.Context)

	reqUserID, err := parseOptionalUint(req.UserID)
	if err != nil {
		return api.PaginatedResponse[dao.TokenBillingListItem]{}, api.BadRequestError("invalid user_id", err)
	}
	tokenID, err := parseOptionalUint(req.TokenID)
	if err != nil {
		return api.PaginatedResponse[dao.TokenBillingListItem]{}, api.BadRequestError("invalid token_id", err)
	}
	startDate, endDate, err := normalizeDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return api.PaginatedResponse[dao.TokenBillingListItem]{}, api.BadRequestError("invalid date range", err)
	}

	filterUserID := middleware.ScopedUserID(scope, reqUserID)
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	items, total, err := q.Billing().ListTokenBilling(dao.ListOptions{Page: page, PageSize: pageSize}, dao.TokenBillingListFilter{
		UserID:    filterUserID,
		TokenID:   tokenID,
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		return api.PaginatedResponse[dao.TokenBillingListItem]{}, api.InternalError("list token billing failed", err)
	}

	return api.PaginatedResponse[dao.TokenBillingListItem]{
		Data:     items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (h *Handler) TokenDaily(c *app.Context, req TokenDailyRequest) (TokenDailyResponse, error) {
	scope := middleware.GetScope(c.Context)

	tokenID, err := parseRequiredUint(req.TokenID)
	if err != nil {
		return TokenDailyResponse{}, api.BadRequestError("invalid token_id", err)
	}
	reqUserID, err := parseOptionalUint(req.UserID)
	if err != nil {
		return TokenDailyResponse{}, api.BadRequestError("invalid user_id", err)
	}
	startDate, endDate, err := normalizeDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return TokenDailyResponse{}, api.BadRequestError("invalid date range", err)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	token, err := q.Token().GetByID(tokenID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return TokenDailyResponse{}, api.NotFoundError("token not found")
		}
		return TokenDailyResponse{}, api.InternalError("get token failed", err)
	}
	if !middleware.EnsureOwnership(c.Context, scope, token.UserID) {
		return TokenDailyResponse{}, nil
	}

	filterUserID := middleware.ScopedUserID(scope, reqUserID)
	items, err := q.Billing().GetTokenDaily(tokenID, dao.TokenBillingListFilter{
		UserID:    filterUserID,
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		return TokenDailyResponse{}, api.InternalError("get token billing trend failed", err)
	}
	if items == nil {
		items = []dao.TokenBillingDailyItem{}
	}
	return TokenDailyResponse{Items: items}, nil
}
