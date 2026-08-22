package api_openapi

import "github.com/VaalaCat/ai-gateway/internal/pkg/app"

type Handler struct {
	App      app.Application
	Importer Importer
	Finder   OpenAPIDocumentFinder
}
