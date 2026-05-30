package dao

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestInviteCode_CreateGetByCode(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	m := NewAdminMutation(ctx).InviteCode()
	q := NewAdminQuery(ctx).InviteCode()
	if err := m.Create(&models.InviteCode{Code: "CODE1", CreatorID: 7, MaxUses: 3}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := q.GetByCode("CODE1")
	if err != nil {
		t.Fatalf("getbycode: %v", err)
	}
	if got.CreatorID != 7 || got.MaxUses != 3 {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestInviteCode_CountActiveExcludesUsedUpAndExpired(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).InviteCode()
	now := int64(1000)
	db.Create(&models.InviteCode{Code: "A", CreatorID: 1, MaxUses: 2, UsedCount: 1, ExpiresAt: 0})  // active
	db.Create(&models.InviteCode{Code: "B", CreatorID: 1, MaxUses: 1, UsedCount: 1})                 // used up
	db.Create(&models.InviteCode{Code: "C", CreatorID: 1, MaxUses: 5, UsedCount: 0, ExpiresAt: 500}) // expired
	db.Create(&models.InviteCode{Code: "D", CreatorID: 2, MaxUses: 1, UsedCount: 0})                 // other creator
	n, err := q.CountActiveByCreator(1, now)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("active = %d, want 1", n)
	}
}

func TestInviteCode_ListAllByCreatorPagination(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).InviteCode()
	for i := 0; i < 3; i++ {
		db.Create(&models.InviteCode{Code: "X" + string(rune('a'+i)), CreatorID: 9, MaxUses: 1})
	}
	db.Create(&models.InviteCode{Code: "other", CreatorID: 10, MaxUses: 1})
	cid := uint(9)
	codes, total, err := q.ListAll(ListOptions{Page: 1, PageSize: 2}, InviteCodeListFilter{CreatorID: &cid})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(codes) != 2 {
		t.Fatalf("total=%d len=%d, want 3 and 2", total, len(codes))
	}
}

func TestInviteCode_ListAllSearchByCode(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).InviteCode()
	db.Create(&models.InviteCode{Code: "ALPHA", CreatorID: 1, MaxUses: 1})
	db.Create(&models.InviteCode{Code: "BETA", CreatorID: 1, MaxUses: 1})
	codes, total, err := q.ListAll(ListOptions{Page: 1, PageSize: 10}, InviteCodeListFilter{Search: "ALP"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(codes) != 1 || codes[0].Code != "ALPHA" {
		t.Fatalf("search total=%d codes=%+v", total, codes)
	}
}

func TestInviteCode_RedeemSuccessIncrements(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx).InviteCode()
	db.Create(&models.InviteCode{Code: "RED", CreatorID: 1, MaxUses: 2, UsedCount: 0})
	ic, err := m.Redeem("RED", 1000)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if ic.UsedCount != 1 {
		t.Fatalf("used_count = %d, want 1", ic.UsedCount)
	}
}

func TestInviteCode_RedeemExhaustedUnavailable(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx).InviteCode()
	db.Create(&models.InviteCode{Code: "FULL", CreatorID: 1, MaxUses: 1, UsedCount: 1})
	if _, err := m.Redeem("FULL", 1000); !errors.Is(err, ErrInviteCodeUnavailable) {
		t.Fatalf("err = %v, want ErrInviteCodeUnavailable", err)
	}
}

func TestInviteCode_RedeemExpiredUnavailable(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx).InviteCode()
	db.Create(&models.InviteCode{Code: "EXP", CreatorID: 1, MaxUses: 5, UsedCount: 0, ExpiresAt: 500})
	if _, err := m.Redeem("EXP", 1000); !errors.Is(err, ErrInviteCodeUnavailable) {
		t.Fatalf("err = %v, want ErrInviteCodeUnavailable", err)
	}
}

func TestInviteCode_RedeemNonexistentUnavailable(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	m := NewAdminMutation(ctx).InviteCode()
	if _, err := m.Redeem("NOSUCHCODE", 1000); !errors.Is(err, ErrInviteCodeUnavailable) {
		t.Fatalf("err = %v, want ErrInviteCodeUnavailable", err)
	}
}

func TestInviteCode_RedeemLastSlotOnlyOnce(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx).InviteCode()
	db.Create(&models.InviteCode{Code: "ONE", CreatorID: 1, MaxUses: 1, UsedCount: 0})
	if _, err := m.Redeem("ONE", 1000); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, err := m.Redeem("ONE", 1000); !errors.Is(err, ErrInviteCodeUnavailable) {
		t.Fatalf("second redeem err = %v, want unavailable", err)
	}
}

func TestInviteCode_RecordRedemption(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx).InviteCode()
	if err := m.RecordRedemption(&models.InviteRedemption{InviteCodeID: 1, Code: "Z", InviterID: 1, InviteeID: 99}); err != nil {
		t.Fatalf("record: %v", err)
	}
	var cnt int64
	db.Model(&models.InviteRedemption{}).Where("invitee_id = ?", 99).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("redemption rows = %d, want 1", cnt)
	}
}
