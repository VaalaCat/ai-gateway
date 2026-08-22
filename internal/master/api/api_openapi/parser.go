package api_openapi

import (
	"encoding/json"

	coreopenapi "github.com/VaalaCat/ai-gateway/internal/pkg/apiopenapi"
)

func parseAndGroup(raw json.RawMessage, choices []coreopenapi.RouteGroupChoice) (coreopenapi.ImportBundle, error) {
	parsed, err := coreopenapi.ParseJSON(raw)
	if err != nil {
		return coreopenapi.ImportBundle{}, err
	}
	return coreopenapi.GroupRoutes(parsed, choices)
}
