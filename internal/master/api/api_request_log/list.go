package api_request_log

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.APIRequestLog], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	filter, err := listFilter(req)
	if err != nil {
		return api.PaginatedResponse[models.APIRequestLog]{}, err
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRequestLog()
	rows, total, err := query.List(dao.ListOptions{Page: page, PageSize: pageSize}, filter)
	if err != nil {
		if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
			return api.PaginatedResponse[models.APIRequestLog]{}, logDatabaseUnavailableError()
		}
		return api.PaginatedResponse[models.APIRequestLog]{}, api.InternalError("list API request logs failed", err)
	}
	return api.PaginatedResponse[models.APIRequestLog]{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func (h *Handler) PortalList(c *app.Context, req PortalListRequest) (api.PaginatedResponse[PortalLogResponse], error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil || scope.UserID == 0 {
		return api.PaginatedResponse[PortalLogResponse]{}, api.UnauthorizedError("not authenticated")
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	filter, err := portalListFilter(req, scope.UserID)
	if err != nil {
		return api.PaginatedResponse[PortalLogResponse]{}, err
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRequestLog()
	rows, total, err := query.List(dao.ListOptions{Page: page, PageSize: pageSize}, filter)
	if err != nil {
		if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
			return api.PaginatedResponse[PortalLogResponse]{}, logDatabaseUnavailableError()
		}
		return api.PaginatedResponse[PortalLogResponse]{}, api.InternalError("list API request logs failed", err)
	}
	data := make([]PortalLogResponse, len(rows))
	for i := range rows {
		data[i] = newPortalLogResponse(rows[i])
	}
	return api.PaginatedResponse[PortalLogResponse]{Data: data, Total: total, Page: page, PageSize: pageSize}, nil
}

func portalListFilter(req PortalListRequest, userID uint) (dao.APIRequestLogFilter, error) {
	filter, err := listFilter(ListRequest{
		PaginationQuery: req.PaginationQuery, TimeWindowQuery: req.TimeWindowQuery,
		TokenID: req.TokenID, StatusCode: req.StatusCode, RequestID: req.RequestID,
	})
	if err != nil {
		return dao.APIRequestLogFilter{}, err
	}
	filter.UserID = &userID
	return filter, nil
}

func listFilter(req ListRequest) (dao.APIRequestLogFilter, error) {
	window := req.TimeWindowQuery.ToTimeWindow()
	if err := window.Validate(maxListDays); err != nil {
		return dao.APIRequestLogFilter{}, api.BadRequestError("range out of bounds", err)
	}
	filter := dao.APIRequestLogFilter{
		TimeWindow: window, APIServiceID: req.APIServiceID, APIRouteID: req.APIRouteID,
		APIUpstreamID: req.APIUpstreamID, RequestID: req.RequestID,
	}
	if req.TokenID != "" {
		value, err := parsePositiveUint(req.TokenID, "token_id")
		if err != nil {
			return dao.APIRequestLogFilter{}, err
		}
		filter.TokenID = &value
	}
	if req.StatusCode != "" {
		value, err := strconv.Atoi(req.StatusCode)
		if err != nil || (value != 0 && (value < 100 || value > 999)) {
			return dao.APIRequestLogFilter{}, api.BadRequestError("invalid status_code", err)
		}
		filter.StatusCode = &value
	}
	return filter, nil
}

func parsePositiveUint(raw, field string) (uint, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, api.BadRequestError("invalid "+field, err)
	}
	return uint(value), nil
}

func logDatabaseUnavailableError() error {
	return api.ErrorWithCode(http.StatusServiceUnavailable, "LogDatabaseUnavailable", "log database is temporarily unavailable", nil)
}
