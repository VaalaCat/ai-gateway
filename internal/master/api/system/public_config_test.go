package system

import (
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newPublicConfigTestContext(t *testing.T) (*app.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	a := app.NewApplication()
	a.SetDB(db)
	return &app.Context{Context: ginCtx, App: a}, db
}

func TestPublicConfig_DefaultsFalse(t *testing.T) {
	c, _ := newPublicConfigTestContext(t)
	resp, err := (&Handler{}).PublicConfig(c, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.RegistrationEnabled || resp.InviteEnabled {
		t.Fatalf("want both false, got %+v", resp)
	}
	if resp.InviteUserMaxCodes != 5 {
		t.Fatalf("want default invite_user_max_codes=5, got %d", resp.InviteUserMaxCodes)
	}
}

func TestPublicConfig_InviteUserMaxCodesZero(t *testing.T) {
	c, db := newPublicConfigTestContext(t)
	db.Create(&models.Setting{Key: "invite_user_max_codes", Value: "0"})
	resp, err := (&Handler{}).PublicConfig(c, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.InviteUserMaxCodes != 0 {
		t.Fatalf("want invite_user_max_codes=0, got %d", resp.InviteUserMaxCodes)
	}
}

func TestPublicConfig_ReflectsSettings(t *testing.T) {
	c, db := newPublicConfigTestContext(t)
	db.Create(&models.Setting{Key: "registration_enabled", Value: "true"})
	db.Create(&models.Setting{Key: "invite_enabled", Value: "true"})
	resp, err := (&Handler{}).PublicConfig(c, api.EmptyRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !resp.RegistrationEnabled || !resp.InviteEnabled {
		t.Fatalf("want both true, got %+v", resp)
	}
}
