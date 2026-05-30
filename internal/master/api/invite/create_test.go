package invite

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestCreate_GateDisabled_Forbidden(t *testing.T) {
	db := newInviteDB(t) // invite_enabled 未设置 => false
	_, err := (&Handler{}).Create(inviteCtx(t, db, false, 1), CreateRequest{})
	assertStatus(t, err, 403)
}

func TestCreate_UserUnderCap_Success(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	out, err := (&Handler{}).Create(inviteCtx(t, db, false, 1), CreateRequest{MaxUses: 1})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Value.Code == "" || out.Value.CreatorID != 1 {
		t.Fatalf("bad result: %+v", out.Value)
	}
}

func TestCreate_UserAtCap_Rejected(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.Setting{Key: consts.SettingKeyInviteUserMaxCodes, Value: "1"})
	db.Create(&models.InviteCode{Code: "EXIST", CreatorID: 1, MaxUses: 1, UsedCount: 0}) // 1 active
	_, err := (&Handler{}).Create(inviteCtx(t, db, false, 1), CreateRequest{MaxUses: 1})
	assertStatus(t, err, 400)
}

func TestCreate_UserCreationDisabled_Forbidden(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.Setting{Key: consts.SettingKeyInviteUserMaxCodes, Value: "0"})
	_, err := (&Handler{}).Create(inviteCtx(t, db, false, 1), CreateRequest{MaxUses: 1})
	assertStatus(t, err, 403)
}

func TestCreate_UserMaxUsesOverLimit_Rejected(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.Setting{Key: consts.SettingKeyInviteUserMaxUses, Value: "1"})
	_, err := (&Handler{}).Create(inviteCtx(t, db, false, 1), CreateRequest{MaxUses: 5})
	assertStatus(t, err, 400)
}

func TestCreate_NoUserInfo_Unauthorized(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	c := inviteCtx(t, db, false, 1)
	c.UserInfo = nil
	_, err := (&Handler{}).Create(c, CreateRequest{})
	assertStatus(t, err, 401)
}

func TestCreate_AdminOverCap_Success(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.Setting{Key: consts.SettingKeyInviteUserMaxCodes, Value: "0"})
	db.Create(&models.Setting{Key: consts.SettingKeyInviteUserMaxUses, Value: "1"})
	out, err := (&Handler{}).Create(inviteCtx(t, db, true, 99), CreateRequest{MaxUses: 1000})
	if err != nil {
		t.Fatalf("admin create: %v", err)
	}
	if out.Value.MaxUses != 1000 {
		t.Fatalf("maxUses = %d, want 1000", out.Value.MaxUses)
	}
}
