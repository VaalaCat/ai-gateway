package token

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func setupTokenUpdateTest(t *testing.T) (*Handler, *app.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)

	ctx := &app.Context{
		Context:  ginCtx,
		App:      application,
		UserInfo: &app.UserInfo{UserID: 1, GroupID: 1, Role: 2},
	}

	return &Handler{}, ctx, db
}

func seedToken(t *testing.T, db *gorm.DB, channelIDs []uint) models.Token {
	t.Helper()
	tok := models.Token{Name: "tok", Key: "sk-test", UserID: 1, Status: 1}
	tok.AllowedChannelIDs = datatypes.JSONSlice[uint](channelIDs)
	if err := db.Create(&tok).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	return tok
}

// TestUpdate_ClearAllowedChannelIDs_Success: PATCH {"allowed_channel_ids": []}
// must persist as empty slice (= "无限制" semantics). This is the core bug lock.
func TestUpdate_ClearAllowedChannelIDs_Success(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	tok := seedToken(t, db, []uint{1, 2, 3})

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"allowed_channel_ids": []any{}})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}

	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.AllowedChannelIDs) != 0 {
		t.Fatalf("expected empty AllowedChannelIDs, got %v", reloaded.AllowedChannelIDs)
	}
}

// TestUpdate_PartialClearAllowedChannelIDs_Boundary: shrinking from [1,2,3] to [1].
func TestUpdate_PartialClearAllowedChannelIDs_Boundary(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	tok := seedToken(t, db, []uint{1, 2, 3})

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"allowed_channel_ids": []any{float64(1)}})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}

	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.AllowedChannelIDs) != 1 || reloaded.AllowedChannelIDs[0] != 1 {
		t.Fatalf("expected [1], got %v", reloaded.AllowedChannelIDs)
	}
}

// TestUpdate_OmitAllowedChannelIDs_NoChange: PATCH 不带 channel key 时,
// 现有 channel 必须保留 (验证 "未提供" ≠ "清空" 语义)。
func TestUpdate_OmitAllowedChannelIDs_NoChange(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	tok := seedToken(t, db, []uint{1, 2, 3})

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"name": "renamed"})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}

	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	want := []uint{1, 2, 3}
	if len(reloaded.AllowedChannelIDs) != len(want) {
		t.Fatalf("expected %v unchanged, got %v", want, reloaded.AllowedChannelIDs)
	}
	for i, v := range want {
		if reloaded.AllowedChannelIDs[i] != v {
			t.Fatalf("expected %v unchanged, got %v", want, reloaded.AllowedChannelIDs)
		}
	}
	if reloaded.Name != "renamed" {
		t.Fatalf("name not updated: %s", reloaded.Name)
	}
}

// TestUpdate_IllegalZeroChannelID_Reject: id=0 must be rejected by validator.
func TestUpdate_IllegalZeroChannelID_Reject(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	tok := seedToken(t, db, []uint{1, 2})

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"allowed_channel_ids": []any{float64(0)}})

	_, err := h.Update(ctx, req)
	if err == nil {
		t.Fatal("expected 400 error for zero channel id")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("expected 400, got %d (%s)", apiErr.Status, apiErr.Message)
	}
}
