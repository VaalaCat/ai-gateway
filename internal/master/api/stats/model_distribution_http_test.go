package stats

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelDistributionHTTPRouteBindingAndAuth(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	serve := func(isAdmin bool, target string) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/stats/model-distribution", func(c *gin.Context) {
			c.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: 1})
		}, api.Adapt(api.NewAdapter(nil, nil, application), api.BindQuery, h.ModelDistribution))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		return recorder
	}
	require.Equal(t, http.StatusForbidden, serve(false, "/stats/model-distribution?top_n=5").Code)
	require.Equal(t, http.StatusBadRequest, serve(true, "/stats/model-distribution?top_n=-1").Code)
	admin := serve(true, "/stats/model-distribution?top_n=10")
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
}
