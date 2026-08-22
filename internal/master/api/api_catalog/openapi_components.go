package api_catalog

import coreopenapi "github.com/VaalaCat/ai-gateway/internal/pkg/apiopenapi"

func retainReachableOpenAPIComponents(raw []byte) ([]byte, error) {
	return coreopenapi.RetainReachableComponents(raw)
}
