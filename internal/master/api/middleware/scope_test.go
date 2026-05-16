package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestScopeMiddleware_Admin(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, Role: consts.RoleAdmin})
		c.Next()
	})
	r.Use(ScopeMiddleware())
	r.GET("/test", func(c *gin.Context) {
		scope := GetScope(c)
		if scope == nil {
			t.Fatal("scope should not be nil")
		}
		if !scope.IsAdmin {
			t.Error("expected IsAdmin to be true")
		}
		if scope.UserID != 1 {
			t.Errorf("expected UserID 1, got %d", scope.UserID)
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestScopeMiddleware_User(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 42, Role: consts.RoleUser})
		c.Next()
	})
	r.Use(ScopeMiddleware())
	r.GET("/test", func(c *gin.Context) {
		scope := GetScope(c)
		if scope == nil {
			t.Fatal("scope should not be nil")
		}
		if scope.IsAdmin {
			t.Error("expected IsAdmin to be false")
		}
		if scope.UserID != 42 {
			t.Errorf("expected UserID 42, got %d", scope.UserID)
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestScopeMiddleware_NoUserInfo(t *testing.T) {
	r := gin.New()
	r.Use(ScopeMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestEnsureOwnership_Admin(t *testing.T) {
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		scope := &RequestScope{IsAdmin: true, UserID: 1}
		if !EnsureOwnership(c, scope, 999) {
			t.Error("admin should always pass ownership check")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestEnsureOwnership_UserOwns(t *testing.T) {
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		scope := &RequestScope{IsAdmin: false, UserID: 42}
		if !EnsureOwnership(c, scope, 42) {
			t.Error("user should pass ownership check for own resource")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestEnsureOwnership_UserDenied(t *testing.T) {
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		scope := &RequestScope{IsAdmin: false, UserID: 42}
		if EnsureOwnership(c, scope, 999) {
			t.Error("user should not pass ownership check for other's resource")
		}
		// EnsureOwnership should have written 404
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetScope_Nil(t *testing.T) {
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		scope := GetScope(c)
		if scope != nil {
			t.Error("expected nil scope when not set")
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
}

func TestScopedUserID(t *testing.T) {
	// Admin with reqUserID set -> use reqUserID
	adminScope := &RequestScope{IsAdmin: true, UserID: 1}
	reqID := uint(42)
	result := ScopedUserID(adminScope, &reqID)
	if result == nil || *result != 42 {
		t.Errorf("admin with reqUserID: expected 42, got %v", result)
	}

	// Admin with nil reqUserID -> nil
	result = ScopedUserID(adminScope, nil)
	if result != nil {
		t.Errorf("admin with nil reqUserID: expected nil, got %v", result)
	}

	// User always gets own ID
	userScope := &RequestScope{IsAdmin: false, UserID: 42}
	result = ScopedUserID(userScope, &reqID)
	if result == nil || *result != 42 {
		t.Errorf("user should get own ID, got %v", result)
	}

	// User with nil reqUserID still gets own ID
	result = ScopedUserID(userScope, nil)
	if result == nil || *result != 42 {
		t.Errorf("user with nil should get own ID, got %v", result)
	}
}
