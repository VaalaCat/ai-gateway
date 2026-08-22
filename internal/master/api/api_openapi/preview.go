package api_openapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	coreopenapi "github.com/VaalaCat/ai-gateway/internal/pkg/apiopenapi"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

type Problem = coreopenapi.Problem

type PreviewRequest struct {
	Document json.RawMessage `json:"document" binding:"required"`
}

type ServiceDraft struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ServerDraft struct {
	Index       int    `json:"index"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type RouteDraft struct {
	Slug           string            `json:"slug"`
	DisplayName    string            `json:"display_name"`
	UpstreamPath   string            `json:"upstream_path"`
	AllowedMethods []string          `json:"allowed_methods"`
	Paths          []string          `json:"paths"`
	PublicPaths    map[string]string `json:"public_paths"`
	PathCount      int               `json:"path_count"`
	OperationCount int               `json:"operation_count"`
}

type PreviewResponse struct {
	Service  ServiceDraft  `json:"service"`
	Servers  []ServerDraft `json:"servers"`
	Routes   []RouteDraft  `json:"routes"`
	Problems []Problem     `json:"problems"`
}

func (h *Handler) Preview(c *app.Context, req PreviewRequest) (PreviewResponse, error) {
	bundle, err := parseAndGroup(req.Document, nil)
	if err != nil {
		return PreviewResponse{}, openAPIInputError(err)
	}
	service := ServiceDraft{Slug: serviceSlug(bundle.Name), Name: bundle.Name, Description: bundle.Description}
	if err := ensureServiceSlugAvailable(c, service.Slug); err != nil {
		return PreviewResponse{}, err
	}
	return PreviewResponse{
		Service: service, Servers: serverDrafts(bundle.Servers), Routes: routeDrafts(bundle.Routes),
		Problems: append([]Problem(nil), bundle.Warnings...),
	}, nil
}

func ensureServiceSlugAvailable(c *app.Context, slug string) error {
	_, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIService().GetBySlug(slug)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return api.InternalError("check API service slug failed", err)
	}
	return api.ErrorWithCode(http.StatusConflict, "service_slug_conflict", "API service slug already exists", map[string]any{"slug": slug})
}

func openAPIInputError(err error) error {
	var parseErr *coreopenapi.ParseError
	if errors.As(err, &parseErr) {
		return api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi", "OpenAPI document is invalid", map[string]any{"problems": parseErr.Problems})
	}
	return api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi", "OpenAPI document is invalid", nil)
}

var nonSlugRun = regexp.MustCompile(`[^a-z0-9._~-]+`)

func serviceSlug(name string) string {
	slug := nonSlugRun.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-._~")
	if slug == "" {
		slug = "api"
	}
	if len(slug) > 64 {
		slug = strings.TrimRight(slug[:64], "-._~")
	}
	return slug
}

func serverDrafts(servers []models.OpenAPIServer) []ServerDraft {
	result := make([]ServerDraft, 0, len(servers))
	for index, server := range servers {
		result = append(result, ServerDraft{Index: index, URL: server.URL, Description: server.Description})
	}
	return result
}

func routeDrafts(routes []coreopenapi.ImportRoute) []RouteDraft {
	result := make([]RouteDraft, 0, len(routes))
	for _, route := range routes {
		paths := make([]string, 0, len(route.OpenAPIPaths.Data()))
		operations := 0
		for path, item := range route.OpenAPIPaths.Data() {
			paths = append(paths, path)
			operations += len(item.Operations)
		}
		sort.Strings(paths)
		displayName := route.Slug
		if displayName == "" {
			displayName = "根路由"
		}
		result = append(result, RouteDraft{
			Slug: route.Slug, DisplayName: displayName, UpstreamPath: route.UpstreamPath,
			AllowedMethods: append([]string(nil), route.AllowedMethods...), Paths: paths,
			PublicPaths: route.PublicPaths, PathCount: len(paths), OperationCount: operations,
		})
	}
	return result
}
