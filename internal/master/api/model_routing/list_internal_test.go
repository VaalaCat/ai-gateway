package model_routing

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelRoutingListPreservesDatabaseErrorCause(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.ModelRouting{}))

	databaseErr := errors.New("model routing list failed")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:fail_model_routing_list",
		func(tx *gorm.DB) { tx.AddError(databaseErr) },
	))

	application := app.NewApplication()
	application.SetCoreDB(db)
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/admin/model-routings", nil)

	_, listErr := (&Handler{}).List(
		&app.Context{Context: ginContext, App: application},
		ListRequest{},
	)
	require.Error(t, listErr)
	var apiErr *api.APIError
	require.ErrorAs(t, listErr, &apiErr)
	require.Equal(t, http.StatusInternalServerError, apiErr.Status)
	require.ErrorIs(t, apiErr.Cause, databaseErr)
}
