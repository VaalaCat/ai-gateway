package api_route

import (
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/genericapipath"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type PreviewRequest struct {
	APIServiceID   uint                     `json:"api_service_id" binding:"required"`
	Slug           string                   `json:"slug" binding:"required"`
	UpstreamPath   string                   `json:"upstream_path"`
	ForwardSubpath bool                     `json:"forward_subpath"`
	Sample         models.APIRequestExample `json:"sample"`
	Target         RouteTargetCommand       `json:"target"`
}

type PreviewEndpoint struct {
	UpstreamID   uint   `json:"upstream_id"`
	UpstreamName string `json:"upstream_name"`
	Status       int    `json:"status"`
	Priority     int    `json:"priority"`
	Weight       int    `json:"weight"`
	FinalURL     string `json:"final_url"`
}

type RoutePreviewResponse struct {
	Endpoints   []PreviewEndpoint `json:"endpoints"`
	Diagnostics []string          `json:"diagnostics"`
}

type previewTargetReader func(*Handler, dao.Context, uint, RouteTargetCommand) ([]models.APIUpstream, error)

type previewStaticConfigurationError struct{ err error }

func (e *previewStaticConfigurationError) Error() string { return e.err.Error() }
func (e *previewStaticConfigurationError) Unwrap() error { return e.err }

var previewTargetReaders = map[string]previewTargetReader{
	"existing": readExistingPreviewTarget,
	"create":   readDraftPreviewTarget,
}

// Preview returns every statically configured candidate for a route draft. It
// is deliberately read-only: it neither selects a candidate nor calls any
// runtime gate, transport, accounting, observation, or publish component.
func (h *Handler) Preview(c *app.Context, req PreviewRequest) (RoutePreviewResponse, error) {
	if err := validatePreviewRequest(req); err != nil {
		return RoutePreviewResponse{}, previewValidationError(err)
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	candidates, err := h.readPreviewTarget(ctx, req.APIServiceID, req.Target)
	if err != nil {
		return RoutePreviewResponse{}, previewTargetError(err)
	}
	return buildPreviewResponse(req, candidates)
}

func validatePreviewRequest(req PreviewRequest) error {
	if _, err := parseRouteTarget(req.Target); err != nil {
		return err
	}
	route := models.APIRoute{
		APIServiceID:   req.APIServiceID,
		BackendID:      1,
		Slug:           req.Slug,
		Protocols:      datatypes.JSONSlice[models.APIProtocol]{models.APIProtocolHTTP},
		UpstreamPath:   req.UpstreamPath,
		ExampleRequest: datatypes.NewJSONType(req.Sample),
		Status:         consts.StatusEnabled,
	}
	if err := route.NormalizeForWrite(); err != nil {
		return err
	}
	if err := route.Validate(); err != nil {
		return err
	}
	subpath := ""
	if req.ForwardSubpath {
		subpath = req.Sample.Subpath
	}
	_, err := (genericapipath.Builder{}).Build("https://preview.invalid", req.UpstreamPath, subpath, req.Sample.Query)
	return err
}

func (h *Handler) readPreviewTarget(ctx dao.Context, serviceID uint, command RouteTargetCommand) ([]models.APIUpstream, error) {
	reader, ok := previewTargetReaders[command.Mode]
	if !ok {
		return nil, fmt.Errorf("invalid route target mode")
	}
	return reader(h, ctx, serviceID, command)
}

func readExistingPreviewTarget(_ *Handler, ctx dao.Context, serviceID uint, command RouteTargetCommand) ([]models.APIUpstream, error) {
	if command.BackendID == 0 || command.Backend != nil || command.FirstUpstream != nil {
		return nil, &previewStaticConfigurationError{err: fmt.Errorf("existing route target requires backend_id only")}
	}
	backend, err := dao.NewAdminQuery(ctx).APIBackend().GetByID(command.BackendID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, api.ErrorWithCode(404, "backend_not_found", "backend not found", nil)
	}
	if err != nil {
		return nil, err
	}
	if backend.APIServiceID != serviceID {
		return nil, api.ErrorWithCode(400, "backend_service_mismatch", "route target backend belongs to another API service", nil)
	}
	return listPreviewUpstreams(ctx, backend.ID)
}

func readDraftPreviewTarget(_ *Handler, _ dao.Context, serviceID uint, command RouteTargetCommand) ([]models.APIUpstream, error) {
	if command.BackendID != 0 || command.Backend == nil || command.FirstUpstream == nil {
		return nil, &previewStaticConfigurationError{err: fmt.Errorf("created route target requires backend and first_upstream")}
	}
	backend := models.APIBackend{APIServiceID: serviceID, Name: command.Backend.Name}
	if err := backend.Validate(); err != nil {
		return nil, &previewStaticConfigurationError{err: err}
	}
	upstream, err := api_upstream.BuildAPIUpstreamForCreate(1, *command.FirstUpstream)
	if err != nil {
		return nil, &previewStaticConfigurationError{err: err}
	}
	return []models.APIUpstream{upstream}, nil
}

func listPreviewUpstreams(ctx dao.Context, backendID uint) ([]models.APIUpstream, error) {
	const allCandidates = int(^uint(0) >> 1)
	rows, _, err := dao.NewAdminQuery(ctx).APIUpstream().List(
		dao.ListOptions{Page: 1, PageSize: allCandidates}, dao.APIUpstreamFilter{BackendID: &backendID},
	)
	return rows, err
}

func previewTargetError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return api.ErrorWithCode(404, "backend_not_found", consts.ErrNotFound, nil)
	}
	var static *previewStaticConfigurationError
	if errors.As(err, &static) {
		return api.ErrorWithCode(400, "preview_invalid_target", "invalid route preview", nil)
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return api.InternalError("read preview route target", err)
}

func previewValidationError(err error) error {
	if _, ok := models.APIRequestExampleErrorCode(err); ok {
		return routeWriteError("invalid route preview", err)
	}
	return api.ErrorWithCode(400, "preview_invalid_target", "invalid route preview", nil)
}

func buildPreviewResponse(req PreviewRequest, candidates []models.APIUpstream) (RoutePreviewResponse, error) {
	response := RoutePreviewResponse{Endpoints: make([]PreviewEndpoint, 0, len(candidates)), Diagnostics: []string{}}
	hasEnabledEndpoint := false
	subpath := ""
	if req.ForwardSubpath {
		subpath = req.Sample.Subpath
	}
	for _, candidate := range candidates {
		finalURL, err := (genericapipath.Builder{}).Build(candidate.BaseURL, req.UpstreamPath, subpath, req.Sample.Query)
		if err != nil {
			return RoutePreviewResponse{}, api.ErrorWithCode(400, "preview_invalid_target", "unsafe preview upstream URL", nil)
		}
		response.Endpoints = append(response.Endpoints, PreviewEndpoint{
			UpstreamID: candidate.ID, UpstreamName: candidate.Name, Status: candidate.Status, Priority: candidate.Priority, Weight: candidate.Weight, FinalURL: finalURL.String(),
		})
		hasEnabledEndpoint = hasEnabledEndpoint || candidate.Status == consts.StatusEnabled
	}
	if !hasEnabledEndpoint {
		response.Diagnostics = append(response.Diagnostics, "no_available_upstream")
	}
	return response, nil
}
