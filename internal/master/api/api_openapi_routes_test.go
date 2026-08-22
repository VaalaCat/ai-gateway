package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	apiopenapi "github.com/VaalaCat/ai-gateway/internal/master/api/api_openapi"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

// Break caught: static OpenAPI management paths must be registered in the
// AdminOnly group before the service :id path and the legacy portal path must
// not remain reachable.
func TestOpenAPIManagementRoutesAreAdminOnlyAndUseDedicatedWrites(t *testing.T) {
	srv := setupTestMaster(t)
	srv.Cfg.Master.PublicBaseURLs = []string{"https://gateway.example.test"}
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	document := multiRouteOpenAPIRequestDocument()

	unauthenticated := reqHelper(srv, "", http.MethodPost, "/api/admin/api-services/openapi/preview", map[string]any{"document": document})
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())
	legacy := reqHelper(srv, adminJWT, http.MethodPost, "/api/api-services/openapi/preview", map[string]any{"document": document})
	require.Equal(t, http.StatusNotFound, legacy.Code, legacy.Body.String())
	preview := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-services/openapi/preview", map[string]any{"document": document})
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())

	selected := 0
	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-services/openapi/import", map[string]any{
		"document": document, "slug": "route-wired-openapi", "selected_server": selected,
		"backend_name": "primary", "upstream": map[string]any{"name": "origin"},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var result apiopenapi.ImportResult
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &result))
	require.NotZero(t, result.ServiceID)

	path := "/api/admin/api-services/" + strconv.FormatUint(uint64(result.ServiceID), 10) + "/openapi"
	get := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var response apiopenapi.DocumentResponse
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &response))
	require.NotEmpty(t, response.Export)
	require.Contains(t, string(response.Export), "https://gateway.example.test/v1/api/route-wired-openapi")

	user := models.User{Username: "openapi-non-admin", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	for _, request := range []struct {
		method string
		path   string
		body   any
	}{
		{method: http.MethodPost, path: "/api/admin/api-services/openapi/preview", body: map[string]any{"document": document}},
		{method: http.MethodPost, path: "/api/admin/api-services/openapi/import", body: map[string]any{"document": document}},
		{method: http.MethodGet, path: path},
		{method: http.MethodPut, path: path, body: openAPIUpdatePayload(response)},
	} {
		forbidden := reqHelper(srv, userJWT, request.method, request.path, request.body)
		require.Equal(t, http.StatusForbidden, forbidden.Code, "%s %s: %s", request.method, request.path, forbidden.Body.String())
	}

	servicePatch := reqHelper(srv, adminJWT, http.MethodPut, "/api/admin/api-services/"+strconv.FormatUint(uint64(result.ServiceID), 10), map[string]any{"openapi_document": map[string]any{}})
	require.Equal(t, http.StatusBadRequest, servicePatch.Code, servicePatch.Body.String())
	require.Equal(t, "openapi_document_requires_dedicated_endpoint", responseCodeFromRecorder(t, servicePatch))
	routePatch := reqHelper(srv, adminJWT, http.MethodPut, "/api/admin/api-routes/"+strconv.FormatUint(uint64(result.RouteIDs[0]), 10), map[string]any{"openapi_paths": map[string]any{}})
	require.Equal(t, http.StatusBadRequest, routePatch.Code, routePatch.Body.String())
	require.Equal(t, "openapi_document_requires_dedicated_endpoint", responseCodeFromRecorder(t, routePatch))

	getDTO := reqHelper(srv, adminJWT, http.MethodPut, path, map[string]any{"service": response.Service, "routes": response.Routes})
	require.Equal(t, http.StatusBadRequest, getDTO.Code, getDTO.Body.String())

	payload := openAPIUpdatePayload(response)
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "trailing JSON", body: append(append([]byte(nil), encoded...), []byte(`{}`)...)},
		{name: "invalid UTF-8", body: []byte{'{', '"', 's', 'e', 'r', 'v', 'i', 'c', 'e', '"', ':', '"', 0xff, '"', '}'}},
		{name: "invalid surrogate", body: []byte(`{"service":{"id":1,"updated_at":1,"document":{"info":{"title":"\ud800"}}},"routes":[]}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := rawOpenAPIRequest(srv.Router, adminJWT, http.MethodPut, path, test.body, false, false)
			require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
		})
	}

	for _, request := range []struct {
		name    string
		method  string
		path    string
		chunked bool
	}{
		{name: "import unknown length", method: http.MethodPost, path: "/api/admin/api-services/openapi/import"},
		{name: "PUT chunked", method: http.MethodPut, path: path, chunked: true},
	} {
		t.Run(request.name, func(t *testing.T) {
			tooLarge := rawOpenAPIRequest(srv.Router, adminJWT, request.method, request.path, bytes.Repeat([]byte{' '}, apiopenapi.MaxRequestBodyBytes+1), true, request.chunked)
			require.Equal(t, http.StatusRequestEntityTooLarge, tooLarge.Code, tooLarge.Body.String())
			require.Equal(t, "request_too_large", responseCodeFromRecorder(t, tooLarge))
		})
	}

	put := reqHelper(srv, adminJWT, http.MethodPut, path, payload)
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
}

func openAPIUpdatePayload(response apiopenapi.DocumentResponse) map[string]any {
	routes := make([]map[string]any, 0, len(response.Routes))
	for _, route := range response.Routes {
		routes = append(routes, map[string]any{
			"id": route.ID, "updated_at": route.UpdatedAt, "paths": route.Paths,
		})
	}
	return map[string]any{
		"service": map[string]any{
			"id": response.Service.ID, "updated_at": response.Service.UpdatedAt, "document": response.Service.Document,
		},
		"routes": routes,
	}
}

func rawOpenAPIRequest(srv interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, jwtToken, method, path string, body []byte, unknownLength, chunked bool) *httptest.ResponseRecorder {
	request, _ := http.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+jwtToken)
	if unknownLength {
		request.ContentLength = -1
	}
	if chunked {
		request.TransferEncoding = []string{"chunked"}
	}
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, request)
	return response
}

func multiRouteOpenAPIRequestDocument() map[string]any {
	return map[string]any{
		"openapi": "3.1.0", "info": map[string]any{"title": "Wired API", "version": "1"},
		"servers": []any{map[string]any{"url": "https://wired.example.test"}},
		"paths": map[string]any{
			"/":      map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}},
			"/users": map[string]any{"post": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}},
		},
	}
}

func responseCodeFromRecorder(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body.Code
}
