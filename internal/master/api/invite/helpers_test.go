package invite

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newInviteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func setEnabled(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&models.Setting{Key: consts.SettingKeyInviteEnabled, Value: "true"}).Error; err != nil {
		t.Fatalf("setEnabled: %v", err)
	}
}

func inviteCtx(t *testing.T, db *gorm.DB, isAdmin bool, userID uint) *app.Context {
	t.Helper()
	w := httptest.NewRecorder()
	g, _ := gin.CreateTestContext(w)
	a := app.NewApplication()
	a.SetDB(db)
	role := consts.RoleUser
	if isAdmin {
		role = consts.RoleAdmin
	}
	c := &app.Context{Context: g, App: a, UserInfo: &app.UserInfo{UserID: userID, Role: role}}
	g.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
	return c
}

func itoa(id uint) string { return strconv.FormatUint(uint64(id), 10) }

func assertStatus(t *testing.T, err error, want int) {
	t.Helper()
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != want {
		t.Fatalf("status = %d, want %d (%s)", apiErr.Status, want, apiErr.Message)
	}
}
