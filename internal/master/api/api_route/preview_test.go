package api_route

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	api_backend "github.com/VaalaCat/ai-gateway/internal/master/api/api_backend"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPreviewListsAllSavedTargetEndpointsWithoutPublishing(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := previewService("saved")
	require.NoError(t, db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, db.Create(&backend).Error)
	for _, upstream := range []models.APIUpstream{
		previewUpstream(backend.ID, "lower", "https://lower.example/base", 1, 2),
		previewUpstream(backend.ID, "higher", "https://higher.example/base?base=1", 9, 5),
	} {
		require.NoError(t, db.Create(&upstream).Error)
	}
	disabled := previewUpstream(backend.ID, "disabled", "https://disabled.example/base", 2, 9)
	require.NoError(t, db.Create(&disabled).Error)
	require.NoError(t, db.Model(&disabled).UpdateColumn("status", consts.StatusDisabled).Error)
	publisher := &targetRecordingPublisher{db: db}
	h := &Handler{App: application, Publisher: publisher}
	response, err := h.Preview(routeTargetManagerContext(t, db, application, service.ID), PreviewRequest{
		APIServiceID: service.ID, Slug: "forecast", UpstreamPath: "/weather", ForwardSubpath: true,
		Sample: models.APIRequestExample{Method: "GET", Subpath: "/today", Query: "city=Tokyo&city=Osaka"},
		Target: RouteTargetCommand{Mode: "existing", BackendID: backend.ID},
	})
	require.NoError(t, err)
	require.Equal(t, []PreviewEndpoint{
		{UpstreamID: 3, UpstreamName: "disabled", Status: consts.StatusDisabled, Priority: 9, Weight: 2, FinalURL: "https://disabled.example/base/weather/today?city=Tokyo&city=Osaka"},
		{UpstreamID: 2, UpstreamName: "higher", Status: consts.StatusEnabled, Priority: 5, Weight: 9, FinalURL: "https://higher.example/base/weather/today?base=1&city=Tokyo&city=Osaka"},
		{UpstreamID: 1, UpstreamName: "lower", Status: consts.StatusEnabled, Priority: 2, Weight: 1, FinalURL: "https://lower.example/base/weather/today?city=Tokyo&city=Osaka"},
	}, response.Endpoints)
	require.Empty(t, response.Diagnostics)
	require.Empty(t, publisher.events)
	var backendCount, upstreamCount int64
	require.NoError(t, db.Model(&models.APIBackend{}).Count(&backendCount).Error)
	require.NoError(t, db.Model(&models.APIUpstream{}).Count(&upstreamCount).Error)
	require.EqualValues(t, 1, backendCount)
	require.EqualValues(t, 3, upstreamCount)
}

func TestPreviewUsesDraftFirstUpstreamWithoutPersistingIt(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := previewService("draft")
	require.NoError(t, db.Create(&service).Error)
	publisher := &targetRecordingPublisher{db: db}
	h := &Handler{App: application, Publisher: publisher}
	response, err := h.Preview(routeTargetManagerContext(t, db, application, service.ID), PreviewRequest{
		APIServiceID: service.ID, Slug: "forecast", UpstreamPath: "/weather", ForwardSubpath: false,
		Sample: models.APIRequestExample{Method: "GET", Subpath: "/ignored", Query: "city=Tokyo"},
		Target: RouteTargetCommand{Mode: "create", Backend: &api_backend.CreateInput{Name: "draft-backend"}, FirstUpstream: &api_upstream.CreateInput{Name: "draft-origin", BaseURL: "https://draft.example/base", Weight: 3, Priority: 7}},
	})
	require.NoError(t, err)
	require.Equal(t, []PreviewEndpoint{{UpstreamID: 0, UpstreamName: "draft-origin", Status: consts.StatusEnabled, Priority: 7, Weight: 3, FinalURL: "https://draft.example/base/weather?city=Tokyo"}}, response.Endpoints)
	require.Empty(t, response.Diagnostics)
	require.Empty(t, publisher.events)
	var backendCount, upstreamCount int64
	require.NoError(t, db.Model(&models.APIBackend{}).Count(&backendCount).Error)
	require.NoError(t, db.Model(&models.APIUpstream{}).Count(&upstreamCount).Error)
	require.Zero(t, backendCount)
	require.Zero(t, upstreamCount)
}

func TestPreviewRejectsInvalidStaticConfiguration(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := previewService("mine")
	foreignService := previewService("foreign")
	require.NoError(t, db.Create(&service).Error)
	require.NoError(t, db.Create(&foreignService).Error)
	foreignBackend := models.APIBackend{APIServiceID: foreignService.ID, Name: "foreign"}
	require.NoError(t, db.Create(&foreignBackend).Error)
	h := &Handler{App: application}
	c := routeTargetManagerContext(t, db, application, service.ID)
	for _, test := range []struct {
		name       string
		req        PreviewRequest
		wantStatus int
		wantCode   string
	}{
		{name: "cross service backend", req: PreviewRequest{APIServiceID: service.ID, Slug: "forecast", Target: RouteTargetCommand{Mode: "existing", BackendID: foreignBackend.ID}}, wantStatus: 400, wantCode: "backend_service_mismatch"},
		{name: "unsafe sample URL", req: PreviewRequest{APIServiceID: service.ID, Slug: "forecast", UpstreamPath: "/weather", Sample: models.APIRequestExample{Method: "GET", Subpath: "//attacker.example"}, Target: RouteTargetCommand{Mode: "create", Backend: &api_backend.CreateInput{Name: "draft"}, FirstUpstream: &api_upstream.CreateInput{Name: "origin", BaseURL: "https://origin.example"}}}, wantStatus: 400, wantCode: "invalid_example_subpath"},
		{name: "unsafe upstream URL", req: PreviewRequest{APIServiceID: service.ID, Slug: "forecast", Target: RouteTargetCommand{Mode: "create", Backend: &api_backend.CreateInput{Name: "draft"}, FirstUpstream: &api_upstream.CreateInput{Name: "origin", BaseURL: "https://origin.example/%2fhidden"}}}, wantStatus: 400, wantCode: "preview_invalid_target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := h.Preview(c, test.req)
			require.Error(t, err)
			require.Empty(t, response.Endpoints)
			requirePreviewStatus(t, err, test.wantStatus)
			requirePreviewCode(t, err, test.wantCode)
		})
	}
}

func TestPreviewMapsMissingBackendToNotFound(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := previewService("not-found")
	require.NoError(t, db.Create(&service).Error)
	h := &Handler{App: application}
	_, err := h.Preview(routeTargetManagerContext(t, db, application, service.ID), PreviewRequest{
		APIServiceID: service.ID, Slug: "forecast", Target: RouteTargetCommand{Mode: "existing", BackendID: 999},
	})
	requirePreviewStatus(t, err, 404)
	requirePreviewCode(t, err, "backend_not_found")
}

func TestPreviewDiagnosesTargetWithoutEndpoints(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := previewService("empty")
	require.NoError(t, db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "empty"}
	require.NoError(t, db.Create(&backend).Error)
	h := &Handler{App: application}
	response, err := h.Preview(routeTargetManagerContext(t, db, application, service.ID), PreviewRequest{
		APIServiceID: service.ID, Slug: "forecast", Target: RouteTargetCommand{Mode: "existing", BackendID: backend.ID},
	})
	require.NoError(t, err)
	require.Empty(t, response.Endpoints)
	require.Equal(t, []string{"no_available_upstream"}, response.Diagnostics)
}

func TestPreviewAvailabilityDiagnosticUsesStableCode(t *testing.T) {
	req := PreviewRequest{Slug: "forecast"}
	for _, test := range []struct {
		name       string
		candidates []models.APIUpstream
		want       []string
	}{
		{name: "zero endpoints", candidates: nil, want: []string{"no_available_upstream"}},
		{name: "all disabled", candidates: []models.APIUpstream{
			{ID: 1, Name: "one", BaseURL: "https://one.example", Status: consts.StatusDisabled},
			{ID: 2, Name: "two", BaseURL: "https://two.example", Status: consts.StatusDisabled},
		}, want: []string{"no_available_upstream"}},
		{name: "mixed availability", candidates: []models.APIUpstream{
			{ID: 1, Name: "enabled", BaseURL: "https://enabled.example", Status: consts.StatusEnabled},
			{ID: 2, Name: "disabled", BaseURL: "https://disabled.example", Status: consts.StatusDisabled},
		}, want: []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := buildPreviewResponse(req, test.candidates)
			require.NoError(t, err)
			require.Equal(t, test.want, response.Diagnostics)
		})
	}
}

func TestPreviewRejectsUnsafePathBeforeListingEndpoints(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := previewService("unsafe-path")
	require.NoError(t, db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "empty"}
	require.NoError(t, db.Create(&backend).Error)
	h := &Handler{App: application}
	response, err := h.Preview(routeTargetManagerContext(t, db, application, service.ID), PreviewRequest{
		APIServiceID: service.ID, Slug: "forecast", UpstreamPath: "/%2fhidden",
		Target: RouteTargetCommand{Mode: "existing", BackendID: backend.ID},
	})
	require.Error(t, err)
	require.Empty(t, response.Endpoints)
}

func TestPreviewDraftCredentialValidationAndResponseRedaction(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := previewService("credential")
	require.NoError(t, db.Create(&service).Error)
	h := &Handler{App: application}
	c := routeTargetManagerContext(t, db, application, service.ID)
	for _, test := range []struct {
		name       string
		authType   models.APIUpstreamAuthType
		credential *api_upstream.APIUpstreamCredential
	}{
		{name: "missing bearer", authType: models.APIUpstreamAuthBearer},
		{name: "mismatched header", authType: models.APIUpstreamAuthHeader, credential: &api_upstream.APIUpstreamCredential{BearerToken: "wrong"}},
		{name: "missing query", authType: models.APIUpstreamAuthQuery},
		{name: "mismatched basic", authType: models.APIUpstreamAuthBasic, credential: &api_upstream.APIUpstreamCredential{BasicUsername: "user", BasicPassword: "secret", QueryName: "wrong"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := h.Preview(c, previewDraftRequest(service.ID, test.authType, test.credential, nil))
			requirePreviewStatus(t, err, 400)
		})
	}
	proxyURL := "http://proxy.example:3128"
	response, err := h.Preview(c, previewDraftRequest(service.ID, models.APIUpstreamAuthBearer, &api_upstream.APIUpstreamCredential{BearerToken: "bearer-secret"}, &proxyURL))
	require.NoError(t, err)
	body, err := json.Marshal(response)
	require.NoError(t, err)
	for _, secret := range []string{"bearer-secret", "credential", "proxy_url"} {
		require.NotContains(t, string(body), secret)
	}
}

func TestPreviewMapsDAOReadFailuresToInternalError(t *testing.T) {
	for _, test := range []struct {
		name  string
		table string
	}{
		{name: "backend get", table: "api_backends"},
		{name: "upstream list", table: "api_upstreams"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, application := routeTargetTestDB(t)
			service := previewService("dao-" + test.table)
			require.NoError(t, db.Create(&service).Error)
			backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
			require.NoError(t, db.Create(&backend).Error)
			if test.table == "api_upstreams" {
				upstream := previewUpstream(backend.ID, "origin", "https://origin.example", 1, 0)
				require.NoError(t, db.Create(&upstream).Error)
			}
			callbackName := "preview-fail-" + test.table
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == test.table {
					tx.AddError(errors.New("database unavailable"))
				}
			}))
			defer db.Callback().Query().Remove(callbackName)
			h := &Handler{App: application}
			_, err := h.Preview(routeTargetManagerContext(t, db, application, service.ID), PreviewRequest{APIServiceID: service.ID, Slug: "forecast", Target: RouteTargetCommand{Mode: "existing", BackendID: backend.ID}})
			requirePreviewStatus(t, err, 500)
		})
	}
}

func previewDraftRequest(serviceID uint, authType models.APIUpstreamAuthType, credential *api_upstream.APIUpstreamCredential, proxyURL *string) PreviewRequest {
	return PreviewRequest{APIServiceID: serviceID, Slug: "forecast", Target: RouteTargetCommand{Mode: "create", Backend: &api_backend.CreateInput{Name: "draft"}, FirstUpstream: &api_upstream.CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: authType, Credential: credential, ProxyURL: proxyURL}}}
}

func requirePreviewStatus(t *testing.T, err error, want int) {
	t.Helper()
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, want, apiErr.Status)
}

func requirePreviewCode(t *testing.T, err error, want string) {
	t.Helper()
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, want, apiErr.Code)
}

func previewService(slug string) models.APIService {
	return models.APIService{Slug: slug, Name: slug, Status: consts.StatusEnabled}
}

func previewUpstream(backendID uint, name, baseURL string, weight, priority int) models.APIUpstream {
	return models.APIUpstream{BackendID: backendID, Name: name, BaseURL: baseURL, Weight: weight, Priority: priority, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled}
}
