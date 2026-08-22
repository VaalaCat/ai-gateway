package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestOpenAPIFieldsRoundTripAndNormalizeOperationMethods(t *testing.T) {
	// A regression that drops any nested field, or persists lowercase method
	// keys, makes the stored platform document unusable by later builders.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))

	service := APIService{
		Slug: "users",
		Name: "Users",
		OpenAPIDocument: datatypes.NewJSONType(OpenAPIServiceDocument{
			Version: "3.1.0",
			Info: OpenAPIInfo{
				Version: "2026-08-18",
				Contact: &OpenAPIContact{Name: "API Team", Email: "api@example.test"},
			},
			Servers: []OpenAPIServer{{URL: "https://api.example.test", Description: "production"}},
			Components: OpenAPIComponents{
				Schemas: map[string]json.RawMessage{"User": json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}}}`)},
			},
			Extensions: map[string]json.RawMessage{"x-service-tier": json.RawMessage(`"gold"`)},
		}),
	}
	require.NoError(t, core.Create(&service).Error)
	backend := APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)

	route := APIRoute{
		APIServiceID: service.ID,
		BackendID:    backend.ID,
		Slug:         "users",
		OpenAPIPaths: datatypes.NewJSONType(map[string]OpenAPIPathItem{
			"/users/{id}": {
				Ref:         "#/components/pathItems/UserLookup",
				Summary:     "User lookup",
				Description: "Looks up one user by ID.",
				Parameters: []OpenAPIParameter{{
					Name: "id", In: "path", Required: true,
					Schema: json.RawMessage(`{"type":"integer"}`),
				}},
				Servers: []OpenAPIServer{{
					URL:         "https://{region}.example.test/{version}",
					Description: "regional endpoint",
					Variables: map[string]OpenAPIServerVariable{
						"region": {
							Default: "us-east", Enum: []string{"us-east", "eu-west"}, Description: "deployment region",
							Extensions: map[string]json.RawMessage{"x-variable-source": json.RawMessage(`"catalog"`)},
						},
						"version": {Default: "v1"},
					},
					Extensions: map[string]json.RawMessage{"x-server-owner": json.RawMessage(`"identity"`)},
				}},
				Operations: map[string]OpenAPIOperation{
					"get": {
						OperationID: "getUser",
						RequestBody: &OpenAPIRequestBody{
							Required: true,
							Content: map[string]OpenAPIMediaType{
								"application/json": {Schema: json.RawMessage(`{"$ref":"#/components/schemas/User"}`)},
							},
						},
						Responses: map[string]OpenAPIResponse{
							"200": {Description: "ok"},
						},
					},
				},
				Extensions: map[string]json.RawMessage{"x-path-owner": json.RawMessage(`"identity"`)},
			},
		}),
	}
	require.NoError(t, core.Create(&route).Error)

	var loadedService APIService
	require.NoError(t, core.First(&loadedService, service.ID).Error)
	require.Equal(t, "3.1.0", loadedService.OpenAPIDocument.Data().Version)
	require.Equal(t, "API Team", loadedService.OpenAPIDocument.Data().Info.Contact.Name)
	require.Equal(t, "https://api.example.test", loadedService.OpenAPIDocument.Data().Servers[0].URL)
	require.Equal(t, "production", loadedService.OpenAPIDocument.Data().Servers[0].Description)
	require.JSONEq(t, `{"type":"object","properties":{"id":{"type":"integer"}}}`, string(loadedService.OpenAPIDocument.Data().Components.Schemas["User"]))
	require.JSONEq(t, `"gold"`, string(loadedService.OpenAPIDocument.Data().Extensions["x-service-tier"]))

	var loadedRoute APIRoute
	require.NoError(t, core.First(&loadedRoute, route.ID).Error)
	path := loadedRoute.OpenAPIPaths.Data()["/users/{id}"]
	require.Equal(t, "#/components/pathItems/UserLookup", path.Ref)
	require.Equal(t, "User lookup", path.Summary)
	require.Equal(t, "Looks up one user by ID.", path.Description)
	require.Len(t, path.Servers, 1)
	require.Equal(t, "https://{region}.example.test/{version}", path.Servers[0].URL)
	require.Equal(t, "regional endpoint", path.Servers[0].Description)
	require.Equal(t, "us-east", path.Servers[0].Variables["region"].Default)
	require.Equal(t, []string{"us-east", "eu-west"}, path.Servers[0].Variables["region"].Enum)
	require.Equal(t, "deployment region", path.Servers[0].Variables["region"].Description)
	require.JSONEq(t, `"catalog"`, string(path.Servers[0].Variables["region"].Extensions["x-variable-source"]))
	require.Equal(t, "v1", path.Servers[0].Variables["version"].Default)
	require.JSONEq(t, `"identity"`, string(path.Servers[0].Extensions["x-server-owner"]))
	require.NotContains(t, path.Operations, "get")
	operation := path.Operations["GET"]
	require.Equal(t, "getUser", operation.OperationID)
	require.NotNil(t, operation.RequestBody)
	require.True(t, operation.RequestBody.Required)
	require.JSONEq(t, `{"$ref":"#/components/schemas/User"}`, string(operation.RequestBody.Content["application/json"].Schema))
	require.Equal(t, "ok", operation.Responses["200"].Description)
	require.JSONEq(t, `{"type":"integer"}`, string(path.Parameters[0].Schema))
	require.JSONEq(t, `"identity"`, string(path.Extensions["x-path-owner"]))
}

func TestOpenAPI31FidelityFieldsRoundTrip(t *testing.T) {
	document := OpenAPIServiceDocument{
		Version:           "3.1.1",
		JSONSchemaDialect: "https://json-schema.org/draft/2020-12/schema",
		Info:              OpenAPIInfo{Summary: "Short summary", Version: "1"},
	}
	header := OpenAPIHeader{AllowEmptyValue: true, AllowReserved: true}

	rawDocument, err := json.Marshal(document)
	require.NoError(t, err)
	var decodedDocument OpenAPIServiceDocument
	require.NoError(t, json.Unmarshal(rawDocument, &decodedDocument))
	require.Equal(t, document.JSONSchemaDialect, decodedDocument.JSONSchemaDialect)
	require.Equal(t, "Short summary", decodedDocument.Info.Summary)

	rawHeader, err := json.Marshal(header)
	require.NoError(t, err)
	var decodedHeader OpenAPIHeader
	require.NoError(t, json.Unmarshal(rawHeader, &decodedHeader))
	require.True(t, decodedHeader.AllowEmptyValue)
	require.True(t, decodedHeader.AllowReserved)
}

func TestOpenAPIRouteRejectsDuplicateNormalizedOperationMethods(t *testing.T) {
	// Silently overwriting either "get" or "GET" would lose an operation.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{Slug: "duplicates", Name: "Duplicates"}
	require.NoError(t, core.Create(&service).Error)
	backend := APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)

	err := core.Create(&APIRoute{
		APIServiceID: service.ID,
		BackendID:    backend.ID,
		Slug:         "duplicates",
		OpenAPIPaths: datatypes.NewJSONType(map[string]OpenAPIPathItem{
			"/duplicates": {Operations: map[string]OpenAPIOperation{
				"get": {OperationID: "lowercase"},
				"GET": {OperationID: "uppercase"},
			}},
		}),
	}).Error
	require.ErrorContains(t, err, "duplicate OpenAPI operation method")
}

func TestOpenAPIServiceComponentPathItemsNormalizeOperationMethodsOnWrite(t *testing.T) {
	// Components.PathItems uses the same platform path-item contract as routes;
	// create and full-object updates must not persist a second method-key format.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{
		Slug: "component-methods",
		Name: "Component Methods",
		OpenAPIDocument: datatypes.NewJSONType(OpenAPIServiceDocument{
			Components: OpenAPIComponents{PathItems: map[string]OpenAPIPathItem{
				"User": {Operations: map[string]OpenAPIOperation{
					"get": {OperationID: "getUser"},
				}},
			}},
		}),
	}
	require.NoError(t, core.Create(&service).Error)

	var created APIService
	require.NoError(t, core.First(&created, service.ID).Error)
	createdOperations := created.OpenAPIDocument.Data().Components.PathItems["User"].Operations
	require.NotContains(t, createdOperations, "get")
	require.Equal(t, "getUser", createdOperations["GET"].OperationID)

	document := created.OpenAPIDocument.Data()
	document.Components.PathItems = map[string]OpenAPIPathItem{
		"Order": {Operations: map[string]OpenAPIOperation{
			"post": {OperationID: "createOrder"},
		}},
	}
	created.OpenAPIDocument = datatypes.NewJSONType(document)
	require.NoError(t, core.Save(&created).Error)

	var updated APIService
	require.NoError(t, core.First(&updated, service.ID).Error)
	updatedOperations := updated.OpenAPIDocument.Data().Components.PathItems["Order"].Operations
	require.NotContains(t, updatedOperations, "post")
	require.Equal(t, "createOrder", updatedOperations["POST"].OperationID)
}

func TestOpenAPIServiceRejectsDuplicateNormalizedComponentOperationMethods(t *testing.T) {
	// Accepting both keys loses one operation as soon as a consumer normalizes
	// the shared path item to the platform's uppercase method contract.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))

	err := core.Create(&APIService{
		Slug: "component-duplicates",
		Name: "Component Duplicates",
		OpenAPIDocument: datatypes.NewJSONType(OpenAPIServiceDocument{
			Components: OpenAPIComponents{PathItems: map[string]OpenAPIPathItem{
				"Duplicate": {Operations: map[string]OpenAPIOperation{
					"get": {OperationID: "lowercase"},
					"GET": {OperationID: "uppercase"},
				}},
			}},
		}),
	}).Error
	require.ErrorContains(t, err, "duplicate OpenAPI operation method")
}

func TestOpenAPIFieldsDefaultToEmptyAndStayOutOfModelJSON(t *testing.T) {
	// Existing hand-authored rows must not become SQL/JSON null, and existing
	// management responses must not expose the large document columns.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{Slug: "manual", Name: "Manual"}
	require.NoError(t, core.Create(&service).Error)
	backend := APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)
	route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "manual"}
	require.NoError(t, core.Create(&route).Error)

	var loadedService APIService
	require.NoError(t, core.First(&loadedService, service.ID).Error)
	require.Equal(t, OpenAPIServiceDocument{}, loadedService.OpenAPIDocument.Data())
	var loadedRoute APIRoute
	require.NoError(t, core.First(&loadedRoute, route.ID).Error)
	require.Empty(t, loadedRoute.OpenAPIPaths.Data())

	serviceJSON, err := json.Marshal(loadedService)
	require.NoError(t, err)
	require.NotContains(t, string(serviceJSON), "openapi_document")
	routeJSON, err := json.Marshal(loadedRoute)
	require.NoError(t, err)
	require.NotContains(t, string(routeJSON), "openapi_paths")
}
