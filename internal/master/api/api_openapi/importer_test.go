package api_openapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	coreopenapi "github.com/VaalaCat/ai-gateway/internal/pkg/apiopenapi"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Break caught: import must persist one coherent Service -> Backend ->
// Upstream -> Routes graph and keep plaintext credentials out of every row
// except the encrypted Upstream credential field.
func TestImporterCreatesGraphAndStoresCredentialOnlyOnUpstream(t *testing.T) {
	db, application := openAPITestDB(t)
	cipher := openAPITestCipher(t)
	selected := 1
	credential := api_upstream.APIUpstreamCredential{BearerToken: "import-secret"}
	importer := Importer{UpstreamCreator: api_upstream.Creator{Cipher: cipher}}

	result, err := importer.Import(dao.NewContext(application), ImportCommand{
		Document: fixtureDocument(t, "openapi-3.0.json"), Slug: "imported-users", SelectedServer: &selected,
		BackendName: "primary", Upstream: UpstreamDraft{Name: "origin", AuthType: models.APIUpstreamAuthBearer, Credential: &credential},
	})
	require.NoError(t, err)
	require.NotZero(t, result.ServiceID)
	require.NotZero(t, result.BackendID)
	require.NotZero(t, result.UpstreamID)
	require.NotEmpty(t, result.RouteIDs)

	var service models.APIService
	require.NoError(t, db.First(&service, result.ServiceID).Error)
	require.Equal(t, "imported-users", service.Slug)
	require.Equal(t, "3.0.3", service.OpenAPIDocument.Data().Version)
	var backend models.APIBackend
	require.NoError(t, db.First(&backend, result.BackendID).Error)
	require.Equal(t, service.ID, backend.APIServiceID)
	var upstream models.APIUpstream
	require.NoError(t, db.First(&upstream, result.UpstreamID).Error)
	require.Equal(t, "https://backup.example.test/v1", upstream.BaseURL)
	require.NotContains(t, upstream.CredentialCiphertext, credential.BearerToken)
	decrypted, err := api_upstream.DecryptAPIUpstreamCredential(cipher, upstream.ID, upstream.AuthType, upstream.CredentialCiphertext)
	require.NoError(t, err)
	require.Equal(t, credential, decrypted)
	var routes []models.APIRoute
	require.NoError(t, db.Where("api_service_id = ?", service.ID).Find(&routes).Error)
	require.Len(t, routes, len(result.RouteIDs))
	for _, route := range routes {
		require.Equal(t, backend.ID, route.BackendID)
		require.NotEmpty(t, route.OpenAPIPaths.Data())
	}
	allRows, err := json.Marshal([]any{service, backend, routes})
	require.NoError(t, err)
	require.NotContains(t, string(allRows), credential.BearerToken)
}

func TestImporterRollsBackEveryEntityOnRouteOrCredentialFailure(t *testing.T) {
	t.Run("second route", func(t *testing.T) {
		db, application := openAPITestDB(t)
		creates := 0
		callback := "fail-second-openapi-route"
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "api_routes" {
				creates++
				if creates == 2 {
					tx.AddError(errors.New("second route unavailable"))
				}
			}
		}))
		defer db.Callback().Create().Remove(callback)
		selected := 0
		_, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: multiRouteDocument(), Slug: "rollback-routes", SelectedServer: &selected,
				BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
			})
		require.Error(t, err)
		require.Equal(t, [4]int64{}, openAPIEntityCounts(t, db))
	})

	t.Run("credential", func(t *testing.T) {
		db, application := openAPITestDB(t)
		selected := 0
		credential := api_upstream.APIUpstreamCredential{BearerToken: "cannot-encrypt"}
		_, err := (Importer{UpstreamCreator: api_upstream.Creator{}}).Import(dao.NewContext(application), ImportCommand{
			Document: fixtureDocument(t, "openapi-3.1.json"), Slug: "rollback-credential", SelectedServer: &selected,
			BackendName: "primary", Upstream: UpstreamDraft{Name: "origin", AuthType: models.APIUpstreamAuthBearer, Credential: &credential},
		})
		require.Error(t, err)
		require.Equal(t, [4]int64{}, openAPIEntityCounts(t, db))
	})
}

func TestImporterRejectsInvalidServerAndSlugChoicesWithoutMerging(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*gorm.DB)
		command   ImportCommand
		wantCode  string
		wantCount [4]int64
	}{
		{
			name: "missing selected server", command: ImportCommand{
				Document: fixtureDocument(t, "openapi-3.0.json"), Slug: "missing-server", BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
			}, wantCode: "server_selection_required",
		},
		{
			name: "unknown selected server", command: func() ImportCommand {
				index := 20
				return ImportCommand{Document: fixtureDocument(t, "openapi-3.0.json"), Slug: "unknown-server", SelectedServer: &index, BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"}}
			}(), wantCode: "selected_server_not_found",
		},
		{
			name: "existing service slug", prepare: func(db *gorm.DB) {
				require.NoError(t, db.Create(&models.APIService{Slug: "same-slug", Name: "Existing"}).Error)
			}, command: func() ImportCommand {
				index := 1
				return ImportCommand{Document: fixtureDocument(t, "openapi-3.0.json"), Slug: "same-slug", SelectedServer: &index, BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"}}
			}(), wantCode: "service_slug_conflict", wantCount: [4]int64{1, 0, 0, 0},
		},
		{
			name: "same route slug", command: func() ImportCommand {
				index := 0
				return ImportCommand{Document: multiRouteDocument(), Slug: "same-route-slug", SelectedServer: &index, BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"}, Choices: []coreopenapi.RouteGroupChoice{{Slug: "shared", Paths: []string{"/users", "/orders"}}}}
			}(), wantCode: "invalid_openapi",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, application := openAPITestDB(t)
			if test.prepare != nil {
				test.prepare(db)
			}
			_, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(dao.NewContext(application), test.command)
			requireAPIErrorCode(t, err, test.wantCode)
			require.Equal(t, test.wantCount, openAPIEntityCounts(t, db))
		})
	}
}

func TestImporterHandlesManualVariableAndRootRouteChoices(t *testing.T) {
	t.Run("no server uses manual base URL", func(t *testing.T) {
		db, application := openAPITestDB(t)
		document := json.RawMessage(`{
		  "openapi":"3.1.0","info":{"title":"Manual Server","version":"1"},
		  "paths":{"/users":{"get":{"responses":{"200":{"description":"ok"}}}}}
		}`)
		result, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: document, Slug: "manual-server", BackendName: "primary",
				Upstream: UpstreamDraft{Name: "origin", BaseURL: "https://manual.example.test/v1"},
			})
		require.NoError(t, err)
		var upstream models.APIUpstream
		require.NoError(t, db.First(&upstream, result.UpstreamID).Error)
		require.Equal(t, "https://manual.example.test/v1", upstream.BaseURL)
	})

	t.Run("server variables use defaults", func(t *testing.T) {
		db, application := openAPITestDB(t)
		selected := 0
		document := json.RawMessage(`{
		  "openapi":"3.1.0","info":{"title":"Variable Server","version":"1"},
		  "servers":[{"url":"https://{region}.example.test/{version}","variables":{"region":{"default":"eu"},"version":{"default":"v2"}}}],
		  "paths":{"/users":{"get":{"responses":{"200":{"description":"ok"}}}}}
		}`)
		result, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: document, Slug: "variable-server", SelectedServer: &selected,
				BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
			})
		require.NoError(t, err)
		var upstream models.APIUpstream
		require.NoError(t, db.First(&upstream, result.UpstreamID).Error)
		require.Equal(t, "https://eu.example.test/v2", upstream.BaseURL)
	})

	t.Run("explicit root route choice", func(t *testing.T) {
		db, application := openAPITestDB(t)
		selected := 0
		result, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: multiRouteDocument(), Slug: "explicit-root", SelectedServer: &selected,
				BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
				Choices: []coreopenapi.RouteGroupChoice{{Slug: "", Paths: []string{"/users", "/orders"}}},
			})
		require.NoError(t, err)
		require.Len(t, result.RouteIDs, 1)
		var route models.APIRoute
		require.NoError(t, db.First(&route, result.RouteIDs[0]).Error)
		require.Empty(t, route.Slug)
		require.Empty(t, route.UpstreamPath)
		require.Len(t, route.OpenAPIPaths.Data(), 3)
	})

	t.Run("one route slug cannot cover unique upstream prefixes", func(t *testing.T) {
		db, application := openAPITestDB(t)
		selected := 0
		_, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: multiRouteDocument(), Slug: "route-slug-conflict", SelectedServer: &selected,
				BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
				Choices: []coreopenapi.RouteGroupChoice{{Slug: "shared", Paths: []string{"/users", "/orders"}}},
			})
		requireAPIErrorCode(t, err, "invalid_openapi")
		require.Equal(t, [4]int64{}, openAPIEntityCounts(t, db))
	})
}

func TestImporterPublishFailureReturnsSyncErrorAfterCommit(t *testing.T) {
	db, application := openAPITestDB(t)
	selected := 0
	publisher := &openAPIFailingPublisher{err: errors.New("bus unavailable")}
	_, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}, Publisher: publisher}).Import(
		dao.NewContext(application), ImportCommand{
			Document: fixtureDocument(t, "openapi-3.1.json"), Slug: "publish-failure", SelectedServer: &selected,
			BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
		})
	requireAPIErrorCode(t, err, "sync_publish_failed")
	counts := openAPIEntityCounts(t, db)
	require.EqualValues(t, 1, counts[0])
	require.EqualValues(t, 1, counts[1])
	require.EqualValues(t, 1, counts[2])
	require.Positive(t, counts[3])
	require.NotEmpty(t, publisher.actions)
}

func TestImporterSeparatesInvalidInputFromUnexpectedDatabaseFailure(t *testing.T) {
	t.Run("invalid backend", func(t *testing.T) {
		db, application := openAPITestDB(t)
		selected := 0
		_, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: multiRouteDocument(), Slug: "invalid-backend", SelectedServer: &selected,
				Upstream: UpstreamDraft{Name: "origin"},
			})
		requireAPIErrorStatus(t, err, http.StatusBadRequest)
		require.Equal(t, [4]int64{}, openAPIEntityCounts(t, db))
	})

	t.Run("invalid upstream", func(t *testing.T) {
		db, application := openAPITestDB(t)
		document := json.RawMessage(`{"openapi":"3.1.0","info":{"title":"No Server","version":"1"},"paths":{"/users":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
		_, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: document, Slug: "invalid-upstream", BackendName: "primary",
				Upstream: UpstreamDraft{Name: "origin", BaseURL: "://not-a-url"},
			})
		requireAPIErrorStatus(t, err, http.StatusBadRequest)
		require.Equal(t, [4]int64{}, openAPIEntityCounts(t, db))
	})

	t.Run("database failure", func(t *testing.T) {
		db, application := openAPITestDB(t)
		secret := "database topology secret"
		callback := "openapi:fail-service-create"
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "api_services" {
				tx.AddError(errors.New(secret))
			}
		}))
		t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
		selected := 0
		_, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
			dao.NewContext(application), ImportCommand{
				Document: multiRouteDocument(), Slug: "database-failure", SelectedServer: &selected,
				BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
			})
		apiErr := requireAPIErrorStatus(t, err, http.StatusInternalServerError)
		require.NotContains(t, apiErr.Message, secret)
		status, body := (api.DefaultErrorMapper{}).Map(err)
		external, marshalErr := json.Marshal(body)
		require.NoError(t, marshalErr)
		require.Equal(t, http.StatusInternalServerError, status)
		require.NotContains(t, string(external), secret)
		require.Equal(t, [4]int64{}, openAPIEntityCounts(t, db))
	})
}

type openAPIFailingPublisher struct {
	err     error
	actions []string
}

func (p *openAPIFailingPublisher) PublishService(_ context.Context, action string, _ models.APIService) error {
	p.actions = append(p.actions, "service:"+action)
	return p.err
}

func (p *openAPIFailingPublisher) PublishUpstream(_ context.Context, action string, _ models.APIUpstream) error {
	p.actions = append(p.actions, "upstream:"+action)
	return p.err
}

func (p *openAPIFailingPublisher) PublishRoute(_ context.Context, action string, _ models.APIRoute) error {
	p.actions = append(p.actions, "route:"+action)
	return p.err
}

func openAPITestCipher(t *testing.T) *byokcrypto.Cipher {
	t.Helper()
	cipher, err := byokcrypto.NewFromConfig("", "openapi-import-test-secret")
	require.NoError(t, err)
	return cipher
}

func requireAPIErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, want, apiErr.Code)
}

func requireAPIErrorStatus(t *testing.T, err error, want int) *api.APIError {
	t.Helper()
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, want, apiErr.Status)
	return apiErr
}

func multiRouteDocument() json.RawMessage {
	return json.RawMessage(`{
	  "openapi":"3.1.0","info":{"title":"Multi API","version":"1"},
	  "servers":[{"url":"https://multi.example.test"}],
	  "paths":{
	    "/":{"get":{"responses":{"200":{"description":"ok"}}}},
	    "/users":{"get":{"responses":{"200":{"description":"ok"}}}},
	    "/orders":{"post":{"responses":{"200":{"description":"ok"}}}}
	  }
	}`)
}
