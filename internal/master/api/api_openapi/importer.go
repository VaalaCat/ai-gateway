package api_openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	api_backend "github.com/VaalaCat/ai-gateway/internal/master/api/api_backend"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	coreopenapi "github.com/VaalaCat/ai-gateway/internal/pkg/apiopenapi"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type ImportPublisher interface {
	PublishService(context.Context, string, models.APIService) error
	PublishUpstream(context.Context, string, models.APIUpstream) error
	PublishRoute(context.Context, string, models.APIRoute) error
}

type Importer struct {
	UpstreamCreator api_upstream.Creator
	Publisher       ImportPublisher
}

// UpstreamDraft carries the non-document fields needed to create the first
// upstream. BaseURL is optional because a selected OpenAPI server supplies it;
// documents without servers require it in selectedUpstreamInput.
type UpstreamDraft struct {
	Name           string                              `json:"name" binding:"required,max=64"`
	BaseURL        string                              `json:"base_url"`
	Weight         int                                 `json:"weight"`
	Priority       int                                 `json:"priority"`
	AuthType       models.APIUpstreamAuthType          `json:"auth_type"`
	Credential     *api_upstream.APIUpstreamCredential `json:"credential"`
	ProxyURL       *string                             `json:"proxy_url"`
	HeaderOverride map[string]string                   `json:"header_override"`
	Status         *int                                `json:"status"`
}

type ImportCommand struct {
	Document       json.RawMessage                `json:"document" binding:"required"`
	Slug           string                         `json:"slug" binding:"required,max=64"`
	Choices        []coreopenapi.RouteGroupChoice `json:"choices"`
	SelectedServer *int                           `json:"selected_server"`
	BackendName    string                         `json:"backend_name" binding:"required,max=64"`
	Upstream       UpstreamDraft                  `json:"upstream" binding:"required"`
	PricePerCall   int64                          `json:"price_per_call"`
}

type ImportResult struct {
	ServiceID  uint   `json:"service_id"`
	BackendID  uint   `json:"backend_id"`
	UpstreamID uint   `json:"upstream_id"`
	RouteIDs   []uint `json:"route_ids"`
}

type importedGraph struct {
	service  models.APIService
	backend  models.APIBackend
	upstream models.APIUpstream
	routes   []models.APIRoute
}

func (i Importer) Import(ctx dao.Context, cmd ImportCommand) (ImportResult, error) {
	bundle, err := parseAndGroup(cmd.Document, cmd.Choices)
	if err != nil {
		return ImportResult{}, openAPIInputError(err)
	}
	upstreamInput, err := selectedUpstreamInput(bundle, cmd)
	if err != nil {
		return ImportResult{}, err
	}
	if err := validateImportWriteInput(bundle, cmd, upstreamInput); err != nil {
		return ImportResult{}, api.BadRequestError("OpenAPI import request is invalid", err)
	}
	graph, err := i.createGraph(ctx, cmd, bundle, upstreamInput)
	if err != nil {
		return ImportResult{}, importWriteError(cmd.Slug, err)
	}
	result := importResult(graph)
	if err := i.publish(graph); err != nil {
		return result, api.ErrorWithCode(http.StatusInternalServerError, "sync_publish_failed", "OpenAPI import committed but sync publication failed", map[string]any{"service_id": result.ServiceID})
	}
	return result, nil
}

func validateImportWriteInput(bundle coreopenapi.ImportBundle, cmd ImportCommand, upstreamInput api_upstream.CreateInput) error {
	service := models.APIService{
		Slug: cmd.Slug, Name: bundle.Name, Description: bundle.Description,
		PricePerCall: cmd.PricePerCall, Status: consts.StatusEnabled,
		OpenAPIDocument: datatypes.NewJSONType(bundle.Document),
	}
	if err := service.NormalizeForWrite(); err != nil {
		return err
	}
	if err := service.Validate(); err != nil {
		return err
	}
	if err := (&models.APIBackend{APIServiceID: 1, Name: cmd.BackendName}).Validate(); err != nil {
		return err
	}
	if _, err := api_upstream.BuildAPIUpstreamForCreate(1, upstreamInput); err != nil {
		return err
	}
	for _, draft := range bundle.Routes {
		route := draft.APIRoute
		route.APIServiceID = 1
		route.BackendID = 1
		if err := route.NormalizeForWrite(); err != nil {
			return err
		}
		if err := route.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (i Importer) createGraph(
	ctx dao.Context,
	cmd ImportCommand,
	bundle coreopenapi.ImportBundle,
	upstreamInput api_upstream.CreateInput,
) (importedGraph, error) {
	var graph importedGraph
	err := dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		if err := ensureImportSlugAvailable(tx, cmd.Slug); err != nil {
			return err
		}
		graph.service = models.APIService{
			Slug: cmd.Slug, Name: bundle.Name, Description: bundle.Description,
			PricePerCall: cmd.PricePerCall, Status: consts.StatusEnabled,
			OpenAPIDocument: datatypes.NewJSONType(bundle.Document),
		}
		if err := dao.NewAdminMutation(tx).APIService().Create(&graph.service); err != nil {
			return err
		}
		var err error
		graph.backend, err = api_backend.CreateInTx(tx, graph.service.ID, api_backend.CreateInput{Name: cmd.BackendName})
		if err != nil {
			return err
		}
		graph.upstream, err = i.UpstreamCreator.CreateInTx(tx, graph.backend.ID, upstreamInput)
		if err != nil {
			return err
		}
		graph.routes, err = createImportRoutes(tx, graph.service.ID, graph.backend.ID, bundle.Routes)
		return err
	})
	return graph, err
}

func createImportRoutes(ctx dao.Context, serviceID, backendID uint, drafts []coreopenapi.ImportRoute) ([]models.APIRoute, error) {
	routes := make([]models.APIRoute, 0, len(drafts))
	for _, draft := range drafts {
		route := draft.APIRoute
		route.APIServiceID = serviceID
		route.BackendID = backendID
		if err := dao.NewAdminMutation(ctx).APIRoute().Create(&route); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func ensureImportSlugAvailable(ctx dao.Context, slug string) error {
	_, err := dao.NewAdminQuery(ctx).APIService().GetBySlug(slug)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return api.ErrorWithCode(http.StatusConflict, "service_slug_conflict", "API service slug already exists", map[string]any{"slug": slug})
}

func selectedUpstreamInput(bundle coreopenapi.ImportBundle, cmd ImportCommand) (api_upstream.CreateInput, error) {
	input := cmd.Upstream.createInput()
	if len(bundle.Servers) == 0 {
		if cmd.SelectedServer != nil {
			return api_upstream.CreateInput{}, api.ErrorWithCode(http.StatusBadRequest, "selected_server_not_found", "selected OpenAPI server does not exist", nil)
		}
		if input.BaseURL == "" {
			return api_upstream.CreateInput{}, api.ErrorWithCode(http.StatusBadRequest, "upstream_url_required", "upstream base_url is required when the document has no servers", nil)
		}
		return input, nil
	}
	if cmd.SelectedServer == nil {
		return api_upstream.CreateInput{}, api.ErrorWithCode(http.StatusBadRequest, "server_selection_required", "one OpenAPI server must be selected", nil)
	}
	if *cmd.SelectedServer < 0 || *cmd.SelectedServer >= len(bundle.Servers) {
		return api_upstream.CreateInput{}, api.ErrorWithCode(http.StatusBadRequest, "selected_server_not_found", "selected OpenAPI server does not exist", nil)
	}
	baseURL, err := expandServerURL(bundle.Servers[*cmd.SelectedServer])
	if err != nil {
		return api_upstream.CreateInput{}, api.ErrorWithCode(http.StatusBadRequest, "invalid_selected_server", "selected OpenAPI server cannot be used as an upstream", nil)
	}
	input.BaseURL = baseURL
	return input, nil
}

func (draft UpstreamDraft) createInput() api_upstream.CreateInput {
	return api_upstream.CreateInput{
		Name: draft.Name, BaseURL: draft.BaseURL, Weight: draft.Weight, Priority: draft.Priority,
		AuthType: draft.AuthType, Credential: draft.Credential, ProxyURL: draft.ProxyURL,
		HeaderOverride: draft.HeaderOverride, Status: draft.Status,
	}
}

func expandServerURL(server models.OpenAPIServer) (string, error) {
	result := server.URL
	names := make([]string, 0, len(server.Variables))
	for name := range server.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		variable := server.Variables[name]
		if variable.Default == "" {
			return "", fmt.Errorf("server variable %s has no default", name)
		}
		result = strings.ReplaceAll(result, "{"+name+"}", variable.Default)
	}
	if strings.ContainsAny(result, "{}") {
		return "", fmt.Errorf("server URL contains unresolved variables")
	}
	return result, nil
}

func importResult(graph importedGraph) ImportResult {
	result := ImportResult{
		ServiceID: graph.service.ID, BackendID: graph.backend.ID, UpstreamID: graph.upstream.ID,
		RouteIDs: make([]uint, 0, len(graph.routes)),
	}
	for _, route := range graph.routes {
		result.RouteIDs = append(result.RouteIDs, route.ID)
	}
	return result
}

func (i Importer) publish(graph importedGraph) error {
	if i.Publisher == nil {
		return nil
	}
	ctx := context.Background()
	errs := []error{
		i.Publisher.PublishService(ctx, "create", graph.service),
		i.Publisher.PublishUpstream(ctx, "create", graph.upstream),
	}
	for _, route := range graph.routes {
		errs = append(errs, i.Publisher.PublishRoute(ctx, "create", route))
	}
	return errors.Join(errs...)
}

func importWriteError(slug string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	message := strings.ToLower(err.Error())
	if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(message, "api_services.slug") {
		return api.ErrorWithCode(http.StatusConflict, "service_slug_conflict", "API service slug already exists", map[string]any{"slug": slug})
	}
	return api.InternalError("import OpenAPI failed", err)
}
