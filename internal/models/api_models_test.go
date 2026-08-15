package models

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestAPIRouteJSONAndUniqueConstraints(t *testing.T) {
	// This catches route writes that either skip the service-scoped slug index or
	// persist an empty protocol set instead of the contract default ["http"].
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))

	service := APIService{Slug: "weather", Name: "Weather", PricePerCall: 100000}
	require.NoError(t, core.Create(&service).Error)
	backend := APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)

	route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast"}
	require.NoError(t, core.Create(&route).Error)

	var got APIRoute
	require.NoError(t, core.First(&got, route.ID).Error)
	require.Equal(t, datatypes.JSONSlice[APIProtocol]{APIProtocolHTTP}, got.Protocols)
	require.Empty(t, got.AllowedMethods, "an empty method set means all standard methods")
	require.Empty(t, got.WebSocketSubprotocols)

	err := core.Create(&APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast"}).Error
	require.Error(t, err, "the same service cannot contain the same route slug twice")

	secondService := APIService{Slug: "news", Name: "News"}
	require.NoError(t, core.Create(&secondService).Error)
	secondBackend := APIBackend{APIServiceID: secondService.ID, Name: "primary"}
	require.NoError(t, core.Create(&secondBackend).Error)
	require.NoError(t, core.Create(&APIRoute{
		APIServiceID:          secondService.ID,
		BackendID:             secondBackend.ID,
		Slug:                  "forecast",
		Protocols:             datatypes.JSONSlice[APIProtocol]{APIProtocolWebSocket, APIProtocolHTTP},
		AllowedMethods:        datatypes.JSONSlice[string]{"post", "GET", "POST"},
		WebSocketSubprotocols: datatypes.JSONSlice[string]{"chat.v2", "chat.v1", "chat.v2"},
	}).Error)
	var normalized APIRoute
	require.NoError(t, core.Where("api_service_id = ? AND slug = ?", secondService.ID, "forecast").First(&normalized).Error)
	require.Equal(t, datatypes.JSONSlice[APIProtocol]{APIProtocolHTTP, APIProtocolWebSocket}, normalized.Protocols)
	require.Equal(t, datatypes.JSONSlice[string]{"GET", "POST"}, normalized.AllowedMethods)
	require.Equal(t, datatypes.JSONSlice[string]{"chat.v1", "chat.v2"}, normalized.WebSocketSubprotocols)
}

func TestAPIRoutingTargetModels(t *testing.T) {
	// This catches a schema that leaves routes/upstreams directly attached to a
	// service, permits zero backend ownership, or writes an absent request
	// example as SQL/JSON null.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))

	serviceA := APIService{Slug: "weather", Name: "Weather", Status: 1}
	serviceB := APIService{Slug: "maps", Name: "Maps", Status: 1}
	require.NoError(t, core.Create(&serviceA).Error)
	require.NoError(t, core.Create(&serviceB).Error)

	backendA := APIBackend{APIServiceID: serviceA.ID, Name: "primary"}
	backendB := APIBackend{APIServiceID: serviceB.ID, Name: "primary"}
	require.NoError(t, core.Create(&backendA).Error)
	require.NoError(t, core.Create(&backendB).Error)
	require.Error(t, core.Create(&APIBackend{APIServiceID: serviceA.ID, Name: "primary"}).Error)

	require.Error(t, core.Create(&APIRoute{APIServiceID: serviceA.ID, Slug: "missing-backend"}).Error)
	require.Error(t, core.Create(&APIUpstream{Name: "missing-backend", BaseURL: "https://api.weather.test", Weight: 1, AuthType: APIUpstreamAuthNone}).Error)

	route := APIRoute{APIServiceID: serviceA.ID, BackendID: backendA.ID, Slug: "forecast"}
	require.NoError(t, core.Create(&route).Error)
	upstream := APIUpstream{BackendID: backendA.ID, Name: "upstream-a", BaseURL: "https://api.weather.test", Weight: 1, AuthType: APIUpstreamAuthNone}
	require.NoError(t, core.Create(&upstream).Error)
	require.Error(t, core.Model(&APIRoute{}).Where("id = ?", route.ID).Update("backend_id", backendB.ID).Error)
	require.Error(t, core.Model(&APIUpstream{}).Where("id = ?", upstream.ID).Update("backend_id", backendB.ID).Error)

	var reloaded APIRoute
	require.NoError(t, core.First(&reloaded, route.ID).Error)
	require.Equal(t, APIRequestExample{}, reloaded.ExampleRequest.Data())
}

func TestAPIRequestExampleNormalization(t *testing.T) {
	allowed := datatypes.JSONSlice[string]{"GET", "POST"}
	valid := APIRequestExample{
		Method:  "post",
		Subpath: "/v1/forecast/hourly",
		Query:   "city=Tokyo&units=metric",
		Headers: map[string]string{"Accept": "application/json", "X-Client": "console"},
		Body:    `{"days":3}`,
	}

	normalized, err := NormalizeAPIRequestExample(valid, allowed)
	require.NoError(t, err)
	require.Equal(t, "POST", normalized.Method)
	require.Equal(t, valid.Subpath, normalized.Subpath)

	for _, tc := range []struct {
		name    string
		example APIRequestExample
		code    string
	}{
		{name: "method outside allowed methods", example: APIRequestExample{Method: "DELETE"}, code: "invalid_example_method"},
		{name: "parent traversal", example: APIRequestExample{Method: "GET", Subpath: "/../admin"}, code: "invalid_example_subpath"},
		{name: "absolute URL", example: APIRequestExample{Method: "GET", Subpath: "https://attacker.example/x"}, code: "invalid_example_subpath"},
		{name: "backslash", example: APIRequestExample{Method: "GET", Subpath: `/a\\b`}, code: "invalid_example_subpath"},
		{name: "multi encoded traversal", example: APIRequestExample{Method: "GET", Subpath: "/%252e%252e/admin"}, code: "invalid_example_subpath"},
		{name: "multi encoded query delimiter", example: APIRequestExample{Method: "GET", Subpath: "/%253fadmin=1"}, code: "invalid_example_subpath"},
		{name: "multi encoded fragment delimiter", example: APIRequestExample{Method: "GET", Subpath: "/%2523fragment"}, code: "invalid_example_subpath"},
		{name: "authorization header case insensitive", example: APIRequestExample{Method: "GET", Headers: map[string]string{"aUtHoRiZaTiOn": "Bearer secret"}}, code: "invalid_example_header"},
		{name: "host header", example: APIRequestExample{Method: "GET", Headers: map[string]string{"Host": "attacker.example"}}, code: "invalid_example_header"},
		{name: "connection header", example: APIRequestExample{Method: "GET", Headers: map[string]string{"Connection": "X-Token"}}, code: "invalid_example_header"},
		{name: "gateway internal header", example: APIRequestExample{Method: "GET", Headers: map[string]string{"X-Vaala-Agent": "internal"}}, code: "invalid_example_header"},
		{name: "token bearing header", example: APIRequestExample{Method: "GET", Headers: map[string]string{"X-Api-Key": "secret"}}, code: "invalid_example_header"},
		{name: "secret header case insensitive", example: APIRequestExample{Method: "GET", Headers: map[string]string{"x-SeCrEt": "secret"}}, code: "invalid_example_header"},
		{name: "access key header", example: APIRequestExample{Method: "GET", Headers: map[string]string{"X-Access-Key": "secret"}}, code: "invalid_example_header"},
		{name: "signature header", example: APIRequestExample{Method: "GET", Headers: map[string]string{"x-HuB-sIgNaTuRe-256": "secret"}}, code: "invalid_example_header"},
		{name: "API token header case insensitive", example: APIRequestExample{Method: "GET", Headers: map[string]string{"x-aPi-ToKeN": "secret"}}, code: "invalid_example_header"},
		{name: "session token header case insensitive", example: APIRequestExample{Method: "GET", Headers: map[string]string{"X-SeSsIoN-tOkEn": "secret"}}, code: "invalid_example_header"},
		{name: "bearer token header case insensitive", example: APIRequestExample{Method: "GET", Headers: map[string]string{"x-BeArEr-ToKeN": "secret"}}, code: "invalid_example_header"},
		{name: "unprefixed API token header", example: APIRequestExample{Method: "GET", Headers: map[string]string{"Api-Token": "secret"}}, code: "invalid_example_header"},
		{name: "case insensitive duplicate", example: APIRequestExample{Method: "GET", Headers: map[string]string{"X-Client": "a", "x-client": "b"}}, code: "invalid_example_header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeAPIRequestExample(tc.example, allowed)
			require.ErrorContains(t, err, tc.code)
		})
	}

	ordinaryTokenHeaders := APIRequestExample{Method: "GET", Headers: map[string]string{
		"x-tOkEnIzE": "enabled", "x-ToKeN-bUcKeT": "blue",
	}}
	ordinaryTokenHeaders, err = NormalizeAPIRequestExample(ordinaryTokenHeaders, allowed)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"X-Tokenize": "enabled", "X-Token-Bucket": "blue"}, ordinaryTokenHeaders.Headers)

	invalidUTF8 := string([]byte{0xff})
	for _, tc := range []struct {
		name    string
		example APIRequestExample
	}{
		{name: "method", example: APIRequestExample{Method: invalidUTF8}},
		{name: "subpath", example: APIRequestExample{Method: "GET", Subpath: invalidUTF8}},
		{name: "query", example: APIRequestExample{Method: "GET", Query: invalidUTF8}},
		{name: "header name", example: APIRequestExample{Method: "GET", Headers: map[string]string{invalidUTF8: "value"}}},
		{name: "header value", example: APIRequestExample{Method: "GET", Headers: map[string]string{"X-Client": invalidUTF8}}},
		{name: "body", example: APIRequestExample{Method: "GET", Body: invalidUTF8}},
	} {
		t.Run("invalid UTF-8 "+tc.name, func(t *testing.T) {
			_, err := NormalizeAPIRequestExample(tc.example, allowed)
			require.ErrorContains(t, err, "invalid_example_encoding")
		})
	}

	empty, err := NormalizeAPIRequestExample(APIRequestExample{}, allowed)
	require.NoError(t, err)
	require.Equal(t, APIRequestExample{}, empty)

	boundary := APIRequestExample{Method: "GET"}
	encodedBoundary, err := json.Marshal(boundary)
	require.NoError(t, err)
	boundary.Body = strings.Repeat("x", MaxAPIRequestExampleBytes-len(encodedBoundary))
	withinLimit, err := NormalizeAPIRequestExample(boundary, allowed)
	require.NoError(t, err)
	require.NotEmpty(t, withinLimit.Body)
	boundary.Body += "x"
	_, err = NormalizeAPIRequestExample(boundary, allowed)
	require.ErrorContains(t, err, "example_too_large")
}

func TestAPIRequestExampleJSONAlwaysUsesAnObjectForHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		example APIRequestExample
		want    string
	}{
		{
			name: "zero value",
			want: `{"method":"","subpath":"","query":"","headers":{},"body":""}`,
		},
		{
			name:    "request without headers",
			example: APIRequestExample{Method: "GET", Subpath: "forecast", Headers: map[string]string{}},
			want:    `{"method":"GET","subpath":"forecast","query":"","headers":{},"body":""}`,
		},
		{
			name:    "request with headers",
			example: APIRequestExample{Method: "POST", Headers: map[string]string{"Content-Type": "application/json"}},
			want:    `{"method":"POST","subpath":"","query":"","headers":{"Content-Type":"application/json"},"body":""}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.example)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
		})
	}
}

func TestAPIRouteExampleRequestGuardsEveryModelWrite(t *testing.T) {
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{Slug: "example-guard", Name: "Example Guard"}
	require.NoError(t, core.Create(&service).Error)
	backend := APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)

	forbidden := APIRequestExample{Method: "GET", Headers: map[string]string{"Authorization": "Bearer secret"}}
	require.ErrorContains(t, core.Create(&APIRoute{
		APIServiceID: service.ID, BackendID: backend.ID, Slug: "forbidden-create", ExampleRequest: datatypes.NewJSONType(forbidden),
	}).Error, "invalid_example_header")

	route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "safe"}
	require.NoError(t, core.Create(&route).Error)
	route.ExampleRequest = datatypes.NewJSONType(forbidden)
	require.ErrorContains(t, core.Save(&route).Error, "invalid_example_header")
	require.Error(t, core.Model(&APIRoute{}).Where("id = ?", route.ID).Update("example_request", datatypes.NewJSONType(forbidden)).Error)

	var persisted APIRoute
	require.NoError(t, core.First(&persisted, route.ID).Error)
	require.Equal(t, APIRequestExample{}, persisted.ExampleRequest.Data())
}

func TestAPIModelFullSaveRejectsCrossServiceRelationships(t *testing.T) {
	// This catches Save bypassing the partial-update guards and changing a
	// persisted route/upstream to a backend owned by another service.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	serviceA := APIService{Slug: "save-weather", Name: "Weather"}
	serviceB := APIService{Slug: "save-maps", Name: "Maps"}
	require.NoError(t, core.Create(&serviceA).Error)
	require.NoError(t, core.Create(&serviceB).Error)
	backendA := APIBackend{APIServiceID: serviceA.ID, Name: "primary"}
	backendB := APIBackend{APIServiceID: serviceB.ID, Name: "primary"}
	require.NoError(t, core.Create(&backendA).Error)
	require.NoError(t, core.Create(&backendB).Error)
	route := APIRoute{APIServiceID: serviceA.ID, BackendID: backendA.ID, Slug: "forecast"}
	upstream := APIUpstream{BackendID: backendA.ID, Name: "primary", BaseURL: "https://api.weather.test", Weight: 1, AuthType: APIUpstreamAuthNone}
	require.NoError(t, core.Create(&route).Error)
	require.NoError(t, core.Create(&upstream).Error)
	require.Error(t, core.Model(&APIRoute{}).Where("id = ?", route.ID).Update("api_service_id", serviceB.ID).Error)

	route.BackendID = backendB.ID
	require.Error(t, core.Save(&route).Error)
	upstream.BackendID = backendB.ID
	require.Error(t, core.Save(&upstream).Error)
	backendA.APIServiceID = serviceB.ID
	require.Error(t, core.Save(&backendA).Error)

	var persistedRoute APIRoute
	var persistedUpstream APIUpstream
	require.NoError(t, core.First(&persistedRoute, route.ID).Error)
	require.NoError(t, core.First(&persistedUpstream, upstream.ID).Error)
	require.Equal(t, backendA.ID, persistedRoute.BackendID)
	require.Equal(t, backendA.ID, persistedUpstream.BackendID)
}

func TestAPIRoutePartialStatusUpdateDoesNotRequireFullRow(t *testing.T) {
	// This catches a full-object GORM hook rejecting a legal partial update
	// because Model(&APIRoute{}) has zero values for unrelated fields.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{Slug: "partial-update", Name: "Partial Update"}
	require.NoError(t, core.Create(&service).Error)
	backend := APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)
	route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "status"}
	require.NoError(t, core.Create(&route).Error)

	require.NoError(t, core.Model(&APIRoute{}).Where("id = ?", route.ID).Update("status", 0).Error)
	var got APIRoute
	require.NoError(t, core.First(&got, route.ID).Error)
	require.Zero(t, got.Status)
}

func createAPIBackendForTest(t *testing.T, db *gorm.DB, serviceID uint) APIBackend {
	t.Helper()
	backend := APIBackend{APIServiceID: serviceID, Name: "primary"}
	require.NoError(t, db.Create(&backend).Error)
	return backend
}

func TestAPIModelsRejectInvalidPartialUpdates(t *testing.T) {
	// These are the public GORM partial-update forms used by future DAOs. They
	// must validate the written field without requiring unrelated row fields.
	tests := []struct {
		name   string
		update func(*testing.T) error
	}{
		{
			name: "service rejects invalid status",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-service-status", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				return core.Model(&APIService{}).Where("id = ?", service.ID).Update("status", 2).Error
			},
		},
		{
			name: "service rejects uppercase slug",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-service-slug", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				return core.Model(&APIService{}).Where("id = ?", service.ID).Updates(map[string]any{"slug": "UpperCase"}).Error
			},
		},
		{
			name: "route rejects invalid status and slug",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-route", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				backend := createAPIBackendForTest(t, core, service.ID)
				route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route"}
				require.NoError(t, core.Create(&route).Error)
				return core.Model(&APIRoute{}).Where("id = ?", route.ID).Updates(map[string]any{"status": 2, "slug": "UpperCase"}).Error
			},
		},
		{
			name: "route rejects partial protocols",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-route-protocols", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				backend := createAPIBackendForTest(t, core, service.ID)
				route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route"}
				require.NoError(t, core.Create(&route).Error)
				return core.Model(&APIRoute{}).Where("id = ?", route.ID).Update("protocols", datatypes.JSONSlice[APIProtocol]{"grpc"}).Error
			},
		},
		{
			name: "route rejects partial methods",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-route-methods", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				backend := createAPIBackendForTest(t, core, service.ID)
				route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route"}
				require.NoError(t, core.Create(&route).Error)
				return core.Model(&APIRoute{}).Where("id = ?", route.ID).Update("allowed_methods", datatypes.JSONSlice[string]{"CONNECT"}).Error
			},
		},
		{
			name: "route rejects otherwise valid partial protocols",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-route-valid-protocols", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				backend := createAPIBackendForTest(t, core, service.ID)
				route := APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route"}
				require.NoError(t, core.Create(&route).Error)
				return core.Model(&APIRoute{}).Where("id = ?", route.ID).Update("protocols", datatypes.JSONSlice[APIProtocol]{APIProtocolWebSocket}).Error
			},
		},
		{
			name: "upstream rejects relative URL",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-upstream-url", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				backend := createAPIBackendForTest(t, core, service.ID)
				upstream := APIUpstream{BackendID: backend.ID, Name: "upstream", BaseURL: "https://upstream.example", Weight: 1, AuthType: APIUpstreamAuthNone}
				require.NoError(t, core.Create(&upstream).Error)
				return core.Model(&APIUpstream{}).Where("id = ?", upstream.ID).Update("base_url", "/relative").Error
			},
		},
		{
			name: "upstream rejects zero weight",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-upstream-weight", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				backend := createAPIBackendForTest(t, core, service.ID)
				upstream := APIUpstream{BackendID: backend.ID, Name: "upstream", BaseURL: "https://upstream.example", Weight: 1, AuthType: APIUpstreamAuthNone}
				require.NoError(t, core.Create(&upstream).Error)
				return core.Model(&APIUpstream{}).Where("id = ?", upstream.ID).Update("weight", 0).Error
			},
		},
		{
			name: "upstream rejects unknown auth type",
			update: func(t *testing.T) error {
				core := openSplitTestDB(t)
				require.NoError(t, MigrateCoreDB(core))
				service := APIService{Slug: "partial-upstream-auth", Name: "Service"}
				require.NoError(t, core.Create(&service).Error)
				backend := createAPIBackendForTest(t, core, service.ID)
				upstream := APIUpstream{BackendID: backend.ID, Name: "upstream", BaseURL: "https://upstream.example", Weight: 1, AuthType: APIUpstreamAuthNone}
				require.NoError(t, core.Create(&upstream).Error)
				return core.Model(&APIUpstream{}).Where("id = ?", upstream.ID).Updates(map[string]any{"auth_type": "oauth"}).Error
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, tt.update(t))
		})
	}
}

func TestAPIModelSaveRejectsInvalidCompleteObject(t *testing.T) {
	// This catches a complete-object Save path bypassing the same model contract
	// after partial updates gained a field-level validation hook.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{Slug: "save-validation", Name: "Service"}
	require.NoError(t, core.Create(&service).Error)
	service.Status = 2
	require.Error(t, core.Save(&service).Error)
}

func TestAPIModelFullObjectUpdatesRejectInvalidContract(t *testing.T) {
	// This catches the complete-object Updates form silently skipping validation
	// even when BeforeUpdate already protects Save and map-style partial writes.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{Slug: "updates-validation", Name: "Service"}
	require.NoError(t, core.Create(&service).Error)
	service.Status = 2
	require.Error(t, core.Model(&service).Updates(&service).Error)
}

func TestAPIMapPatchValidationMatchesGormAssignments(t *testing.T) {
	// This catches patch validation diverging from GORM's actual SET list: an
	// omitted/unselected bad value must not reject a safe write, while aliases
	// for one DB column must never leave the final SQL assignment ambiguous.
	tests := []struct {
		name string
		slug string
		run  func(*testing.T, *gorm.DB, APIService)
	}{
		{
			name: "omit ignores invalid status that is not written",
			slug: "map-patch-omit",
			run: func(t *testing.T, db *gorm.DB, service APIService) {
				require.NoError(t, db.Model(&APIService{}).Where("id = ?", service.ID).
					Omit("status").Updates(map[string]any{"name": "Renamed", "status": 2}).Error)
				var got APIService
				require.NoError(t, db.First(&got, service.ID).Error)
				require.Equal(t, "Renamed", got.Name)
				require.Equal(t, 1, got.Status)
			},
		},
		{
			name: "select ignores invalid status that is not written",
			slug: "map-patch-select",
			run: func(t *testing.T, db *gorm.DB, service APIService) {
				require.NoError(t, db.Model(&APIService{}).Where("id = ?", service.ID).
					Select("name").Updates(map[string]any{"name": "Selected", "status": 2}).Error)
				var got APIService
				require.NoError(t, db.First(&got, service.ID).Error)
				require.Equal(t, "Selected", got.Name)
				require.Equal(t, 1, got.Status)
			},
		},
		{
			name: "conflicting aliases are rejected before SQL",
			slug: "map-patch-alias",
			run: func(t *testing.T, db *gorm.DB, service APIService) {
				for range 32 {
					err := db.Model(&APIService{}).Where("id = ?", service.ID).Updates(map[string]any{"Status": 0, "status": 2}).Error
					require.EqualError(t, err, "api model update has duplicate aliases for one field")
					var got APIService
					require.NoError(t, db.First(&got, service.ID).Error)
					require.Equal(t, 1, got.Status)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openSplitTestDB(t)
			require.NoError(t, MigrateCoreDB(db))
			service := APIService{Slug: tt.slug, Name: "Original"}
			require.NoError(t, db.Create(&service).Error)
			tt.run(t, db, service)
		})
	}
}

func TestAPIStructPatchValidationMatchesGormAssignments(t *testing.T) {
	// This catches struct patch validation treating zero-value fields omitted by
	// GORM as assignments. Explicit Select must still make a zero value present,
	// while Omit must exclude even a non-zero invalid value.
	tests := []struct {
		name string
		slug string
		run  func(*testing.T, *gorm.DB, APIService)
	}{
		{
			name: "unselected zero fields are not validated",
			slug: "struct-patch-name",
			run: func(t *testing.T, db *gorm.DB, service APIService) {
				require.NoError(t, db.Model(&APIService{}).Where("id = ?", service.ID).
					Updates(APIService{Name: "renamed"}).Error)
				var got APIService
				require.NoError(t, db.First(&got, service.ID).Error)
				require.Equal(t, "renamed", got.Name)
				require.Equal(t, service.Slug, got.Slug)
				require.Equal(t, 1, got.Status)
			},
		},
		{
			name: "unselected zero status is not written",
			slug: "struct-patch-zero",
			run: func(t *testing.T, db *gorm.DB, service APIService) {
				require.NoError(t, db.Model(&APIService{}).Where("id = ?", service.ID).
					Updates(APIService{Status: 0}).Error)
				var got APIService
				require.NoError(t, db.First(&got, service.ID).Error)
				require.Equal(t, 1, got.Status)
			},
		},
		{
			name: "selected zero status is validated and written",
			slug: "struct-patch-select",
			run: func(t *testing.T, db *gorm.DB, service APIService) {
				require.NoError(t, db.Model(&APIService{}).Where("id = ?", service.ID).
					Select("status").Updates(APIService{Status: 0}).Error)
				var got APIService
				require.NoError(t, db.First(&got, service.ID).Error)
				require.Zero(t, got.Status)
			},
		},
		{
			name: "omitted non-zero status is not validated or written",
			slug: "struct-patch-omit",
			run: func(t *testing.T, db *gorm.DB, service APIService) {
				require.NoError(t, db.Model(&APIService{}).Where("id = ?", service.ID).
					Omit("status").Updates(APIService{Status: 2, Name: "renamed"}).Error)
				var got APIService
				require.NoError(t, db.First(&got, service.ID).Error)
				require.Equal(t, "renamed", got.Name)
				require.Equal(t, 1, got.Status)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openSplitTestDB(t)
			require.NoError(t, MigrateCoreDB(db))
			service := APIService{Slug: tt.slug, Name: "Original"}
			require.NoError(t, db.Create(&service).Error)
			tt.run(t, db, service)
		})
	}
}

func TestAPIUpstreamCredentialColumnsNeverUseJSON(t *testing.T) {
	// This catches accidental JSON encoding of opaque encrypted blobs: a quoted
	// ciphertext would no longer be readable by the crypto layer unchanged.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))

	service := APIService{Slug: "weather", Name: "Weather"}
	require.NoError(t, core.Create(&service).Error)
	backend := createAPIBackendForTest(t, core, service.ID)
	upstream := APIUpstream{
		BackendID:            backend.ID,
		Name:                 "primary",
		BaseURL:              "https://upstream.example/v1",
		Weight:               1,
		Priority:             -10,
		AuthType:             APIUpstreamAuthBearer,
		CredentialCiphertext: "v1:credential:ciphertext",
		ProxyURLCiphertext:   "v1:proxy:ciphertext",
		HeaderOverride:       datatypes.NewJSONType(map[string]string{"X-Tenant": "west"}),
	}
	require.NoError(t, core.Create(&upstream).Error)

	var storedCredential, storedProxy string
	require.NoError(t, core.Table("api_upstreams").Select("credential_ciphertext, proxy_url_ciphertext").Where("id = ?", upstream.ID).Row().Scan(&storedCredential, &storedProxy))
	require.Equal(t, "v1:credential:ciphertext", storedCredential)
	require.Equal(t, "v1:proxy:ciphertext", storedProxy)

	var got APIUpstream
	require.NoError(t, core.First(&got, upstream.ID).Error)
	require.Equal(t, -10, got.Priority)
	require.Equal(t, map[string]string{"X-Tenant": "west"}, got.HeaderOverride.Data())
}

func TestAPICoreModelCreateValidation(t *testing.T) {
	// Each case exercises the real GORM create boundary. These catch malformed
	// admin writes before a future DAO persists an unusable gateway configuration.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	service := APIService{Slug: "validation", Name: "Validation"}
	require.NoError(t, core.Create(&service).Error)
	backend := createAPIBackendForTest(t, core, service.ID)
	role := Role{Key: "validation-role", Name: "Validation Role"}
	require.NoError(t, core.Create(&role).Error)

	tests := []struct {
		name   string
		create func() error
	}{
		{
			name: "service rejects uppercase slug",
			create: func() error {
				return core.Create(&APIService{Slug: "Invalid", Name: "Invalid"}).Error
			},
		},
		{
			name: "service rejects negative price",
			create: func() error {
				return core.Create(&APIService{Slug: "negative-price", Name: "Negative", PricePerCall: -1}).Error
			},
		},
		{
			name: "route rejects unknown protocol",
			create: func() error {
				return core.Create(&APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "unknown-protocol", Protocols: datatypes.JSONSlice[APIProtocol]{"grpc"}}).Error
			},
		},
		{
			name: "route rejects CONNECT method",
			create: func() error {
				return core.Create(&APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "connect", AllowedMethods: datatypes.JSONSlice[string]{"CONNECT"}}).Error
			},
		},
		{
			name: "upstream rejects relative URL",
			create: func() error {
				return core.Create(&APIUpstream{BackendID: backend.ID, Name: "relative", BaseURL: "/v1", Weight: 1}).Error
			},
		},
		{
			name: "upstream rejects zero weight",
			create: func() error {
				return core.Create(&APIUpstream{BackendID: backend.ID, Name: "zero-weight", BaseURL: "https://upstream.example", Weight: 0}).Error
			},
		},
		{
			name: "upstream rejects unknown auth type",
			create: func() error {
				return core.Create(&APIUpstream{BackendID: backend.ID, Name: "unknown-auth", BaseURL: "https://upstream.example", Weight: 1, AuthType: "oauth"}).Error
			},
		},
		{
			name: "permission rejects unknown resource",
			create: func() error {
				return core.Create(&Permission{Resource: "channel", Action: APIPermissionInvoke}).Error
			},
		},
		{
			name: "role rejects unknown status",
			create: func() error {
				return core.Create(&Role{Key: "invalid-role-status", Name: "Invalid", Status: 2}).Error
			},
		},
		{
			name: "permission rejects unknown action",
			create: func() error {
				return core.Create(&Permission{Resource: APIResourceService, Action: "write"}).Error
			},
		},
		{
			name: "binding rejects unknown principal",
			create: func() error {
				return core.Create(&RoleBinding{PrincipalType: "service", PrincipalID: 1, RoleID: role.ID}).Error
			},
		},
		{
			name: "binding rejects zero principal ID",
			create: func() error {
				return core.Create(&RoleBinding{PrincipalType: APIPrincipalUser, PrincipalID: 0, RoleID: role.ID}).Error
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, tt.create())
		})
	}
}

func TestAPIRoleKindDefaultsToCustomAndBuildsManagedKey(t *testing.T) {
	// This catches a migration or create hook that leaves ordinary Roles without
	// an ownership kind, letting them bypass the custom/managed API boundary.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	require.NoError(t, MigrateCoreDB(core))

	role := Role{Key: "kind-default", Name: "Kind default"}
	require.NoError(t, core.Create(&role).Error)
	var stored Role
	require.NoError(t, core.First(&stored, role.ID).Error)
	require.Equal(t, APIRoleKindCustom, stored.Kind)
	require.Equal(t, "managed:user:42", ManagedAPIRoleKey(APIPrincipalUser, 42))
}

func TestAPIPermissionMatrix(t *testing.T) {
	// Break caught: reintroducing control-plane read/manage resources into the
	// relay contract lets an ordinary Role carry permissions Agents must ignore.
	valid := []Permission{
		{Resource: APIResourceService, ResourceID: 0, Action: APIPermissionInvoke},
		{Resource: APIResourceService, ResourceID: 7, Action: APIPermissionInvoke},
		{Resource: APIResourceRoute, ResourceID: 9, Action: APIPermissionInvoke},
	}
	for _, permission := range valid {
		require.NoError(t, permission.Validate(), "%s/%s", permission.Resource, permission.Action)
	}

	invalid := []Permission{
		{Resource: APIResourceRoute, ResourceID: 0, Action: APIPermissionInvoke},
		{Resource: APIResourceService, Action: "read"},
		{Resource: APIResourceService, Action: "manage"},
		{Resource: APIResourceRoute, Action: "manage"},
		{Resource: "api_request_log", Action: "read"},
		{Resource: "api_upstream", Action: "manage"},
		{Resource: "", Action: APIPermissionInvoke},
		{Resource: APIResourceService, Action: ""},
	}
	for _, permission := range invalid {
		require.Error(t, permission.Validate(), "%s/%s", permission.Resource, permission.Action)
	}
}

func TestAPIPermissionFreshSchemaAcceptsOnlyInvokeResources(t *testing.T) {
	// Break caught: model validation can be bypassed by raw writes, so a fresh
	// database must enforce the same invoke-only contract itself.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))

	valid := []Permission{
		{Resource: APIResourceService, ResourceID: 0, Action: APIPermissionInvoke},
		{Resource: APIResourceService, ResourceID: 7, Action: APIPermissionInvoke},
		{Resource: APIResourceRoute, ResourceID: 9, Action: APIPermissionInvoke},
	}
	for _, permission := range valid {
		require.NoError(t, core.Exec(
			"INSERT INTO permissions (resource, resource_id, action, created_at) VALUES (?, ?, ?, ?)",
			permission.Resource, permission.ResourceID, permission.Action, 1,
		).Error, "%s/%d/%s", permission.Resource, permission.ResourceID, permission.Action)
	}

	invalid := []Permission{
		{Resource: APIResourceRoute, ResourceID: 0, Action: APIPermissionInvoke},
		{Resource: APIResourceService, ResourceID: 1, Action: "read"},
		{Resource: APIResourceService, ResourceID: 2, Action: "manage"},
		{Resource: APIResourceRoute, ResourceID: 3, Action: "manage"},
		{Resource: "api_request_log", ResourceID: 4, Action: "read"},
		{Resource: "api_upstream", ResourceID: 5, Action: "manage"},
		{Resource: "", ResourceID: 6, Action: APIPermissionInvoke},
		{Resource: APIResourceService, ResourceID: 8, Action: ""},
	}
	for _, permission := range invalid {
		require.Error(t, core.Exec(
			"INSERT INTO permissions (resource, resource_id, action, created_at) VALUES (?, ?, ?, ?)",
			permission.Resource, permission.ResourceID, permission.Action, 1,
		).Error, "%s/%d/%s", permission.Resource, permission.ResourceID, permission.Action)
	}
}

func TestAPIPermissionDatabaseConstraintRejectsPartialInvalidUpdate(t *testing.T) {
	// Break caught: partial Updates can otherwise bypass the full-object hook and
	// reintroduce a control-plane action into an invoke-only permission row.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	permission := Permission{Resource: APIResourceRoute, ResourceID: 7, Action: APIPermissionInvoke}
	require.NoError(t, core.Create(&permission).Error)

	require.Error(t, core.Model(&Permission{}).Where("id = ?", permission.ID).Update("action", "manage").Error)
	require.Error(t, core.Model(&Permission{}).Where("id = ?", permission.ID).Update("resource", "api_request_log").Error)
	require.Error(t, core.Model(&Permission{}).Where("id = ?", permission.ID).Update("resource_id", 0).Error)
	require.Error(t, core.Model(&Permission{}).Where("id = ?", permission.ID).Updates(map[string]any{
		"resource": APIResourceService, "action": "manage",
	}).Error)
	require.NoError(t, core.Model(&Permission{}).Where("id = ?", permission.ID).Update("resource", APIResourceService).Error)
	require.Error(t, core.Model(&Permission{}).Where("id = ?", permission.ID).Update("action", "").Error)
	var stored Permission
	require.NoError(t, core.First(&stored, permission.ID).Error)
	require.Equal(t, APIResourceService, stored.Resource)
	require.Equal(t, APIPermissionInvoke, stored.Action)
}

func TestAPIRBACCompositeUniqueConstraints(t *testing.T) {
	// This catches missing composite indexes that would duplicate a principal's
	// effective grant or create ambiguous duplicate permission rows.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))
	permission := Permission{Resource: APIResourceService, ResourceID: 0, Action: APIPermissionInvoke}
	require.NoError(t, core.Create(&permission).Error)
	require.Error(t, core.Create(&Permission{Resource: APIResourceService, ResourceID: 0, Action: APIPermissionInvoke}).Error)
	role := Role{Key: "binding-role", Name: "Binding Role"}
	require.NoError(t, core.Create(&role).Error)
	require.NoError(t, core.Create(&RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.Error(t, core.Create(&RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	binding := RoleBinding{PrincipalType: APIPrincipalUser, PrincipalID: 42, RoleID: role.ID}
	require.NoError(t, core.Create(&binding).Error)
	require.Error(t, core.Create(&RoleBinding{PrincipalType: APIPrincipalUser, PrincipalID: 42, RoleID: role.ID}).Error)
}

func TestAPIRequestLogRoundTripAndRequestIDUnique(t *testing.T) {
	// This catches log-schema drift that drops dispatch/limiter facts, byte
	// counters, or request identity uniqueness while preserving a migratable row.
	logDB := openSplitTestDB(t)
	require.NoError(t, MigrateLogDB(logDB))
	closeCode := 1000
	entry := APIRequestLog{
		RequestID:             "api-request-round-trip",
		UserID:                12,
		TokenID:               34,
		ClientIP:              "203.0.113.8",
		APIServiceID:          1,
		APIServiceName:        "Weather",
		APIRouteID:            2,
		APIRouteName:          "Forecast",
		APIUpstreamID:         3,
		APIUpstreamName:       "Primary",
		Protocol:              APIProtocolHTTP,
		Method:                "POST",
		Subpath:               "/daily",
		SourceAgentID:         "agent-edge",
		ExecutionAgentID:      "agent-east",
		AgentRouteID:          4,
		AgentRoutePath:        "token/model",
		StatusCode:            200,
		DurationMs:            210,
		FirstByteMs:           32,
		RequestBytes:          1024,
		ResponseBytes:         2048,
		WebSocketCloseCode:    &closeCode,
		ProviderDispatchKnown: true,
		ProviderDispatched:    true,
		QuotaGateDecision:     "allowed",
		ErrorStage:            "",
		ErrorCode:             "",
		RateLimitDecision:     "allow",
		RateLimitWaitMs:       7,
		RateLimitReason:       "matched global limiter",
		RateLimitHits: datatypes.JSONSlice[RateLimitHit]{
			{LimiterID: 9, Name: "global", Dimension: "rate/shared", Bucket: "global", Decision: "allow", WaitMs: 7},
		},
		UnitPrice: 100000,
		TotalCost: 100000,
	}
	require.NoError(t, logDB.Create(&entry).Error)

	var got APIRequestLog
	require.NoError(t, logDB.First(&got, entry.ID).Error)
	require.Equal(t, entry.RequestID, got.RequestID)
	require.Equal(t, entry.UserID, got.UserID)
	require.Equal(t, entry.ClientIP, got.ClientIP)
	require.Equal(t, entry.AgentRoutePath, got.AgentRoutePath)
	require.Equal(t, entry.StatusCode, got.StatusCode)
	require.Equal(t, entry.RequestBytes, got.RequestBytes)
	require.Equal(t, entry.ResponseBytes, got.ResponseBytes)
	require.Equal(t, entry.ProviderDispatchKnown, got.ProviderDispatchKnown)
	require.Equal(t, entry.ProviderDispatched, got.ProviderDispatched)
	require.Equal(t, entry.RateLimitHits, got.RateLimitHits)
	require.NotNil(t, got.WebSocketCloseCode)
	require.Equal(t, closeCode, *got.WebSocketCloseCode)
	require.Error(t, logDB.Create(&APIRequestLog{RequestID: entry.RequestID}).Error)
}

func TestTokenAPIRoleModeDefaultsToInheritAndPersistsExplicit(t *testing.T) {
	// This catches a migration default that would turn pre-existing tokens into
	// explicit mode and unintentionally remove their inherited permissions.
	core := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(core))

	inherit := Token{Key: "sk-api-role-inherit", Name: "inherit"}
	require.NoError(t, core.Create(&inherit).Error)
	var got Token
	require.NoError(t, core.First(&got, inherit.ID).Error)
	require.Equal(t, APIRoleModeInherit, got.APIRoleMode)

	explicit := Token{Key: "sk-api-role-explicit", Name: "explicit", APIRoleMode: APIRoleModeExplicit}
	require.NoError(t, core.Create(&explicit).Error)
	got = Token{}
	require.NoError(t, core.First(&got, explicit.ID).Error)
	require.Equal(t, APIRoleModeExplicit, got.APIRoleMode)
}
