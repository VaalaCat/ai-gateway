package invite

import (
	"encoding/json"
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
	if out.Value.Code == "" || out.Value.CreatorID != 1 || out.Value.GroupID != 0 {
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

func TestCreate_UserCannotSupplyGroup(t *testing.T) {
	for _, groupID := range []uint{0, models.DefaultUserGroupID, 8} {
		db := newInviteDB(t)
		setEnabled(t, db)
		var req CreateRequest
		if err := json.Unmarshal([]byte(`{"group_id":`+itoa(groupID)+`}`), &req); err != nil {
			t.Fatal(err)
		}
		_, err := (&Handler{}).Create(inviteCtx(t, db, false, 1), req)
		assertStatus(t, err, 403)
		var count int64
		db.Model(&models.InviteCode{}).Count(&count)
		if count != 0 {
			t.Fatalf("group %d: forbidden create persisted %d codes", groupID, count)
		}
	}
}

func TestCreate_AdminSelectsGroup(t *testing.T) {
	for _, groupID := range []uint{0, models.DefaultUserGroupID, 8} {
		db := newInviteDB(t)
		setEnabled(t, db)
		if err := db.Create(&models.UserGroup{ID: 8, Name: "selected"}).Error; err != nil {
			t.Fatal(err)
		}
		out, err := (&Handler{}).Create(inviteCtx(t, db, true, 99), CreateRequest{GroupID: &groupID})
		if err != nil {
			t.Fatal(err)
		}
		var stored models.InviteCode
		if err := db.First(&stored, out.Value.ID).Error; err != nil {
			t.Fatal(err)
		}
		if out.Value.GroupID != groupID || stored.GroupID != groupID {
			t.Fatalf("selected %d: response=%+v stored=%+v", groupID, out.Value, stored)
		}
	}
}

func TestCreate_AdminRejectsUnavailableGroup(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		db := newInviteDB(t)
		setEnabled(t, db)
		groupID := uint(8)
		if disabled {
			if err := db.Create(&models.UserGroup{ID: groupID, Name: "disabled", Status: consts.StatusDisabled}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.UserGroup{}).Where("id = ?", groupID).Update("status", consts.StatusDisabled).Error; err != nil {
				t.Fatal(err)
			}
		}
		_, err := (&Handler{}).Create(inviteCtx(t, db, true, 99), CreateRequest{GroupID: &groupID})
		assertStatus(t, err, 400)
		var count int64
		db.Model(&models.InviteCode{}).Count(&count)
		if count != 0 {
			t.Fatalf("unavailable group persisted %d codes", count)
		}
	}
}
