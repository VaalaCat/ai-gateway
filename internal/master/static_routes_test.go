package master

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func setupStaticRouter(t *testing.T, assets fs.FS) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := &Server{
		Router: gin.New(),
		Logger: zap.NewNop(),
	}
	s.setupStaticRoutesFromFS(assets)
	return s.Router
}

func TestStaticRoutes_FallbackToIndex(t *testing.T) {
	router := setupStaticRouter(t, fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"assets/app.js": {Data: []byte("console.log('ok')")},
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/dashboard", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<html>index</html>") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestStaticRoutes_ServeRouteSpecificIndex(t *testing.T) {
	router := setupStaticRouter(t, fstest.MapFS{
		"index.html":       {Data: []byte("<html>root</html>")},
		"login/index.html": {Data: []byte("<html>login</html>")},
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/login", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<html>login</html>") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestStaticRoutes_ServeAssetFile(t *testing.T) {
	router := setupStaticRouter(t, fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"assets/app.js": {Data: []byte("console.log('ok')")},
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/assets/app.js", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "console.log('ok')") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestStaticRoutes_KeepAPINotFoundJSON(t *testing.T) {
	router := setupStaticRouter(t, fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/not-found", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "\"error\":\"not found\"") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestStaticRoutes_KeepWSNotFoundJSON(t *testing.T) {
	router := setupStaticRouter(t, fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/ws/not-found", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "\"error\":\"not found\"") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}
