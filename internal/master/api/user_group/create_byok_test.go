package user_group

import (
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupBYOKTest(t *testing.T) (*Handler, *app.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatal(err)
	}

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)

	ctx := &app.Context{
		Context: ginCtx,
		App:     application,
		UserInfo: &app.UserInfo{
			UserID:  1,
			GroupID: 1,
			Role:    2,
		},
	}

	h := &Handler{Bus: application.GetEventBus()}
	return h, ctx, db
}

func TestCreate_WithBYOKFields(t *testing.T) {
	h, ctx, db := setupBYOKTest(t)

	enabled := true
	maxCh := 50
	req := CreateRequest{
		Name:            "pro",
		BYOKEnabled:     &enabled,
		BYOKMaxChannels: &maxCh,
	}

	_, err := h.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var g models.UserGroup
	db.Where("name = ?", "pro").First(&g)
	if g.BYOKEnabled == nil || !*g.BYOKEnabled {
		t.Fatalf("byok_enabled not persisted: %v", g.BYOKEnabled)
	}
	if g.BYOKMaxChannels == nil || *g.BYOKMaxChannels != 50 {
		t.Fatalf("byok_max_channels not persisted: got %v", g.BYOKMaxChannels)
	}
}

func TestCreate_BYOKFieldsNil(t *testing.T) {
	h, ctx, db := setupBYOKTest(t)

	req := CreateRequest{Name: "default-inherit"}
	_, err := h.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var g models.UserGroup
	db.Where("name = ?", "default-inherit").First(&g)
	if g.BYOKEnabled != nil {
		t.Fatalf("byok_enabled should be nil for inherit semantics: %v", *g.BYOKEnabled)
	}
	if g.BYOKMaxChannels != nil {
		t.Fatalf("byok_max_channels should be nil for inherit: %v", *g.BYOKMaxChannels)
	}
}

func TestCreate_BYOKDisabledExplicit(t *testing.T) {
	h, ctx, db := setupBYOKTest(t)

	disabled := false
	maxCh := 0
	req := CreateRequest{
		Name:            "restricted",
		BYOKEnabled:     &disabled,
		BYOKMaxChannels: &maxCh,
	}

	_, err := h.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var g models.UserGroup
	db.Where("name = ?", "restricted").First(&g)
	if g.BYOKEnabled == nil || *g.BYOKEnabled {
		t.Fatalf("byok_enabled should be false: %v", g.BYOKEnabled)
	}
	if g.BYOKMaxChannels == nil || *g.BYOKMaxChannels != 0 {
		t.Fatalf("byok_max_channels should be 0: got %v", g.BYOKMaxChannels)
	}
}
