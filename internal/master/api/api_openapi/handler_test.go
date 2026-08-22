package api_openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Break caught: preview must parse the submitted document through the real
// OpenAPI boundary, expose every server and root route, and remain read-only.
func TestPreviewHTTPAcceptsOpenAPI30And31WithoutWriting(t *testing.T) {
	db, application := openAPITestDB(t)
	router := previewRouter(application)

	for _, fixture := range []string{"openapi-3.0.json", "openapi-3.1.json"} {
		t.Run(fixture, func(t *testing.T) {
			before := openAPIEntityCounts(t, db)
			response := postPreview(t, router, fixtureDocument(t, fixture))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body PreviewResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.NotEmpty(t, body.Service.Slug)
			require.NotEmpty(t, body.Service.Name)
			require.Len(t, body.Servers, 2)
			require.NotEmpty(t, body.Routes)
			require.Equal(t, before, openAPIEntityCounts(t, db))
		})
	}
}

func TestPreviewHTTPReturnsProblemsAndRejectsExistingServiceSlug(t *testing.T) {
	db, application := openAPITestDB(t)
	router := previewRouter(application)

	invalid := postPreview(t, router, json.RawMessage(`"{\"openapi\":"`))
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	require.Equal(t, "invalid_openapi", responseCode(t, invalid))
	require.Equal(t, "invalid_document", responseProblems(t, invalid)[0].Code)

	document := fixtureDocument(t, "openapi-3.0.json")
	require.NoError(t, db.Create(&models.APIService{Slug: "user-api", Name: "existing"}).Error)
	conflict := postPreview(t, router, document)
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.Equal(t, "service_slug_conflict", responseCode(t, conflict))
}

func TestPreviewHTTPShowsRootRouteAndRejectsOversizedBody(t *testing.T) {
	_, application := openAPITestDB(t)
	router := previewRouter(application)
	document := json.RawMessage(`{
	  "openapi":"3.1.0","info":{"title":"Root API","version":"1"},
	  "servers":[{"url":"https://root.example.test"}],
	  "paths":{"/":{"get":{"responses":{"200":{"description":"ok"}}}},"/{tenant}/users":{"post":{"responses":{"200":{"description":"ok"}}}}}
	}`)
	response := postPreview(t, router, document)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body PreviewResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Routes, 1)
	require.Empty(t, body.Routes[0].Slug)
	require.Equal(t, "根路由", body.Routes[0].DisplayName)

	request := httptest.NewRequest(http.MethodPost, "/preview", strings.NewReader(strings.Repeat(" ", MaxRequestBodyBytes+1)))
	request.Header.Set("Content-Type", "application/json")
	tooLarge := httptest.NewRecorder()
	router.ServeHTTP(tooLarge, request)
	require.Equal(t, http.StatusRequestEntityTooLarge, tooLarge.Code, tooLarge.Body.String())
}

// Break caught: unknown-length and chunked requests must not leak an
// http.MaxBytesError into the JSON binder as a generic 400 response.
func TestLimitRequestBodyHandlesKnownUnknownChunkedAndBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/limited", LimitRequestBody(func(c *gin.Context) {
		payload, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"size": len(payload)})
	}))

	for _, test := range []struct {
		name       string
		length     int64
		chunked    bool
		bodyBytes  int
		wantStatus int
	}{
		{name: "known length over limit", length: MaxRequestBodyBytes + 1, bodyBytes: MaxRequestBodyBytes + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "unknown length over limit", length: -1, bodyBytes: MaxRequestBodyBytes + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "chunked over limit", length: -1, chunked: true, bodyBytes: MaxRequestBodyBytes + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "exact boundary", length: MaxRequestBodyBytes, bodyBytes: MaxRequestBodyBytes, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/limited", bytes.NewReader(bytes.Repeat([]byte{'x'}, test.bodyBytes)))
			request.ContentLength = test.length
			if test.chunked {
				request.TransferEncoding = []string{"chunked"}
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, test.wantStatus, response.Code, response.Body.String())
			if test.wantStatus == http.StatusRequestEntityTooLarge {
				require.Equal(t, "request_too_large", responseCode(t, response))
			} else {
				require.JSONEq(t, fmt.Sprintf(`{"size":%d}`, MaxRequestBodyBytes), response.Body.String())
			}
		})
	}
}

func previewRouter(application app.Application) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adapter := api.NewAdapter(&config.MasterRuntimeConfig{}, zap.NewNop(), application)
	handler := &Handler{App: application}
	router.POST("/preview", LimitRequestBody(api.Adapt(adapter, api.BindStrictJSONText, handler.Preview)))
	return router
}

func postPreview(t *testing.T, router http.Handler, document json.RawMessage) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(PreviewRequest{Document: document})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func fixtureDocument(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "apiopenapi", "testdata", name))
	require.NoError(t, err)
	return raw
}

func openAPITestDB(t *testing.T) (*gorm.DB, app.Application) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	return db, application
}

func openAPIEntityCounts(t *testing.T, db *gorm.DB) [4]int64 {
	t.Helper()
	var counts [4]int64
	for index, model := range []any{&models.APIService{}, &models.APIBackend{}, &models.APIUpstream{}, &models.APIRoute{}} {
		require.NoError(t, db.Model(model).Count(&counts[index]).Error)
	}
	return counts
}

func responseCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body.Code
}

func responseProblems(t *testing.T, response *httptest.ResponseRecorder) []Problem {
	t.Helper()
	var body struct {
		Details struct {
			Problems []Problem `json:"problems"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotEmpty(t, body.Details.Problems)
	return body.Details.Problems
}
