package middleware_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateToken_DisplayNameAvatarClaims(t *testing.T) {
	secret := "test-secret"
	tok, err := middleware.GenerateToken(secret, 42, 1, "alice", "Alice 张三", "https://example.com/a.png")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	parsed, err := jwt.Parse(tok, func(_ *jwt.Token) (any, error) { return []byte(secret), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("parse: err=%v valid=%v", err, parsed.Valid)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if got, _ := claims[consts.ClaimDisplayName].(string); got != "Alice 张三" {
		t.Errorf("display_name claim mismatch: %q", got)
	}
	if got, _ := claims[consts.ClaimAvatarURL].(string); got != "https://example.com/a.png" {
		t.Errorf("avatar_url claim mismatch: %q", got)
	}
}

func TestAuthMiddleware_ParsesDisplayNameAvatarClaims(t *testing.T) {
	secret := "test-secret"
	tok, _ := middleware.GenerateToken(secret, 42, 1, "alice", "Alice 张三", "https://example.com/a.png")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.AuthMiddleware(secret))
	r.GET("/me", func(c *gin.Context) {
		ui := c.MustGet(consts.CtxKeyUserInfo).(*app.UserInfo)
		c.JSON(200, gin.H{"display_name": ui.DisplayName, "avatar_url": ui.AvatarURL})
	})

	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Alice 张三") || !strings.Contains(body, "https://example.com/a.png") {
		t.Errorf("body missing claims: %s", body)
	}
}

func TestAuthMiddleware_BackwardCompat_OldTokenWithoutNewClaims(t *testing.T) {
	secret := "test-secret"
	// 手工签一个不含 display_name / avatar_url 的 token,模拟旧 token。
	tokRaw := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		consts.ClaimUserID:   42,
		consts.ClaimRole:     1,
		consts.ClaimUsername: "old",
		consts.ClaimExp:      time.Now().Add(time.Hour).Unix(),
	})
	tok, _ := tokRaw.SignedString([]byte(secret))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.AuthMiddleware(secret))
	r.GET("/me", func(c *gin.Context) {
		ui := c.MustGet(consts.CtxKeyUserInfo).(*app.UserInfo)
		c.JSON(200, gin.H{"display_name": ui.DisplayName, "avatar_url": ui.AvatarURL})
	})

	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"display_name":""`) {
		t.Errorf("expected empty display_name fallback, got: %s", w.Body.String())
	}
}
