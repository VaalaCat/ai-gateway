package log

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newLogTestCtx 构造最小 handler + DB + Application 三件套。
func newLogTestCtx(t *testing.T) (*Handler, *gorm.DB, app.Application) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"})
	db.Create(&models.User{ID: 2, GroupID: 1, Username: "bob"})

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	return &Handler{}, db, application
}

// makeCtx 构造 *app.Context 并把 RequestScope 写入 gin context。
func makeCtx(application app.Application, userID uint, isAdmin bool) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
	return &app.Context{
		Context:  ginCtx,
		App:      application,
		UserInfo: &app.UserInfo{UserID: userID, GroupID: 1},
	}
}

func seedPC(t *testing.T, db *gorm.DB, id uint, ownerID uint) {
	t.Helper()
	pc := models.PrivateChannel{
		ChannelCore: models.ChannelCore{ID: id, Type: 1},
		OwnerID:     ownerID,
		Name:        "k",
	}
	if err := db.Create(&pc).Error; err != nil {
		t.Fatalf("seed pc: %v", err)
	}
}

func seedLog(t *testing.T, db *gorm.DB, l models.UsageLog) {
	t.Helper()
	if err := db.Select("*").Create(&l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

func TestList_NormalUser_OwnsPrivateChannel_ReturnsRows(t *testing.T) {
	h, db, application := newLogTestCtx(t)
	seedPC(t, db, 42, 1)
	seedLog(t, db, models.UsageLog{UserID: 1, OwnerType: "private", PrivateChannelID: 42, ModelName: "claude", Status: 1, RequestID: "a"})

	ctx := makeCtx(application, 1, false)
	resp, err := h.List(ctx, ListRequest{PrivateChannelID: "42"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("total = %d, want 1", resp.Total)
	}
	if resp.Data[0].PrivateChannelID != 42 {
		t.Fatalf("pcid = %d, want 42", resp.Data[0].PrivateChannelID)
	}
	if resp.Data[0].ChannelID != 0 {
		t.Fatalf("ChannelID should be wiped to 0, got %d", resp.Data[0].ChannelID)
	}
}

func TestList_Admin_AnyPrivateChannel_NoOwnershipCheck(t *testing.T) {
	h, db, application := newLogTestCtx(t)
	seedPC(t, db, 42, 2) // owner is user 2
	seedLog(t, db, models.UsageLog{UserID: 2, OwnerType: "private", PrivateChannelID: 42, ModelName: "claude", Status: 1, RequestID: "b"})

	// admin (UserID=1) querying pc owned by user 2
	ctx := makeCtx(application, 1, true)
	resp, err := h.List(ctx, ListRequest{PrivateChannelID: "42"})
	if err != nil {
		t.Fatalf("admin should succeed without ownership: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("admin total = %d, want 1", resp.Total)
	}
}

func TestList_NormalUser_NotOwned_403(t *testing.T) {
	h, db, application := newLogTestCtx(t)
	seedPC(t, db, 42, 2) // owner is user 2

	ctx := makeCtx(application, 1, false)
	_, err := h.List(ctx, ListRequest{PrivateChannelID: "42"})
	if err == nil {
		t.Fatalf("expected 403, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Fatalf("err = %v, want 403", err)
	}
}

func TestList_NormalUser_NotExistChannel_Also_403(t *testing.T) {
	h, _, application := newLogTestCtx(t)
	ctx := makeCtx(application, 1, false)
	_, err := h.List(ctx, ListRequest{PrivateChannelID: "99999"})
	if err == nil {
		t.Fatalf("expected 403, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Fatalf("err = %v, want 403 (anti-probing)", err)
	}
}

func TestList_InvalidPrivateChannelID_400(t *testing.T) {
	h, _, application := newLogTestCtx(t)
	ctx := makeCtx(application, 1, false)
	_, err := h.List(ctx, ListRequest{PrivateChannelID: "abc"})
	if err == nil {
		t.Fatalf("expected 400, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Fatalf("err = %v, want 400", err)
	}
}

func TestList_WithTimeWindow_FiltersByCreatedAt(t *testing.T) {
	h, db, application := newLogTestCtx(t)
	seedLog(t, db, models.UsageLog{UserID: 1, ModelName: "m", Status: 1, RequestID: "a", CreatedAt: 1000})
	seedLog(t, db, models.UsageLog{UserID: 1, ModelName: "m", Status: 1, RequestID: "b", CreatedAt: 2000})
	seedLog(t, db, models.UsageLog{UserID: 1, ModelName: "m", Status: 1, RequestID: "c", CreatedAt: 3000})

	ctx := makeCtx(application, 1, true)
	resp, err := h.List(ctx, ListRequest{
		TimeWindowQuery: listfilter.TimeWindowQuery{Start: 1500, End: 3000},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("total = %d, want 1 (only created_at=2000)", resp.Total)
	}
}

func TestList_RangeOutOfBounds_Returns400(t *testing.T) {
	h, _, application := newLogTestCtx(t)
	ctx := makeCtx(application, 1, true)
	// start=0, end far in future → > MaxLogsListDays (365)
	_, err := h.List(ctx, ListRequest{
		TimeWindowQuery: listfilter.TimeWindowQuery{Start: 0, End: 366 * 86400},
	})
	if err == nil {
		t.Fatal("expected error for range > 365 days, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("status = %d, want 400", apiErr.Status)
	}
}

func TestList_NoTimeWindow_ReturnsAll(t *testing.T) {
	h, db, application := newLogTestCtx(t)
	seedLog(t, db, models.UsageLog{UserID: 1, ModelName: "m", Status: 1, RequestID: "a", CreatedAt: 1000})
	seedLog(t, db, models.UsageLog{UserID: 1, ModelName: "m", Status: 1, RequestID: "b", CreatedAt: 2000})

	ctx := makeCtx(application, 1, true)
	resp, err := h.List(ctx, ListRequest{}) // no TimeWindowQuery
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("total = %d, want 2 (no time filter)", resp.Total)
	}
}
