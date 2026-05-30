package invite

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestListMine_OnlyOwn(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.InviteCode{Code: "MINE", CreatorID: 1, MaxUses: 1})
	db.Create(&models.InviteCode{Code: "OTHER", CreatorID: 2, MaxUses: 1})
	resp, err := (&Handler{}).ListMine(inviteCtx(t, db, false, 1), ListRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Total != 1 || len(resp.Data) != 1 || resp.Data[0].Code != "MINE" {
		t.Fatalf("expected only own code, got %+v", resp)
	}
}

func TestListMine_GateDisabled_Forbidden(t *testing.T) {
	db := newInviteDB(t)
	_, err := (&Handler{}).ListMine(inviteCtx(t, db, false, 1), ListRequest{})
	assertStatus(t, err, 403)
}

func TestDeleteMine_Own_Success(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	ic := models.InviteCode{Code: "DEL", CreatorID: 1, MaxUses: 1}
	db.Create(&ic)
	_, err := (&Handler{}).DeleteMine(inviteCtx(t, db, false, 1), api.IDPathRequest{ID: itoa(ic.ID)})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var cnt int64
	db.Model(&models.InviteCode{}).Where("id = ?", ic.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("expected code deleted, still %d rows", cnt)
	}
}

func TestDeleteMine_Others_NotFound(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	ic := models.InviteCode{Code: "NOPE", CreatorID: 2, MaxUses: 1}
	db.Create(&ic)
	_, err := (&Handler{}).DeleteMine(inviteCtx(t, db, false, 1), api.IDPathRequest{ID: itoa(ic.ID)})
	assertStatus(t, err, 404)
}

func TestAdminList_All(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.InviteCode{Code: "A", CreatorID: 1, MaxUses: 1})
	db.Create(&models.InviteCode{Code: "B", CreatorID: 2, MaxUses: 1})
	resp, err := (&Handler{}).AdminList(inviteCtx(t, db, true, 99), ListRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("admin total = %d, want 2", resp.Total)
	}
}

func TestAdminDelete_Any_Success(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	ic := models.InviteCode{Code: "X", CreatorID: 2, MaxUses: 1}
	db.Create(&ic)
	_, err := (&Handler{}).AdminDelete(inviteCtx(t, db, true, 99), api.IDPathRequest{ID: itoa(ic.ID)})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var cnt int64
	db.Model(&models.InviteCode{}).Where("id = ?", ic.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("expected deleted, still %d rows", cnt)
	}
}

func TestAdminList_GateDisabled_Forbidden(t *testing.T) {
	db := newInviteDB(t)
	_, err := (&Handler{}).AdminList(inviteCtx(t, db, true, 99), ListRequest{})
	assertStatus(t, err, 403)
}

func TestAdminDelete_GateDisabled_Forbidden(t *testing.T) {
	db := newInviteDB(t)
	_, err := (&Handler{}).AdminDelete(inviteCtx(t, db, true, 99), api.IDPathRequest{ID: "1"})
	assertStatus(t, err, 403)
}

func TestAdminList_FilterByCreator(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.InviteCode{Code: "A", CreatorID: 1, MaxUses: 1})
	db.Create(&models.InviteCode{Code: "B", CreatorID: 2, MaxUses: 1})
	resp, err := (&Handler{}).AdminList(inviteCtx(t, db, true, 99), ListRequest{CreatorID: "1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Total != 1 || len(resp.Data) != 1 || resp.Data[0].Code != "A" {
		t.Fatalf("creator filter: %+v", resp)
	}
}

func TestAdminList_SearchByCode(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.InviteCode{Code: "ALPHA", CreatorID: 1, MaxUses: 1})
	db.Create(&models.InviteCode{Code: "BETA", CreatorID: 1, MaxUses: 1})
	resp, err := (&Handler{}).AdminList(inviteCtx(t, db, true, 99), ListRequest{Search: "ALP"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Total != 1 || len(resp.Data) != 1 || resp.Data[0].Code != "ALPHA" {
		t.Fatalf("search: %+v", resp)
	}
}

func TestListMine_SearchByCode(t *testing.T) {
	db := newInviteDB(t)
	setEnabled(t, db)
	db.Create(&models.InviteCode{Code: "MINEALPHA", CreatorID: 1, MaxUses: 1})
	db.Create(&models.InviteCode{Code: "MINEBETA", CreatorID: 1, MaxUses: 1})
	db.Create(&models.InviteCode{Code: "OTHERALPHA", CreatorID: 2, MaxUses: 1})
	resp, err := (&Handler{}).ListMine(inviteCtx(t, db, false, 1), ListRequest{Search: "ALPHA"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Total != 1 || len(resp.Data) != 1 || resp.Data[0].Code != "MINEALPHA" {
		t.Fatalf("mine search: %+v", resp)
	}
}
