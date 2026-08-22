package api_openapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestOpenAPIDocumentFinderReturnsPlatformDocumentsAndVersions(t *testing.T) {
	db, application := openAPITestDB(t)
	result := importMultiRoute(t, application, "finder-api")
	require.NoError(t, db.Model(&models.APIUpstream{}).Where("id = ?", result.UpstreamID).Update("base_url", "https://runtime-only.example.test").Error)

	snapshot, err := (OpenAPIDocumentFinder{}).Find(dao.NewContext(application), result.ServiceID)
	require.NoError(t, err)
	require.Equal(t, result.ServiceID, snapshot.Service.ID)
	require.NotZero(t, snapshot.Service.UpdatedAt)
	require.Equal(t, "3.1.0", snapshot.Service.Document.Version)
	require.Len(t, snapshot.Routes, len(result.RouteIDs))
	for _, route := range snapshot.Routes {
		require.NotZero(t, route.ID)
		require.NotZero(t, route.UpdatedAt)
		require.NotEmpty(t, route.Paths)
	}
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "api_upstreams")
	var upstream models.APIUpstream
	require.NoError(t, db.First(&upstream, result.UpstreamID).Error)
	require.NotContains(t, string(encoded), upstream.BaseURL)
}

func TestOpenAPIDocumentFinderDistinguishesAllAndEmptyVisibleRouteScopes(t *testing.T) {
	_, application := openAPITestDB(t)
	result := importMultiRoute(t, application, "visible-scope-api")
	finder := OpenAPIDocumentFinder{}

	all, err := finder.FindVisible(dao.NewContext(application), result.ServiceID, nil)
	require.NoError(t, err)
	require.Len(t, all.Routes, len(result.RouteIDs))

	emptyIDs := []uint{}
	empty, err := finder.FindVisible(dao.NewContext(application), result.ServiceID, &emptyIDs)
	require.NoError(t, err)
	require.Empty(t, empty.Routes)
	require.Equal(t, result.ServiceID, empty.Service.ID)
}

// Break caught: the Finder must not combine service metadata from before a
// concurrent commit with document versions, paths, or a route set from after it.
func TestOpenAPIDocumentFinderReadsOneSQLiteSnapshot(t *testing.T) {
	reader, writer, application := openAPIFileDatabases(t)
	result := importMultiRoute(t, application, "snapshot-api")
	finder := OpenAPIDocumentFinder{}
	before, err := finder.Find(dao.NewContext(application), result.ServiceID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(before.Routes), 2)

	firstRead := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	callback := "openapi:snapshot-after-first-read"
	require.NoError(t, reader.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "api_services" {
			once.Do(func() {
				close(firstRead)
				<-resume
			})
		}
	}))
	t.Cleanup(func() { _ = reader.Callback().Query().Remove(callback) })

	type findResult struct {
		snapshot DocumentSnapshot
		err      error
	}
	resultCh := make(chan findResult, 1)
	go func() {
		snapshot, findErr := finder.Find(dao.NewContext(application), result.ServiceID)
		resultCh <- findResult{snapshot: snapshot, err: findErr}
	}()
	<-firstRead

	updatedDocument := before.Service.Document
	updatedDocument.Info.Summary = "committed between finder reads"
	updatedPaths := datatypes.NewJSONType(map[string]models.OpenAPIPathItem{
		"/changed": before.Routes[0].Paths[firstRoutePath(before.Routes[0].Paths)],
	})
	require.NoError(t, writer.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.APIService{}).Where("id = ?", result.ServiceID).UpdateColumns(map[string]any{
			"openapi_document": datatypes.NewJSONType(updatedDocument),
			"updated_at":       before.Service.UpdatedAt + 100,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.APIRoute{}).Where("id = ?", before.Routes[0].ID).UpdateColumns(map[string]any{
			"openapi_paths": updatedPaths,
			"updated_at":    before.Routes[0].UpdatedAt + 100,
		}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.APIRoute{}, before.Routes[len(before.Routes)-1].ID).Error
	}))
	close(resume)
	after := <-resultCh
	require.NoError(t, after.err)
	require.Equal(t, before, after.snapshot)
}

func TestGetAndUpdateOpenAPIDocumentUsePlatformSSOTAndSanitizedExport(t *testing.T) {
	db, application := openAPITestDB(t)
	result := importMultiRoute(t, application, "document-api")
	require.NoError(t, db.Model(&models.APIUpstream{}).Where("id = ?", result.UpstreamID).Updates(map[string]any{
		"base_url": "https://private-upstream.example.test", "credential_ciphertext": "encrypted-secret-marker",
	}).Error)
	handler := &Handler{App: application}
	ctx := openAPIManagerContext(application)

	before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
	require.NoError(t, err)
	require.NotContains(t, string(before.Export), "private-upstream.example.test")
	require.NotContains(t, string(before.Export), "encrypted-secret-marker")
	require.Contains(t, string(before.Export), "http://gateway.example.test/v1/api/document-api")

	request := updateRequest(before)
	request.Service.Document.Info.Summary = "edited summary"
	firstPath := firstRoutePath(request.Routes[0].Paths)
	item := request.Routes[0].Paths[firstPath]
	for method, operation := range item.Operations {
		operation.Description = "edited operation"
		item.Operations[method] = operation
		break
	}
	request.Routes[0].Paths[firstPath] = item
	after, err := handler.Update(ctx, request)
	require.NoError(t, err)
	require.Greater(t, after.Service.UpdatedAt, before.Service.UpdatedAt)
	require.Contains(t, string(after.Export), "edited summary")
	require.Contains(t, string(after.Export), "edited operation")
	require.NotContains(t, string(after.Export), "private-upstream.example.test")
}

func TestUpdateOpenAPIDocumentRejectsVersionOwnershipAndDuplicatePathsAtomically(t *testing.T) {
	t.Run("service version conflict", func(t *testing.T) {
		db, application := openAPITestDB(t)
		result := importMultiRoute(t, application, "version-api")
		handler := &Handler{App: application}
		ctx := openAPIManagerContext(application)
		before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
		require.NoError(t, err)
		require.NoError(t, db.Model(&models.APIService{}).Where("id = ?", result.ServiceID).UpdateColumn("updated_at", before.Service.UpdatedAt+10).Error)
		_, err = handler.Update(ctx, updateRequest(before))
		requireAPIErrorCode(t, err, "openapi_version_conflict")
	})

	t.Run("route version conflict", func(t *testing.T) {
		db, application := openAPITestDB(t)
		result := importMultiRoute(t, application, "route-version-api")
		handler := &Handler{App: application}
		ctx := openAPIManagerContext(application)
		before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
		require.NoError(t, err)
		require.NoError(t, db.Model(&models.APIRoute{}).Where("id = ?", before.Routes[0].ID).UpdateColumn("updated_at", before.Routes[0].UpdatedAt+10).Error)
		_, err = handler.Update(ctx, updateRequest(before))
		requireAPIErrorCode(t, err, "openapi_version_conflict")
	})

	t.Run("duplicate route id", func(t *testing.T) {
		_, application := openAPITestDB(t)
		result := importMultiRoute(t, application, "duplicate-route-id-api")
		handler := &Handler{App: application}
		ctx := openAPIManagerContext(application)
		before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
		require.NoError(t, err)
		request := updateRequest(before)
		request.Routes = append(request.Routes, request.Routes[0])
		_, err = handler.Update(ctx, request)
		requireAPIErrorCode(t, err, "invalid_openapi_update")
	})

	t.Run("incomplete route set", func(t *testing.T) {
		_, application := openAPITestDB(t)
		result := importMultiRoute(t, application, "incomplete-route-set-api")
		handler := &Handler{App: application}
		ctx := openAPIManagerContext(application)
		before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
		require.NoError(t, err)
		request := updateRequest(before)
		request.Routes = request.Routes[:len(request.Routes)-1]
		_, err = handler.Update(ctx, request)
		requireAPIErrorCode(t, err, "route_set_mismatch")
	})

	t.Run("foreign route", func(t *testing.T) {
		_, application := openAPITestDB(t)
		owned := importMultiRoute(t, application, "owned-api")
		foreign := importMultiRoute(t, application, "foreign-api")
		handler := &Handler{App: application}
		ctx := openAPIManagerContext(application)
		before, err := handler.Get(ctx, IDRequest{ID: owned.ServiceID})
		require.NoError(t, err)
		request := updateRequest(before)
		request.Routes[0].ID = foreign.RouteIDs[0]
		_, err = handler.Update(ctx, request)
		requireAPIErrorCode(t, err, "route_service_mismatch")
		after, err := handler.Get(ctx, IDRequest{ID: owned.ServiceID})
		require.NoError(t, err)
		require.Equal(t, before.Export, after.Export)
	})

	t.Run("duplicate public path", func(t *testing.T) {
		_, application := openAPITestDB(t)
		result := importMultiRoute(t, application, "duplicate-path-api")
		handler := &Handler{App: application}
		ctx := openAPIManagerContext(application)
		before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
		require.NoError(t, err)
		request := updateRequest(before)
		root := routeUpdateBySlug(request.Routes, before.Routes, "")
		users := routeUpdateBySlug(request.Routes, before.Routes, "users")
		require.NotNil(t, root)
		require.NotNil(t, users)
		root.Paths["/users"] = users.Paths["/users"]
		_, err = handler.Update(ctx, request)
		requireAPIErrorCode(t, err, "invalid_openapi_update")
		after, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
		require.NoError(t, err)
		require.Equal(t, before.Export, after.Export)
	})
}

func TestUpdateOpenAPIDocumentCASDetectsChangesAfterRead(t *testing.T) {
	for _, target := range []string{"service", "route"} {
		t.Run(target, func(t *testing.T) {
			db, application := openAPITestDB(t)
			result := importMultiRoute(t, application, "cas-"+target+"-api")
			handler := &Handler{App: application}
			ctx := openAPIManagerContext(application)
			before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
			require.NoError(t, err)
			request := updateRequest(before)

			table := "api_services"
			id := before.Service.ID
			version := before.Service.UpdatedAt
			if target == "route" {
				table = "api_routes"
				id = before.Routes[0].ID
				version = before.Routes[0].UpdatedAt
			}
			injected := false
			callback := "openapi:cas-after-read:" + target
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if injected || tx.Statement == nil || tx.Statement.Table != table {
					return
				}
				injected = true
				if execErr := tx.Exec("UPDATE "+table+" SET updated_at = ? WHERE id = ?", version+1, id).Error; execErr != nil {
					tx.AddError(execErr)
				}
			}))
			t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })

			_, err = handler.Update(ctx, request)
			requireAPIErrorCode(t, err, "openapi_version_conflict")
			after, findErr := handler.Get(ctx, IDRequest{ID: result.ServiceID})
			require.NoError(t, findErr)
			require.Equal(t, before, after)
		})
	}
}

func TestConcurrentSQLiteOpenAPIDocumentUpdatesHaveOneWinner(t *testing.T) {
	reader, writer, readerApp := openAPIFileDatabases(t)
	writerApp := openAPIApplication(writer)
	result := importMultiRoute(t, readerApp, "concurrent-put-api")
	before, err := (&Handler{}).Get(openAPIManagerContext(readerApp), IDRequest{ID: result.ServiceID})
	require.NoError(t, err)

	requests := []UpdateRequest{updateRequest(before), updateRequest(before)}
	requests[0].Service.Document.Info.Summary = "first writer"
	requests[1].Service.Document.Info.Summary = "second writer"
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	for index, application := range []app.Application{readerApp, writerApp} {
		go func(index int, application app.Application) {
			<-start
			_, updateErr := (&Handler{}).Update(openAPIManagerContext(application), requests[index])
			errorsCh <- updateErr
		}(index, application)
	}
	close(start)

	var success, conflict int
	for range 2 {
		updateErr := <-errorsCh
		if updateErr == nil {
			success++
			continue
		}
		var apiErr *api.APIError
		require.ErrorAs(t, updateErr, &apiErr)
		if apiErr.Status == http.StatusConflict && apiErr.Code == "openapi_version_conflict" {
			conflict++
			continue
		}
		t.Fatalf("concurrent loser must be 409, got status=%d code=%q error=%v", apiErr.Status, apiErr.Code, updateErr)
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)

	var service models.APIService
	require.NoError(t, reader.First(&service, result.ServiceID).Error)
	require.Contains(t, []string{"first writer", "second writer"}, service.OpenAPIDocument.Data().Info.Summary)
}

func TestUpdateOpenAPIDocumentRollsBackOnSecondRouteWriteFailure(t *testing.T) {
	db, application := openAPITestDB(t)
	result := importMultiRoute(t, application, "write-rollback-api")
	handler := &Handler{App: application}
	ctx := openAPIManagerContext(application)
	before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
	require.NoError(t, err)
	request := updateRequest(before)
	request.Service.Document.Info.Summary = "must roll back"

	writes := 0
	callback := "fail-second-openapi-route-update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "api_routes" {
			writes++
			if writes == 2 {
				tx.AddError(errors.New("second route update failed"))
			}
		}
	}))
	defer db.Callback().Update().Remove(callback)
	_, err = handler.Update(ctx, request)
	require.Error(t, err)
	after, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
	require.NoError(t, err)
	require.Equal(t, before.Export, after.Export)
}

func TestUpdateOpenAPIDocumentMapsUnexpectedDatabaseFailureToInternalError(t *testing.T) {
	db, application := openAPITestDB(t)
	result := importMultiRoute(t, application, "update-database-failure")
	handler := &Handler{App: application}
	ctx := openAPIManagerContext(application)
	before, err := handler.Get(ctx, IDRequest{ID: result.ServiceID})
	require.NoError(t, err)
	secret := "private database failure detail"
	callback := "openapi:fail-document-update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "api_services" {
			tx.AddError(errors.New(secret))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })

	_, err = handler.Update(ctx, updateRequest(before))
	apiErr := requireAPIErrorStatus(t, err, http.StatusInternalServerError)
	require.NotContains(t, apiErr.Message, secret)
	status, body := (api.DefaultErrorMapper{}).Map(err)
	external, marshalErr := json.Marshal(body)
	require.NoError(t, marshalErr)
	require.Equal(t, http.StatusInternalServerError, status)
	require.NotContains(t, string(external), secret)
	after, findErr := handler.Get(ctx, IDRequest{ID: result.ServiceID})
	require.NoError(t, findErr)
	require.Equal(t, before, after)
}

func TestOrdinaryPatchesCannotModifyOpenAPIDocuments(t *testing.T) {
	_, application := openAPITestDB(t)
	result := importMultiRoute(t, application, "patch-isolation-api")
	ctx := dao.NewContext(application)
	snapshot, err := (OpenAPIDocumentFinder{}).Find(ctx, result.ServiceID)
	require.NoError(t, err)

	err = dao.NewAdminMutation(ctx).APIService().Update(result.ServiceID, map[string]any{
		"openapi_document": datatypes.NewJSONType(models.OpenAPIServiceDocument{}),
	})
	require.Error(t, err)
	err = dao.NewAdminMutation(ctx).APIRoute().Update(result.RouteIDs[0], map[string]any{
		"openapi_paths": datatypes.NewJSONType(map[string]models.OpenAPIPathItem{}),
	})
	require.Error(t, err)
	after, err := (OpenAPIDocumentFinder{}).Find(ctx, result.ServiceID)
	require.NoError(t, err)
	require.Equal(t, snapshot, after)
}

func importMultiRoute(t *testing.T, application app.Application, slug string) ImportResult {
	t.Helper()
	selected := 0
	result, err := (Importer{UpstreamCreator: api_upstream.Creator{Cipher: openAPITestCipher(t)}}).Import(
		dao.NewContext(application), ImportCommand{
			Document: multiRouteDocument(), Slug: slug, SelectedServer: &selected,
			BackendName: "primary", Upstream: UpstreamDraft{Name: "origin"},
		})
	require.NoError(t, err)
	return result
}

func openAPIManagerContext(application app.Application) *app.Context {
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Request = httptest.NewRequest("GET", "http://gateway.example.test/api/admin/api-services/1/openapi", nil)
	return &app.Context{Context: ginContext, App: application, OwnerContext: context.Background()}
}

func updateRequest(response DocumentResponse) UpdateRequest {
	routes := make([]RouteDocumentUpdate, 0, len(response.Routes))
	for _, route := range response.Routes {
		routes = append(routes, RouteDocumentUpdate{ID: route.ID, UpdatedAt: route.UpdatedAt, Paths: route.Paths})
	}
	return UpdateRequest{
		ID: response.Service.ID,
		Service: ServiceDocumentUpdate{
			ID: response.Service.ID, UpdatedAt: response.Service.UpdatedAt, Document: response.Service.Document,
		},
		Routes: routes,
	}
}

func firstRoutePath(paths map[string]models.OpenAPIPathItem) string {
	for path := range paths {
		return path
	}
	return ""
}

func routeUpdateBySlug(routes []RouteDocumentUpdate, responseRoutes []RouteDocument, slug string) *RouteDocumentUpdate {
	for index := range responseRoutes {
		if responseRoutes[index].Slug == slug {
			return &routes[index]
		}
	}
	return nil
}

func openAPIFileDatabases(t *testing.T) (*gorm.DB, *gorm.DB, app.Application) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "openapi.db") + "?_pragma=busy_timeout(5000)"
	reader, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, reader.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, models.MigrateCoreDB(reader))
	writer, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	for _, db := range []*gorm.DB{reader, writer} {
		sqlDB, dbErr := db.DB()
		require.NoError(t, dbErr)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	}
	return reader, writer, openAPIApplication(reader)
}

func openAPIApplication(db *gorm.DB) app.Application {
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	return application
}
