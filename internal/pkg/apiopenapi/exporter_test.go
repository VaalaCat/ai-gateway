package apiopenapi

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestBuildDocumentExportsStandardSanitizedOpenAPI(t *testing.T) {
	parsed, err := ParseJSON(readFixture(t, "openapi-3.0.json"))
	require.NoError(t, err)
	users := parsed.Paths["/users"]
	users.Servers = []models.OpenAPIServer{{URL: "https://path-upstream.secret.test"}}
	get := users.Operations["GET"]
	get.Servers = []models.OpenAPIServer{{URL: "https://operation-upstream.secret.test"}}
	users.Operations["GET"] = get
	parsed.Paths["/users"] = users

	bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{Slug: "accounts", Paths: []string{"/users", "/users/{id}"}}})
	require.NoError(t, err)
	routes := bundleModelRoutes(bundle)
	routes[0].ExampleRequest = datatypes.NewJSONType(models.APIRequestExample{
		Method: "GET", Headers: map[string]string{"Authorization": "credential-secret"}, Body: "credential-secret",
	})
	service := models.APIService{
		Name: "Current User API", Description: "Current platform description",
		OpenAPIDocument: datatypes.NewJSONType(parsed.Document),
	}

	raw, err := BuildDocument(service, routes, "https://gateway.example.test/v1/api/user-api")
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	require.Contains(t, string(raw), "\n  \"info\"")
	require.NotContains(t, string(raw), "api.example.test")
	require.NotContains(t, string(raw), "path-upstream.secret.test")
	require.NotContains(t, string(raw), "operation-upstream.secret.test")
	require.NotContains(t, string(raw), "credential-secret")
	require.NotContains(t, string(raw), `"operations"`)
	require.NotContains(t, string(raw), `"extensions"`)

	var exported map[string]any
	require.NoError(t, json.Unmarshal(raw, &exported))
	require.Equal(t, "3.0.3", exported["openapi"])
	info := exported["info"].(map[string]any)
	require.Equal(t, "Current User API", info["title"])
	require.Equal(t, "Current platform description", info["description"])
	require.Equal(t, "identity", info["x-info-owner"])
	servers := exported["servers"].([]any)
	require.Len(t, servers, 1)
	require.Equal(t, "https://gateway.example.test/v1/api/user-api", servers[0].(map[string]any)["url"])
	require.Equal(t, "users-v1", exported["x-document-id"])

	paths := exported["paths"].(map[string]any)
	require.NotContains(t, paths, "/users")
	require.Contains(t, paths, "/accounts")
	require.Contains(t, paths, "/accounts/{id}")
	accounts := paths["/accounts"].(map[string]any)
	require.Contains(t, accounts, "get")
	require.Contains(t, accounts, "post")
	require.NotContains(t, accounts, "GET")
	require.Equal(t, "accounts", accounts[routeSlugExtension])
	require.Equal(t, "identity", accounts["x-path-owner"])
	require.NotContains(t, accounts, "servers")
	require.NotContains(t, accounts["get"].(map[string]any), "servers")
	require.Equal(t, "admin", accounts["get"].(map[string]any)["x-operation-audience"])

	reparsed, err := ParseJSON(raw)
	require.NoError(t, err)
	roundTrip, err := GroupRoutes(reparsed, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"accounts"}, roundTrip.RouteSlugs())
	require.Equal(t, []string{"GET", "POST"}, []string(roundTrip.Route("accounts").AllowedMethods))
	require.ElementsMatch(t, []string{"/accounts", "/accounts/{id}"}, mapKeys(roundTrip.Route("accounts").OpenAPIPaths.Data()))
}

func TestBuildDocumentRejectsDuplicatePublicPath(t *testing.T) {
	routes := []models.APIRoute{
		{
			Slug: "users", UpstreamPath: "/users",
			OpenAPIPaths: datatypes.NewJSONType(map[string]models.OpenAPIPathItem{"/users": pathWithMethods("GET")}),
		},
		{
			Slug: "", UpstreamPath: "",
			OpenAPIPaths: datatypes.NewJSONType(map[string]models.OpenAPIPathItem{"/users": pathWithMethods("POST")}),
		},
	}
	service := models.APIService{Name: "API", OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{
		Version: "3.1.0", Info: models.OpenAPIInfo{Version: "1"},
	})}
	_, err := BuildDocument(service, routes, "https://gateway.example.test")
	require.ErrorContains(t, err, "duplicate exported OpenAPI path")
}

func TestBuildDocumentRewritesLocalPathReferencesToPublicPaths(t *testing.T) {
	parsed, err := ParseJSON([]byte(`{
      "openapi":"3.1.0","info":{"title":"API","version":"1"},
      "paths":{
        "/users":{"get":{"operationId":"listUsers","responses":{"200":{"description":"ok","links":{"detail":{"operationRef":"#/paths/~1users~1{id}/get"}}}}}},
        "/users/{id}":{"$ref":"#/paths/~1users","get":{"operationId":"getUser","responses":{"200":{"description":"ok"}}}}
	  },
	  "components":{"links":{"UserDetail":{"operationRef":"#/paths/~1users~1{id}/get"}}}
    }`))
	require.NoError(t, err)
	bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{Slug: "accounts", Paths: []string{"/users", "/users/{id}"}}})
	require.NoError(t, err)
	service := models.APIService{
		Name: "API", OpenAPIDocument: datatypes.NewJSONType(parsed.Document),
	}

	raw, err := BuildDocument(service, bundleModelRoutes(bundle), "https://gateway.example.test")
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	paths := document["paths"].(map[string]any)
	require.Equal(t, "#/paths/~1accounts", paths["/accounts/{id}"].(map[string]any)["$ref"])
	link := paths["/accounts"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["links"].(map[string]any)["detail"].(map[string]any)
	require.Equal(t, "#/paths/~1accounts~1{id}/get", link["operationRef"])
	componentLink := document["components"].(map[string]any)["links"].(map[string]any)["UserDetail"].(map[string]any)
	require.Equal(t, "#/paths/~1accounts~1{id}/get", componentLink["operationRef"])

	reparsed, err := ParseJSON(raw)
	require.NoError(t, err)
	require.Contains(t, reparsed.Paths, "/accounts")
	require.Contains(t, reparsed.Paths, "/accounts/{id}")
}

func TestBuildDocumentRewritesUnevaluatedItemsPathReference(t *testing.T) {
	raw := buildRenamedSchemaReferenceDocument(t, `{"type":"array","unevaluatedItems":{"$ref":"#/paths/~1users/get/responses/200/content/application~1json/schema"}}`)
	require.Contains(t, string(raw), `"$ref": "#/paths/~1accounts/get/responses/200/content/application~1json/schema"`)
	_, err := ParseJSON(raw)
	require.NoError(t, err)
}

func TestBuildDocumentRewritesDynamicPathReference(t *testing.T) {
	raw := buildRenamedSchemaReferenceDocument(t, `{"$dynamicRef":"#/paths/~1users/get/responses/200/content/application~1json/schema"}`)
	require.Contains(t, string(raw), `"$dynamicRef": "#/paths/~1accounts/get/responses/200/content/application~1json/schema"`)
	_, err := ParseJSON(raw)
	require.NoError(t, err)
}

func TestBuildDocumentRewritesOnlyDocumentResourceSchemaPathReferences(t *testing.T) {
	parsed, err := ParseJSON([]byte(`{
      "openapi":"3.1.0","info":{"title":"API","version":"1"},
      "paths":{"/users":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"string"}}}}}}}},
      "components":{"schemas":{
        "Scoped":{
          "$id":"urn:example:scoped",
          "paths":{"/users":{"type":"string"}},
          "$defs":{
            "Ordinary":{"$ref":"#/paths/~1users"},
            "Dynamic":{"$dynamicRef":"#/paths/~1users"}
          }
        },
        "DocumentOrdinary":{"$ref":"#/paths/~1users/get/responses/200/content/application~1json/schema"},
        "DocumentDynamic":{"$dynamicRef":"#/paths/~1users/get/responses/200/content/application~1json/schema"}
      }}
    }`))
	require.NoError(t, err)
	bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{Slug: "accounts", Paths: []string{"/users"}}})
	require.NoError(t, err)
	raw, err := BuildDocument(models.APIService{
		Name: "API", OpenAPIDocument: datatypes.NewJSONType(parsed.Document),
	}, bundleModelRoutes(bundle), "https://gateway.example.test")
	require.NoError(t, err)

	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	scopedDefinitions := schemas["Scoped"].(map[string]any)["$defs"].(map[string]any)
	require.Equal(t, "#/paths/~1users", scopedDefinitions["Ordinary"].(map[string]any)["$ref"])
	require.Equal(t, "#/paths/~1users", scopedDefinitions["Dynamic"].(map[string]any)["$dynamicRef"])
	require.Equal(t, "#/paths/~1accounts/get/responses/200/content/application~1json/schema", schemas["DocumentOrdinary"].(map[string]any)["$ref"])
	require.Equal(t, "#/paths/~1accounts/get/responses/200/content/application~1json/schema", schemas["DocumentDynamic"].(map[string]any)["$dynamicRef"])

	_, err = ParseJSON(raw)
	require.NoError(t, err)
}

func TestBuildDocumentRewritesEncodingHeaderPathReference(t *testing.T) {
	parsed, err := ParseJSON([]byte(`{
      "openapi":"3.1.0","info":{"title":"API","version":"1"},
      "paths":{"/users":{"post":{
        "requestBody":{"content":{"multipart/form-data":{
          "schema":{"type":"object","properties":{"profile":{"type":"string"}}},
          "encoding":{"profile":{"headers":{"X-Trace":{"$ref":"#/paths/~1users/post/responses/200/headers/X-Trace"}}}}
        }}},
        "responses":{"200":{"description":"ok","headers":{"X-Trace":{"description":"trace","schema":{"type":"string"}}}}}
      }}}
    }`))
	require.NoError(t, err)
	bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{Slug: "accounts", Paths: []string{"/users"}}})
	require.NoError(t, err)
	raw, err := BuildDocument(models.APIService{
		Name: "API", OpenAPIDocument: datatypes.NewJSONType(parsed.Document),
	}, bundleModelRoutes(bundle), "https://gateway.example.test")
	require.NoError(t, err)
	require.Contains(t, string(raw), `"$ref": "#/paths/~1accounts/post/responses/200/headers/X-Trace"`)
	_, err = ParseJSON(raw)
	require.NoError(t, err)
}

func TestBuildDocumentRejectsExternalLinkOperationReference(t *testing.T) {
	service := models.APIService{
		Name: "API",
		OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{
			Version: "3.1.0", Info: models.OpenAPIInfo{Version: "1"},
			Components: models.OpenAPIComponents{Links: map[string]json.RawMessage{
				"External": json.RawMessage(`{"operationRef":"https://api.example.test/openapi.json#/paths/~1users/get"}`),
			}},
		}),
	}
	raw, err := BuildDocument(service, nil, "https://gateway.example.test")
	require.ErrorContains(t, err, "external operationRef")
	require.Nil(t, raw)
}

func TestBuildDocumentPreservesReferenceLikeExampleData(t *testing.T) {
	service := models.APIService{
		Name: "API",
		OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{
			Version: "3.1.0", Info: models.OpenAPIInfo{Version: "1"},
			Components: models.OpenAPIComponents{Schemas: map[string]json.RawMessage{
				"Data": json.RawMessage(`{"type":"object","default":{"$ref":"ordinary-data"}}`),
			}},
		}),
	}
	raw, err := BuildDocument(service, nil, "https://gateway.example.test")
	require.NoError(t, err)
	require.Contains(t, string(raw), `"$ref": "ordinary-data"`)
}

func TestBuildDocumentReturnsMarshalErrorsWithoutPanicking(t *testing.T) {
	invalid := json.RawMessage(`{`)
	tests := []struct {
		name    string
		service models.APIService
		routes  []models.APIRoute
	}{
		{
			name: "contact extension",
			service: models.APIService{Name: "API", OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{
				Info: models.OpenAPIInfo{Contact: &models.OpenAPIContact{Extensions: map[string]json.RawMessage{"x-invalid": invalid}}},
			})},
		},
		{
			name: "external docs extension",
			service: models.APIService{Name: "API", OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{
				ExternalDocs: &models.OpenAPIExternalDocs{URL: "https://docs.example.test", Extensions: map[string]json.RawMessage{"x-invalid": invalid}},
			})},
		},
		{
			name:    "media type schema",
			service: models.APIService{Name: "API"},
			routes: []models.APIRoute{{Slug: "items", UpstreamPath: "/items", OpenAPIPaths: datatypes.NewJSONType(map[string]models.OpenAPIPathItem{
				"/items": {Operations: map[string]models.OpenAPIOperation{"GET": {Responses: map[string]models.OpenAPIResponse{
					"200": {Description: "ok", Content: map[string]models.OpenAPIMediaType{"application/json": {Schema: invalid}}},
				}}}},
			})}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw []byte
			var err error
			require.NotPanics(t, func() {
				raw, err = BuildDocument(tt.service, tt.routes, "https://gateway.example.test")
			})
			require.Error(t, err)
			require.Nil(t, raw)
		})
	}
}

func TestEncodeHelpersReturnMarshalErrorsWithoutPanicking(t *testing.T) {
	invalidExtensions := map[string]json.RawMessage{"x-invalid": json.RawMessage(`{`)}
	tests := []struct {
		name   string
		encode func() (map[string]any, error)
	}{
		{name: "contact", encode: func() (map[string]any, error) {
			return encodeContact(models.OpenAPIContact{Extensions: invalidExtensions})
		}},
		{name: "external docs", encode: func() (map[string]any, error) {
			return encodeExternalDocs(models.OpenAPIExternalDocs{URL: "https://docs.example.test", Extensions: invalidExtensions})
		}},
		{name: "media type", encode: func() (map[string]any, error) {
			return encodeMediaType(models.OpenAPIMediaType{Extensions: invalidExtensions})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var encoded map[string]any
			var err error
			require.NotPanics(t, func() { encoded, err = tt.encode() })
			require.Error(t, err)
			require.Nil(t, encoded)
		})
	}
}

func TestBuildDocumentValidatesGatewayAndPlatformDocument(t *testing.T) {
	validService := models.APIService{Name: "API", OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{
		Version: "3.1.0", Info: models.OpenAPIInfo{Version: "1"},
	})}
	tests := []struct {
		name    string
		service models.APIService
		server  string
	}{
		{name: "relative gateway", service: validService, server: "/v1/api"},
		{name: "gateway credentials", service: validService, server: "https://user:secret@gateway.example.test"},
		{name: "unsupported stored version", service: models.APIService{Name: "API", OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{Version: "2.0"})}, server: "https://gateway.example.test"},
		{name: "empty service name", service: models.APIService{OpenAPIDocument: validService.OpenAPIDocument}, server: "https://gateway.example.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildDocument(tt.service, nil, tt.server)
			require.Error(t, err)
		})
	}
}

func TestBuildDocumentDefaultsEmptyDocumentation(t *testing.T) {
	raw, err := BuildDocument(models.APIService{Name: "Manual API"}, nil, "https://gateway.example.test")
	require.NoError(t, err)
	var document struct {
		OpenAPI string                     `json:"openapi"`
		Info    map[string]any             `json:"info"`
		Paths   map[string]json.RawMessage `json:"paths"`
		Servers []models.OpenAPIServer     `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(raw, &document))
	require.Equal(t, "3.1.0", document.OpenAPI)
	require.Equal(t, "1.0.0", document.Info["version"])
	require.Empty(t, document.Paths)
	require.Equal(t, "https://gateway.example.test", document.Servers[0].URL)
}

func bundleModelRoutes(bundle ImportBundle) []models.APIRoute {
	routes := make([]models.APIRoute, 0, len(bundle.Routes))
	for _, route := range bundle.Routes {
		routes = append(routes, route.APIRoute)
	}
	return routes
}

func buildRenamedSchemaReferenceDocument(t *testing.T, sourceSchema string) []byte {
	t.Helper()
	parsed, err := ParseJSON([]byte(`{
      "openapi":"3.1.0","info":{"title":"API","version":"1"},
      "paths":{
        "/users":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$dynamicAnchor":"node","type":"string"}}}}}}},
        "/users/list":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":` + sourceSchema + `}}}}}}
      }
    }`))
	require.NoError(t, err)
	bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{Slug: "accounts", Paths: []string{"/users", "/users/list"}}})
	require.NoError(t, err)
	raw, err := BuildDocument(models.APIService{
		Name: "API", OpenAPIDocument: datatypes.NewJSONType(parsed.Document),
	}, bundleModelRoutes(bundle), "https://gateway.example.test")
	require.NoError(t, err)
	return raw
}
