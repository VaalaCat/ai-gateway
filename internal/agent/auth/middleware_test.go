package auth

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
)

func setupRouter(store *cache.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/test", TokenAuth(store), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	return r
}

func TestValidToken(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{ID: 1, Key: "sk-valid", UserID: 1, Status: 1, ExpiredAt: -1})
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Authorization", "Bearer sk-valid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestValidTokenWithXAPIKey(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{ID: 1, Key: "sk-valid", UserID: 1, Status: 1, ExpiredAt: -1})
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("x-api-key", "sk-valid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestMissingKey(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestInvalidKey(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("Authorization", "Bearer sk-invalid")
	r.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestDisabledToken(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{ID: 1, Key: "sk-disabled", UserID: 1, Status: 0, ExpiredAt: -1})
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("Authorization", "Bearer sk-disabled")
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestExpiredToken(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{ID: 1, Key: "sk-expired", UserID: 1, Status: 1, ExpiredAt: time.Now().Add(-time.Hour).Unix()})
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("Authorization", "Bearer sk-expired")
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestModelNotAllowed(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{ID: 1, Key: "sk-limited", UserID: 1, Status: 1, ExpiredAt: -1, Models: `["gpt-3.5-turbo"]`})
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Authorization", "Bearer sk-limited")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestTokenAuth_PopulatesAllowedChannelIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{
		ID:                1,
		Key:               "sk-test",
		UserID:            1,
		Status:            1,
		ExpiredAt:         -1,
		AllowedChannelIDs: datatypes.JSONSlice[uint]{3, 7, 9},
	})

	r := gin.New()
	var captured []uint
	r.POST("/probe", TokenAuth(store), func(c *gin.Context) {
		v, _ := c.Get(consts.CtxKeyUserInfo)
		ui := v.(*app.UserInfo)
		captured = ui.AllowedChannelIDs
		c.JSON(200, gin.H{"ok": true})
	})

	req := httptest.NewRequest("POST", "/probe", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	want := []uint{3, 7, 9}
	if !reflect.DeepEqual(captured, want) {
		t.Fatalf("AllowedChannelIDs = %v, want %v", captured, want)
	}
}

func TestTokenAuth_AppliesUserGroup(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetUserGroup(&models.UserGroup{
		ID: 5, Status: consts.StatusEnabled, Name: "g",
		AllowedChannelIDs: datatypes.JSONSlice[uint]{10, 20},
		Models:            `["gpt-4o"]`,
	})
	store.SetUser(&protocol.SyncedUser{ID: 42, GroupID: 5})
	store.SetToken(&models.Token{
		ID: 1, UserID: 42, Key: "tk_abc", Status: consts.StatusEnabled, ExpiredAt: -1,
	})

	var captured *app.UserInfo
	r := gin.New()
	r.Use(TokenAuth(store))
	r.GET("/x", func(c *gin.Context) {
		v, _ := c.Get(consts.CtxKeyUserInfo)
		captured = v.(*app.UserInfo)
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer tk_abc")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if captured.GroupID != 5 {
		t.Fatalf("GroupID = %d, want 5", captured.GroupID)
	}
	if !reflect.DeepEqual(captured.GroupAllowedChannelIDs, []uint{10, 20}) {
		t.Fatalf("GroupAllowedChannelIDs = %v", captured.GroupAllowedChannelIDs)
	}
	if !reflect.DeepEqual(captured.GroupModels, []string{"gpt-4o"}) {
		t.Fatalf("GroupModels = %v", captured.GroupModels)
	}
}

func TestTokenAuth_DefaultsToGroupOneWhenUserMissing(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetUserGroup(&models.UserGroup{ID: 1, Status: consts.StatusEnabled, Name: "default"})
	store.SetToken(&models.Token{ID: 1, UserID: 99, Key: "tk_xyz", Status: consts.StatusEnabled, ExpiredAt: -1})

	var captured *app.UserInfo
	r := gin.New()
	r.Use(TokenAuth(store))
	r.GET("/x", func(c *gin.Context) {
		v, _ := c.Get(consts.CtxKeyUserInfo)
		captured = v.(*app.UserInfo)
		c.Status(200)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer tk_xyz")
	r.ServeHTTP(w, req)
	if captured.GroupID != 1 {
		t.Fatalf("expected default GroupID=1, got %d", captured.GroupID)
	}
}

func TestTokenAuth_GroupDisabled_403(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetUserGroup(&models.UserGroup{ID: 7, Status: consts.StatusDisabled, Name: "off"})
	store.SetUser(&protocol.SyncedUser{ID: 42, GroupID: 7})
	store.SetToken(&models.Token{ID: 1, UserID: 42, Key: "tk_off", Status: consts.StatusEnabled, ExpiredAt: -1})

	r := gin.New()
	r.Use(TokenAuth(store))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer tk_off")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// __system_test__ 等系统级 token UserID=0，DB 里不存在该 user。
// middleware 必须跳过 GetUser，否则每次 channel test 都会把 users 实体的 negative_hits 推高。
func TestTokenAuth_SystemTestTokenSkipsUserLookup(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetUserGroup(&models.UserGroup{ID: 1, Status: consts.StatusEnabled, Name: "default"})
	store.SetToken(&models.Token{
		ID: 1, UserID: 0, Key: "tk_sys", Name: "__system_test__",
		Status: consts.StatusEnabled, ExpiredAt: -1,
	})

	var captured *app.UserInfo
	r := gin.New()
	r.Use(TokenAuth(store))
	r.GET("/x", func(c *gin.Context) {
		v, _ := c.Get(consts.CtxKeyUserInfo)
		captured = v.(*app.UserInfo)
		c.Status(200)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer tk_sys")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if captured.GroupID != 1 {
		t.Fatalf("GroupID = %d, want 1 (default)", captured.GroupID)
	}
	// 关键断言：UserID=0 不应触发 user cache 查询。
	if miss := store.CacheSnapshot()["user"].Misses; miss != 0 {
		t.Fatalf("user cache Misses = %d, want 0 (system test token must not probe user cache)", miss)
	}
}

func TestTokenAuth_DefaultGroupDisabledFlag_Ignored(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	// Even if default group has Status=2, auth layer ignores it (default group is always usable)
	store.SetUserGroup(&models.UserGroup{ID: 1, Status: consts.StatusDisabled, Name: "default"})
	store.SetUser(&protocol.SyncedUser{ID: 42, GroupID: 1})
	store.SetToken(&models.Token{ID: 1, UserID: 42, Key: "tk_def", Status: consts.StatusEnabled, ExpiredAt: -1})

	r := gin.New()
	r.Use(TokenAuth(store))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer tk_def")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("default-disabled should be ignored, got %d", w.Code)
	}
}
