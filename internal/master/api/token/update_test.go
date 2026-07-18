package token

import (
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
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
		Context:      ginCtx,
		App:          application,
		UserInfo:     &app.UserInfo{UserID: 1, GroupID: 1, Role: 2},
		OwnerContext: t.Context(),
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

func setScope(ctx *app.Context, isAdmin bool, userID uint) {
	ctx.Context.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
}

func TestUpdate_UserModelWhitelistCapability(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setting    string
		models     any
		wantStatus int
		wantCode   string
		wantModels string
	}{
		{name: "disabled", setting: "false", models: `["gpt-4o"]`, wantStatus: 403, wantCode: "model_whitelist_edit_forbidden"},
		{name: "expand", setting: "true", models: `["gpt-4o","claude-.*"]`, wantModels: `["gpt-4o","claude-.*"]`},
		{name: "clear empty array", setting: "true", models: `[]`, wantModels: `[]`},
		{name: "clear empty string", setting: "true", models: "", wantModels: ""},
		{name: "invalid type", setting: "true", models: []any{"gpt-4o"}, wantStatus: 400},
		{name: "invalid pattern", setting: "true", models: `["["]`, wantStatus: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, ctx, db := setupTokenUpdateTest(t)
			tok := seedToken(t, db, nil)
			setScope(ctx, false, tok.UserID)
			if tc.setting != "" {
				if err := db.Create(&models.Setting{Key: consts.SettingKeyTokenModelWhitelistSelfService, Value: tc.setting}).Error; err != nil {
					t.Fatal(err)
				}
			}
			req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
			req.SetBodyMap(map[string]any{"models": tc.models})
			_, err := h.Update(ctx, req)
			if tc.wantStatus != 0 {
				if err == nil {
					t.Fatalf("expected status %d", tc.wantStatus)
				}
				apiErr, ok := err.(*api.APIError)
				if !ok || apiErr.Status != tc.wantStatus {
					t.Fatalf("error = %#v, want status %d", err, tc.wantStatus)
				}
				if apiErr.Code != tc.wantCode {
					t.Fatalf("code = %q, want %q", apiErr.Code, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var reloaded models.Token
			if err := db.First(&reloaded, tok.ID).Error; err != nil {
				t.Fatal(err)
			}
			if reloaded.Models != tc.wantModels {
				t.Fatalf("models = %q, want %q", reloaded.Models, tc.wantModels)
			}
		})
	}
}

func seedUserQuota(t *testing.T, db *gorm.DB, id uint, quota int64) {
	t.Helper()
	u := models.User{Quota: quota}
	u.ID = id
	u.Username = fmt.Sprintf("user-%d", id)
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedTokenStatus(t *testing.T, db *gorm.DB, userID uint, status int) models.Token {
	t.Helper()
	tok := models.Token{Name: "tok", Key: fmt.Sprintf("sk-%d-%d", userID, status), UserID: userID, Status: status}
	if err := db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	// models.Token.Status has `gorm:"default:1"`, so a struct value of 0 falls back
	// to 1 at insert. Force the requested status explicitly so seeding is honest.
	if err := db.Model(&models.Token{}).Where("id = ?", tok.ID).Update("status", status).Error; err != nil {
		t.Fatalf("seed token status: %v", err)
	}
	tok.Status = status
	return tok
}

// 普通用户、余额>0、status 0->1:成功。
func TestUpdate_UserEnableWithBalance_Success(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	seedUserQuota(t, db, 1, 1000)
	tok := seedTokenStatus(t, db, 1, 0)
	setScope(ctx, false, 1)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"status": float64(1)})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != 1 {
		t.Fatalf("expected status 1, got %d", reloaded.Status)
	}
}

// 普通用户、余额==0、status 0->1:放行(0 是无钱但未欠债的合法态)。
func TestUpdate_UserEnableZeroBalance_Success(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	seedUserQuota(t, db, 1, 0)
	tok := seedTokenStatus(t, db, 1, 0)
	setScope(ctx, false, 1)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"status": float64(1)})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != 1 {
		t.Fatalf("expected status 1, got %d", reloaded.Status)
	}
}

// 普通用户、余额<0(欠债)、status 0->1:拒绝(400),token 仍禁用。
func TestUpdate_UserEnableNegativeBalance_Reject(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	seedUserQuota(t, db, 1, -1)
	tok := seedTokenStatus(t, db, 1, 0)
	setScope(ctx, false, 1)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"status": float64(1)})

	_, err := h.Update(ctx, req)
	if err == nil {
		t.Fatal("expected 400 for negative balance enable")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("expected 400, got %d (%s)", apiErr.Status, apiErr.Message)
	}
	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != 0 {
		t.Fatalf("expected status unchanged 0, got %d", reloaded.Status)
	}
}

// 用户实际遇到的 bug:令牌本就启用、余额==0、只改 trace_enabled,
// 前端表单仍恒发 status=1。必须放行,不得误判为"启用动作"。
func TestUpdate_UserEditTraceOnEnabledTokenZeroBalance_Success(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	seedUserQuota(t, db, 1, 0)
	tok := seedTokenStatus(t, db, 1, 1) // 已启用
	setScope(ctx, false, 1)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"status": float64(1), "trace_enabled": true})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != 1 {
		t.Fatalf("expected status 1, got %d", reloaded.Status)
	}
	if !reloaded.TraceEnabled {
		t.Fatalf("expected trace_enabled true, got false")
	}
}

// 普通用户、status ->0(禁用):始终成功(无视余额)。
func TestUpdate_UserDisable_AlwaysAllowed(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	seedUserQuota(t, db, 1, 0)
	tok := seedTokenStatus(t, db, 1, 1)
	setScope(ctx, false, 1)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"status": float64(0)})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != 0 {
		t.Fatalf("expected status 0, got %d", reloaded.Status)
	}
}

// 管理员、余额<=0、status 0->1:成功(管理员不受余额校验影响)。
func TestUpdate_AdminEnableWithoutBalance_Success(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	seedUserQuota(t, db, 1, 0)
	tok := seedTokenStatus(t, db, 1, 0)
	setScope(ctx, true, 99)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"status": float64(1)})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var reloaded models.Token
	if err := db.First(&reloaded, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != 1 {
		t.Fatalf("expected status 1, got %d", reloaded.Status)
	}
}

func TestTokenUpdate_NormalUserCanSetBYOKOnly(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	tok := seedToken(t, db, nil)
	ctx.Context.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: false, UserID: tok.UserID})

	req := UpdateRequest{ID: strconv.FormatUint(uint64(tok.ID), 10)}
	req.SetBodyMap(map[string]any{"byok_only": true})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var got models.Token
	if err := db.First(&got, tok.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.BYOKOnly {
		t.Errorf("BYOKOnly = false, want true (normal user must be able to set it)")
	}
}
