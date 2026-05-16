package user

import (
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupUserTestDB(t *testing.T) *gorm.DB {
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

func newUserTestContext(db *gorm.DB) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	testApp := app.NewApplication()
	testApp.SetDB(db)
	return &app.Context{
		Context: ginCtx,
		App:     testApp,
	}
}

func TestRegistrationStatus_DefaultsToDisabledWhenSettingMissing(t *testing.T) {
	db := setupUserTestDB(t)
	h := &Handler{}
	c := newUserTestContext(db)

	resp, err := h.RegistrationStatus(c, struct{}{})
	if err != nil {
		t.Fatalf("RegistrationStatus returned error: %v", err)
	}
	if resp.RegistrationEnabled {
		t.Fatalf("registration_enabled = true, want false")
	}
}

func TestRegistrationStatus_ReturnsTrueWhenSettingEnabled(t *testing.T) {
	db := setupUserTestDB(t)
	if err := db.Create(&models.Setting{Key: "registration_enabled", Value: "true"}).Error; err != nil {
		t.Fatalf("create setting failed: %v", err)
	}

	h := &Handler{}
	c := newUserTestContext(db)

	resp, err := h.RegistrationStatus(c, struct{}{})
	if err != nil {
		t.Fatalf("RegistrationStatus returned error: %v", err)
	}
	if !resp.RegistrationEnabled {
		t.Fatalf("registration_enabled = false, want true")
	}
}
