package api_service

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type ServicePublisher interface {
	PublishService(context.Context, string, models.APIService) error
}
type Handler struct {
	App       app.Application
	Publisher ServicePublisher
}
type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
	Status *int   `form:"status"`
}
type CreateRequest struct {
	Slug         string `json:"slug" binding:"required,max=64"`
	Name         string `json:"name" binding:"required,max=128"`
	Description  string `json:"description"`
	PricePerCall int64  `json:"price_per_call"`
	Status       *int   `json:"status"`
}
type IDRequest struct {
	ID string `uri:"id" binding:"required"`
}
type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(v map[string]any) { r.Fields = v }

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.APIService], error) {
	page, size := api.NormalizePagination(req.Page, req.PageSize)
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	filter := dao.APIServiceFilter{Search: req.Search, Status: req.Status}
	rows, total, err := q.APIService().List(dao.ListOptions{Page: page, PageSize: size}, filter)
	if err != nil {
		return api.PaginatedResponse[models.APIService]{}, api.InternalError("list API services failed", err)
	}
	return api.PaginatedResponse[models.APIService]{Data: rows, Total: total, Page: page, PageSize: size}, nil
}
func (h *Handler) Get(c *app.Context, req IDRequest) (models.APIService, error) {
	id, err := parseID(req.ID)
	if err != nil {
		return models.APIService{}, err
	}
	row, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIService().GetByID(id)
	if err != nil {
		return models.APIService{}, api.NotFoundError(consts.ErrNotFound)
	}
	return *row, nil
}
func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.APIService], error) {
	status := consts.StatusEnabled
	if req.Status != nil {
		status = *req.Status
	}
	row := models.APIService{Slug: req.Slug, Name: req.Name, Description: req.Description, PricePerCall: req.PricePerCall, Status: status}
	if err := dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext())).APIService().Create(&row); err != nil {
		return api.Created[models.APIService]{}, api.BadRequestError("create API service failed", err)
	}
	if err := h.publish(c, "create", row); err != nil {
		return api.Created[models.APIService]{}, err
	}
	return api.Created[models.APIService]{Value: row}, nil
}
func (h *Handler) Update(c *app.Context, req UpdateRequest) (api.StatusResponse, error) {
	id, err := parseID(req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	normalizeFields(req.Fields)
	if err = dao.NewAdminMutation(ctx).APIService().Update(id, req.Fields); err != nil {
		return api.StatusResponse{}, api.BadRequestError("update API service failed", err)
	}
	row, err := dao.NewAdminQuery(ctx).APIService().GetByID(id)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("reload API service failed", err)
	}
	if err = h.publish(c, "update", *row); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}
func (h *Handler) Delete(c *app.Context, req IDRequest) (api.StatusResponse, error) {
	id, err := parseID(req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	row, err := dao.NewAdminQuery(ctx).APIService().GetByID(id)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err = dao.NewAdminMutation(ctx).APIService().Delete(id); err != nil {
		return api.StatusResponse{}, api.InternalError("delete API service failed", err)
	}
	if err = h.publish(c, "delete", *row); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}
func (h *Handler) publish(c *app.Context, action string, row models.APIService) error {
	if h.Publisher == nil {
		return nil
	}
	if err := h.Publisher.PublishService(context.Background(), action, row); err != nil {
		return api.InternalError("publish API service failed", err)
	}
	return nil
}
func parseID(raw string) (uint, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, api.BadRequestError("invalid id", err)
	}
	return uint(n), nil
}

func normalizeFields(fields map[string]any) {
	for _, key := range []string{"price_per_call", "status"} {
		if value, ok := fields[key].(float64); ok && value == float64(int64(value)) {
			if key == "price_per_call" {
				fields[key] = int64(value)
			} else {
				fields[key] = int(value)
			}
		}
	}
}
