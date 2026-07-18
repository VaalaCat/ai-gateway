package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

type cancelAwareTokenClient struct {
	entered chan struct{}
	release chan struct{}
}

func (c *cancelAwareTokenClient) Call(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
	close(c.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.release:
		return nil, errors.New("released test load")
	}
}

func (*cancelAwareTokenClient) OnNotification(string, app.NotificationHandler) {}
func (*cancelAwareTokenClient) Notify(string, any) error                       { return nil }
func (*cancelAwareTokenClient) Close() error                                   { return nil }
func (*cancelAwareTokenClient) ReadLoop()                                      {}

func TestTokenAuthCacheLoadHonorsRequestCancellation(t *testing.T) {
	client := &cancelAwareTokenClient{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(client.release)
	store := cache.NewStore(client, config.AgentCacheConfig{})
	router := setupRouter(store)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/test", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer uncached")
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		done <- w.Code
	}()
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("TokenAuth did not enter cache loader")
	}
	cancel()
	select {
	case code := <-done:
		if code != http.StatusUnauthorized {
			t.Fatalf("status after canceled cache load = %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("TokenAuth cache load ignored request cancellation")
	}
}

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

func TestAuthorizeModelTokenAndGroupMatchers(t *testing.T) {
	tests := []struct {
		name  string
		user  *app.UserInfo
		model string
		allow bool
	}{
		{name: "no restrictions", user: &app.UserInfo{}, model: "gpt-4o", allow: true},
		{name: "token allows", user: &app.UserInfo{TokenModels: []string{"gpt-.*"}}, model: "gpt-4o", allow: true},
		{name: "token denies", user: &app.UserInfo{TokenModels: []string{"claude-.*"}}, model: "gpt-4o"},
		{name: "group allows", user: &app.UserInfo{GroupModels: []string{"gpt-4o"}}, model: "gpt-4o", allow: true},
		{name: "group denies", user: &app.UserInfo{GroupModels: []string{"gpt-3.5"}}, model: "gpt-4o"},
		{name: "token allows but group denies", user: &app.UserInfo{TokenModels: []string{"gpt-.*"}, GroupModels: []string{"claude-.*"}}, model: "gpt-4o"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AuthorizeModel(tt.user, tt.model)
			if tt.allow && err != nil {
				t.Fatalf("AuthorizeModel() = %v, want nil", err)
			}
			if !tt.allow && !errors.Is(err, ErrModelNotAllowed) {
				t.Fatalf("AuthorizeModel() = %v, want ErrModelNotAllowed", err)
			}
		})
	}
}

func TestTokenAuthDoesNotReadRequestBody(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{
		ID: 1, Key: "sk-limited", UserID: 1, Status: 1, ExpiredAt: -1,
		Models: `["claude-.*"]`,
	})
	r := setupRouter(store)
	body := &readSpy{reader: strings.NewReader(`{"model":"gpt-4o"}`)}
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Body = body
	req.Header.Set("Authorization", "Bearer sk-limited")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("TokenAuth status = %d, want 200 before model authorization", w.Code)
	}
	if body.reads != 0 {
		t.Fatalf("TokenAuth read request body %d times", body.reads)
	}
}

type readSpy struct {
	reader io.Reader
	reads  int
}

func (r *readSpy) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func (*readSpy) Close() error { return nil }

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

func TestTokenAuth_PropagatesBYOKOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag bool
	}{
		{"byok_only true", true},
		{"byok_only false", false},
		{"default token (unset → false)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := cache.NewStore(nil, config.AgentCacheConfig{})
			store.SetToken(&models.Token{ID: 1, Key: "sk-valid", UserID: 1, Status: 1, ExpiredAt: -1, BYOKOnly: tc.flag})

			gin.SetMode(gin.TestMode)
			r := gin.New()
			var got bool
			r.POST("/test", TokenAuth(store), func(c *gin.Context) {
				v, _ := c.Get(consts.CtxKeyUserInfo)
				got = v.(*app.UserInfo).BYOKOnly
				c.JSON(200, gin.H{"ok": true})
			})

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/test", strings.NewReader(`{"model":"gpt-4o"}`))
			req.Header.Set("Authorization", "Bearer sk-valid")
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if got != tc.flag {
				t.Errorf("UserInfo.BYOKOnly = %v, want %v", got, tc.flag)
			}
		})
	}
}
