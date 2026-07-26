package billing

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestInsightsHTTPQueryBindingAndHiddenTokenErrors(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	require.NoError(t, db.Create(&models.Token{ID: 11, UserID: 1, Key: "own", Name: "own"}).Error)
	require.NoError(t, db.Create(&models.Token{ID: 21, UserID: 2, Key: "foreign", Name: "foreign"}).Error)
	serve := func(isAdmin bool, target string) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/billing/insights", func(c *gin.Context) {
			c.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: 1})
		}, api.Adapt(api.NewAdapter(nil, nil, application), api.BindQuery, h.Insights))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		return recorder
	}

	for _, query := range []string{"?top_n=-1", "?token_id=-1", "?token_id=abc"} {
		resp := serve(true, "/billing/insights"+query)
		require.Equal(t, http.StatusBadRequest, resp.Code, query)
	}
	admin := serve(true, "/billing/insights?token_id=21&top_n=5")
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	own := serve(false, "/billing/insights?token_id=11")
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())
	foreign := serve(false, "/billing/insights?token_id=21")
	unknown := serve(false, "/billing/insights?token_id=999")
	require.Equal(t, http.StatusNotFound, foreign.Code)
	require.Equal(t, foreign.Code, unknown.Code)
	require.JSONEq(t, foreign.Body.String(), unknown.Body.String())
}
