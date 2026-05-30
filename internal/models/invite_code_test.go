package models

import (
	"testing"
)

func TestInviteCode_CreateAndUniqueCode(t *testing.T) {
	db := setupTestDB(t)
	ic := InviteCode{Code: "ABC123", CreatorID: 1, MaxUses: 1}
	if err := db.Create(&ic).Error; err != nil {
		t.Fatalf("create invite code: %v", err)
	}
	if ic.ID == 0 {
		t.Fatal("expected ID to be assigned")
	}
	dup := InviteCode{Code: "ABC123", CreatorID: 2, MaxUses: 1}
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("expected unique-index violation on duplicate code")
	}
}

func TestInviteCode_MaxUsesDefaultsToOne(t *testing.T) {
	db := setupTestDB(t)
	ic := InviteCode{Code: "DEF", CreatorID: 1}
	if err := db.Create(&ic).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got InviteCode
	if err := db.First(&got, ic.ID).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.MaxUses != 1 {
		t.Fatalf("MaxUses default = %d, want 1", got.MaxUses)
	}
}

func TestInviteRedemption_UniqueInvitee(t *testing.T) {
	db := setupTestDB(t)
	if err := db.Create(&InviteRedemption{InviteCodeID: 1, Code: "X", InviterID: 1, InviteeID: 10}).Error; err != nil {
		t.Fatalf("create redemption: %v", err)
	}
	err := db.Create(&InviteRedemption{InviteCodeID: 2, Code: "Y", InviterID: 1, InviteeID: 10}).Error
	if err == nil {
		t.Fatal("expected unique-index violation on duplicate invitee_id")
	}
}
