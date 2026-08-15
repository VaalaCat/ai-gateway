package api_backend

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

type UpstreamPublisher interface {
	PublishUpstream(context.Context, string, models.APIUpstream) error
}

type Handler struct {
	App       app.Application
	Publisher UpstreamPublisher
}

type ListRequest struct {
	api.PaginationQuery
	APIServiceID *uint  `form:"api_service_id"`
	Search       string `form:"search"`
}

type CreateInput struct {
	Name string `json:"name" binding:"required,max=64"`
}

type CreateRequest struct {
	APIServiceID uint `json:"api_service_id" binding:"required"`
	CreateInput
}

type IDRequest struct {
	ID string `uri:"id" binding:"required"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

type ManagementResponse struct {
	models.APIBackend
	RouteCount           int64    `json:"route_count"`
	UpstreamCount        int64    `json:"upstream_count"`
	EnabledUpstreamCount int64    `json:"enabled_upstream_count"`
	EndpointHosts        []string `json:"endpoint_hosts"`
}

func (r *UpdateRequest) SetBodyMap(v map[string]any) { r.Fields = v }

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[ManagementResponse], error) {
	if req.APIServiceID == nil {
		return api.PaginatedResponse[ManagementResponse]{}, api.BadRequestError("api_service_id is required", nil)
	}
	p, s := api.NormalizePagination(req.Page, req.PageSize)
	rows, total, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIBackend().List(dao.ListOptions{Page: p, PageSize: s}, dao.APIBackendFilter{APIServiceID: req.APIServiceID, Search: req.Search})
	if err != nil {
		return api.PaginatedResponse[ManagementResponse]{}, api.InternalError("list API backends failed", err)
	}
	data := make([]ManagementResponse, 0, len(rows))
	for _, row := range rows {
		data = append(data, newManagementResponse(row))
	}
	return api.PaginatedResponse[ManagementResponse]{Data: data, Total: total, Page: p, PageSize: s}, nil
}

func (h *Handler) Get(c *app.Context, req IDRequest) (ManagementResponse, error) {
	backend, err := h.backend(c, req.ID)
	if err != nil {
		return ManagementResponse{}, err
	}
	response, err := h.managementResponse(c, *backend)
	if err != nil {
		return ManagementResponse{}, err
	}
	return response, nil
}

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[ManagementResponse], error) {
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	var backend models.APIBackend
	err := dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		var createErr error
		backend, createErr = CreateInTx(tx, req.APIServiceID, req.CreateInput)
		return createErr
	})
	if err != nil {
		return api.Created[ManagementResponse]{}, createBackendError(err)
	}
	response, err := h.managementResponse(c, backend)
	if err != nil {
		return api.Created[ManagementResponse]{}, err
	}
	return api.Created[ManagementResponse]{Value: response}, nil
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (api.StatusResponse, error) {
	backend, err := h.backend(c, req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	if err = dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext())).APIBackend().Update(backend.ID, req.Fields); err != nil {
		return api.StatusResponse{}, createBackendError(err)
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func (h *Handler) Delete(c *app.Context, req IDRequest) (api.StatusResponse, error) {
	backend, err := h.backend(c, req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	deleted, err := dao.NewAdminMutation(ctx).APIBackend().DeleteUnused(backend.ID)
	if err != nil {
		return api.StatusResponse{}, h.deleteError(ctx, backend.ID, err)
	}
	publishErrors := make([]error, 0, len(deleted))
	for _, upstream := range deleted {
		if publishErr := h.publish(c, upstream); publishErr != nil {
			publishErrors = append(publishErrors, publishErr)
		}
	}
	if cause := errors.Join(publishErrors...); cause != nil {
		return api.StatusResponse{}, api.InternalError("publish API upstream failed", cause)
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func CreateInTx(ctx dao.Context, apiServiceID uint, input CreateInput) (models.APIBackend, error) {
	backend := models.APIBackend{APIServiceID: apiServiceID, Name: input.Name}
	if err := dao.NewAdminMutation(ctx).APIBackend().Create(&backend); err != nil {
		return models.APIBackend{}, err
	}
	return backend, nil
}

func (h *Handler) backend(c *app.Context, raw string) (*models.APIBackend, error) {
	id, err := backendID(raw)
	if err != nil {
		return nil, err
	}
	backend, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIBackend().GetByID(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, api.NotFoundError("not found")
	}
	if err != nil {
		return nil, api.InternalError("load API backend failed", err)
	}
	return backend, nil
}

func (h *Handler) managementResponse(c *app.Context, backend models.APIBackend) (ManagementResponse, error) {
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	routeCount, err := dao.NewAdminQuery(ctx).APIBackend().RouteCount(backend.ID)
	if err != nil {
		return ManagementResponse{}, api.InternalError("count API backend routes failed", err)
	}
	var upstreams []models.APIUpstream
	if err := ctx.GetCoreDB().Where("backend_id = ?", backend.ID).Find(&upstreams).Error; err != nil {
		return ManagementResponse{}, api.InternalError("list API backend upstreams failed", err)
	}
	enabled, hosts, err := endpointSummary(upstreams)
	if err != nil {
		return ManagementResponse{}, api.InternalError("summarize API backend endpoints failed", err)
	}
	return ManagementResponse{
		APIBackend: backend, RouteCount: routeCount, UpstreamCount: int64(len(upstreams)),
		EnabledUpstreamCount: enabled, EndpointHosts: hosts,
	}, nil
}

func (h *Handler) deleteError(ctx dao.Context, backendID uint, cause error) error {
	if !errors.Is(cause, dao.ErrAPIBackendInUse) {
		return api.InternalError("delete API backend failed", cause)
	}
	routeCount, countErr := dao.NewAdminQuery(ctx).APIBackend().RouteCount(backendID)
	if countErr != nil {
		return api.InternalError("count API backend routes failed", countErr)
	}
	return api.ErrorWithCode(409, "backend_in_use", "backend is still referenced by routes", map[string]any{"route_count": routeCount})
}

func (h *Handler) publish(c *app.Context, upstream models.APIUpstream) error {
	if h.Publisher == nil {
		return nil
	}
	if err := h.Publisher.PublishUpstream(context.Background(), "delete", upstream); err != nil {
		return api.InternalError("publish API upstream failed", err)
	}
	return nil
}

func newManagementResponse(item dao.APIBackendListItem) ManagementResponse {
	return ManagementResponse{
		APIBackend: item.APIBackend, RouteCount: item.RouteCount, UpstreamCount: item.UpstreamCount,
		EnabledUpstreamCount: item.EnabledUpstreamCount, EndpointHosts: item.EndpointHosts,
	}
}

func endpointSummary(upstreams []models.APIUpstream) (int64, []string, error) {
	var enabled int64
	hostSet := make(map[string]struct{}, len(upstreams))
	for _, upstream := range upstreams {
		if upstream.Status == 1 {
			enabled++
		}
		parsed, err := url.Parse(upstream.BaseURL)
		if err != nil {
			return 0, nil, err
		}
		hostSet[parsed.Hostname()] = struct{}{}
	}
	hosts := make([]string, 0, len(hostSet))
	for host := range hostSet {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return enabled, hosts, nil
}

func createBackendError(err error) error {
	if errors.Is(err, dao.ErrAPIBackendNameConflict) {
		return api.ErrorWithCode(409, "backend_name_conflict", "backend name already exists for this service", nil)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return api.NotFoundError("not found")
	}
	return api.BadRequestError("create API backend failed", err)
}

func backendID(raw string) (uint, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, api.BadRequestError("invalid id", err)
	}
	return uint(n), nil
}
