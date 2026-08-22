package api_route

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type RoutePublisher interface {
	PublishRoute(context.Context, string, models.APIRoute) error
	PublishUpstream(context.Context, string, models.APIUpstream) error
}
type Handler struct {
	App             app.Application
	Publisher       RoutePublisher
	UpstreamCreator api_upstream.Creator
}
type ListRequest struct {
	api.PaginationQuery
	APIServiceID *uint  `form:"api_service_id"`
	Search       string `form:"search"`
	Status       *int   `form:"status"`
}
type CreateRequest struct {
	APIServiceID          uint                     `json:"api_service_id" binding:"required"`
	Slug                  string                   `json:"slug" binding:"max=64"`
	Protocols             []models.APIProtocol     `json:"protocols"`
	AllowedMethods        []string                 `json:"allowed_methods"`
	WebSocketSubprotocols []string                 `json:"websocket_subprotocols"`
	UpstreamPath          string                   `json:"upstream_path"`
	ForwardSubpath        bool                     `json:"forward_subpath"`
	ExampleRequest        models.APIRequestExample `json:"example_request"`
	Target                RouteTargetCommand       `json:"target" binding:"required"`
	Status                *int                     `json:"status"`
}
type IDRequest struct {
	ID string `uri:"id" binding:"required"`
}
type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(v map[string]any) { r.Fields = v }
func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.APIRoute], error) {
	if req.APIServiceID == nil {
		return api.PaginatedResponse[models.APIRoute]{}, api.BadRequestError("api_service_id is required", nil)
	}
	p, s := api.NormalizePagination(req.Page, req.PageSize)
	rows, total, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIRoute().List(dao.ListOptions{Page: p, PageSize: s}, dao.APIRouteFilter{APIServiceID: req.APIServiceID, Search: req.Search, Status: req.Status})
	if err != nil {
		return api.PaginatedResponse[models.APIRoute]{}, api.InternalError("list API routes failed", err)
	}
	return api.PaginatedResponse[models.APIRoute]{Data: rows, Total: total, Page: p, PageSize: s}, nil
}
func (h *Handler) Get(c *app.Context, req IDRequest) (models.APIRoute, error) {
	id, err := id(req.ID)
	if err != nil {
		return models.APIRoute{}, err
	}
	row, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIRoute().GetByID(id)
	if err != nil {
		return models.APIRoute{}, api.NotFoundError(consts.ErrNotFound)
	}
	return *row, nil
}
func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.APIRoute], error) {
	status := consts.StatusEnabled
	if req.Status != nil {
		status = *req.Status
	}
	row := models.APIRoute{APIServiceID: req.APIServiceID, Slug: req.Slug, Protocols: datatypes.JSONSlice[models.APIProtocol](req.Protocols), AllowedMethods: datatypes.JSONSlice[string](req.AllowedMethods), WebSocketSubprotocols: datatypes.JSONSlice[string](req.WebSocketSubprotocols), UpstreamPath: req.UpstreamPath, ForwardSubpath: req.ForwardSubpath, ExampleRequest: datatypes.NewJSONType(req.ExampleRequest), Status: status}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	var target targetWriteResult
	err := dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		if _, err := dao.NewAdminQuery(tx).APIService().LockByID(row.APIServiceID); err != nil {
			return err
		}
		var targetErr error
		target, targetErr = h.applyTargetInTx(tx, row.APIServiceID, req.Target)
		if targetErr != nil {
			return targetErr
		}
		row.BackendID = target.BackendID
		return dao.NewAdminMutation(tx).APIRoute().Create(&row)
	})
	if err != nil {
		return api.Created[models.APIRoute]{}, routeWriteError("create API route failed", err)
	}
	if target.Upstream != nil {
		if err := h.publishUpstream(c, "create", *target.Upstream); err != nil {
			return api.Created[models.APIRoute]{}, err
		}
	}
	if err := h.publish(c, "create", row); err != nil {
		return api.Created[models.APIRoute]{}, err
	}
	return api.Created[models.APIRoute]{Value: row}, nil
}
func (h *Handler) Update(c *app.Context, req UpdateRequest) (api.StatusResponse, error) {
	if _, ok := req.Fields["openapi_paths"]; ok {
		return api.StatusResponse{}, api.ErrorWithCode(
			http.StatusBadRequest,
			"openapi_document_requires_dedicated_endpoint",
			"OpenAPI document must be updated through the dedicated endpoint",
			nil,
		)
	}
	row, err := h.route(c, req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	target, err := takeRouteTarget(req.Fields)
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError("invalid route target", err)
	}
	if err := normalizeFields(req.Fields); err != nil {
		return api.StatusResponse{}, err
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	var targetResult targetWriteResult
	err = dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		// Keep the shared service -> backend lock order used by API upstream
		// writes before a target writer locks an existing backend.
		if _, lockErr := dao.NewAdminQuery(tx).APIService().LockByID(row.APIServiceID); lockErr != nil {
			return lockErr
		}
		if target != nil {
			var targetErr error
			targetResult, targetErr = h.applyTargetInTx(tx, row.APIServiceID, *target)
			if targetErr != nil {
				return targetErr
			}
			req.Fields["backend_id"] = targetResult.BackendID
		}
		return dao.NewAdminMutation(tx).APIRoute().Update(row.ID, req.Fields)
	})
	if err != nil {
		return api.StatusResponse{}, routeWriteError("update API route failed", err)
	}
	row, err = dao.NewAdminQuery(ctx).APIRoute().GetByID(row.ID)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("reload API route failed", err)
	}
	if targetResult.Upstream != nil {
		if err = h.publishUpstream(c, "create", *targetResult.Upstream); err != nil {
			return api.StatusResponse{}, err
		}
	}
	if err = h.publish(c, "update", *row); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func routeWriteError(message string, err error) error {
	if code, ok := models.APIRequestExampleErrorCode(err); ok {
		return api.ErrorWithCode(400, code, code, nil)
	}
	if errors.Is(err, dao.ErrAPIBackendNameConflict) {
		return api.ErrorWithCode(409, "backend_name_conflict", "backend name already exists for this service", nil)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return api.NotFoundError(consts.ErrNotFound)
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return api.BadRequestError(message, err)
}

func (h *Handler) Delete(c *app.Context, req IDRequest) (api.StatusResponse, error) {
	row, err := h.route(c, req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	if err = dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext())).APIRoute().Delete(row.ID); err != nil {
		return api.StatusResponse{}, api.InternalError("delete API route failed", err)
	}
	if err = h.publish(c, "delete", *row); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}
func (h *Handler) route(c *app.Context, raw string) (*models.APIRoute, error) {
	n, err := id(raw)
	if err != nil {
		return nil, err
	}
	row, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIRoute().GetByID(n)
	if err != nil {
		return nil, api.NotFoundError(consts.ErrNotFound)
	}
	return row, nil
}
func (h *Handler) publish(c *app.Context, a string, row models.APIRoute) error {
	if h.Publisher != nil {
		if err := h.Publisher.PublishRoute(context.Background(), a, row); err != nil {
			return api.InternalError("publish API route failed", err)
		}
	}
	return nil
}

func (h *Handler) publishUpstream(c *app.Context, action string, row models.APIUpstream) error {
	if h.Publisher != nil {
		if err := h.Publisher.PublishUpstream(context.Background(), action, row); err != nil {
			return api.InternalError("publish API upstream failed", err)
		}
	}
	return nil
}
func id(raw string) (uint, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, api.BadRequestError("invalid id", err)
	}
	return uint(n), nil
}
func normalizeFields(v map[string]any) error {
	if status, ok := v["status"].(float64); ok && status == float64(int(status)) {
		v["status"] = int(status)
	}
	if p, ok := v["protocols"].([]any); ok {
		values := make(datatypes.JSONSlice[models.APIProtocol], 0, len(p))
		for _, x := range p {
			s, ok := x.(string)
			if !ok {
				return api.BadRequestError("protocols must contain strings", nil)
			}
			values = append(values, models.APIProtocol(s))
		}
		v["protocols"] = values
	}
	if m, ok := v["allowed_methods"].([]any); ok {
		values := make(datatypes.JSONSlice[string], 0, len(m))
		for _, x := range m {
			s, ok := x.(string)
			if !ok {
				return api.BadRequestError("allowed_methods must contain strings", nil)
			}
			values = append(values, s)
		}
		v["allowed_methods"] = values
	}
	if p, ok := v["websocket_subprotocols"].([]any); ok {
		values := make(datatypes.JSONSlice[string], 0, len(p))
		for _, x := range p {
			s, ok := x.(string)
			if !ok {
				return api.BadRequestError("websocket_subprotocols must contain strings", nil)
			}
			values = append(values, s)
		}
		v["websocket_subprotocols"] = values
	}
	if raw, exists := v["example_request"]; exists {
		example, err := decodeExampleRequest(raw)
		if err != nil {
			return api.BadRequestError("invalid example_request", err)
		}
		v["example_request"] = datatypes.NewJSONType(example)
	}
	return nil
}

func takeRouteTarget(fields map[string]any) (*RouteTargetCommand, error) {
	raw, exists := fields["target"]
	if !exists {
		return nil, nil
	}
	delete(fields, "target")
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var command RouteTargetCommand
	if err := json.Unmarshal(encoded, &command); err != nil {
		return nil, err
	}
	if _, err := parseRouteTarget(command); err != nil {
		return nil, err
	}
	return &command, nil
}

func decodeExampleRequest(raw any) (models.APIRequestExample, error) {
	if raw == nil {
		return models.APIRequestExample{}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return models.APIRequestExample{}, err
	}
	var example models.APIRequestExample
	if err := json.Unmarshal(encoded, &example); err != nil {
		return models.APIRequestExample{}, err
	}
	return example, nil
}
