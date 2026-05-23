package dao

import (
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

func TestPrivateChannel_GetByID(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	if err := db.Create(pc).Error; err != nil {
		t.Fatal(err)
	}

	q := NewAdminQuery(NewContext(a))
	got, err := q.PrivateChannel().GetByID(pc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != 1 || got.Name != "a" {
		t.Fatalf("got %+v", got)
	}
}

func TestPrivateChannel_ListVisibleTo_OwnerOnly(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 2, Name: "b", Status: 1})

	q := NewAdminQuery(NewContext(a))
	rows, err := q.PrivateChannel().ListVisibleTo(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].OwnerID != 1 {
		t.Fatalf("want only owner=1 row, got %+v", rows)
	}
}

func TestPrivateChannel_ListVisibleTo_DisabledExcluded(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	// Status has a DB default of 1; force status=0 via explicit column update after create.
	pcDisabled := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "b", Status: 1}
	db.Create(pcDisabled)
	db.Model(pcDisabled).UpdateColumn("status", 0)

	q := NewAdminQuery(NewContext(a))
	rows, _ := q.PrivateChannel().ListVisibleTo(1, nil)
	if len(rows) != 1 || rows[0].Name != "a" {
		t.Fatalf("disabled row not excluded: %+v", rows)
	}
}

func TestPrivateChannel_ListVisibleTo_ShareUser(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	db.Create(pc)
	db.Create(&models.PrivateChannelShare{ChannelID: pc.ID, TargetType: "user", TargetID: 2})

	q := NewAdminQuery(NewContext(a))
	rows, _ := q.PrivateChannel().ListVisibleTo(2, nil)
	if len(rows) != 1 || rows[0].ID != pc.ID {
		t.Fatalf("share-user not visible: %+v", rows)
	}
}

func TestPrivateChannel_ListVisibleTo_ShareGroup(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	db.Create(pc)
	db.Create(&models.PrivateChannelShare{ChannelID: pc.ID, TargetType: "group", TargetID: 5})

	q := NewAdminQuery(NewContext(a))
	rows, _ := q.PrivateChannel().ListVisibleTo(2, []uint{5})
	if len(rows) != 1 || rows[0].ID != pc.ID {
		t.Fatalf("share-group not visible: %+v", rows)
	}
}

func TestPrivateChannel_ListVisibleTo_DedupOwnerAndShare(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	db.Create(pc)
	db.Create(&models.PrivateChannelShare{ChannelID: pc.ID, TargetType: "user", TargetID: 1})

	q := NewAdminQuery(NewContext(a))
	rows, _ := q.PrivateChannel().ListVisibleTo(1, nil)
	if len(rows) != 1 {
		t.Fatalf("owner+share duplicate, want 1 row, got %d", len(rows))
	}
}

func TestPrivateChannel_CountByOwner(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "b", Status: 0})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 2, Name: "c", Status: 1})

	q := NewAdminQuery(NewContext(a))
	n, _ := q.PrivateChannel().CountByOwner(1)
	if n != 2 {
		t.Fatalf("CountByOwner(1)=%d want 2", n)
	}
}

func TestPrivateChannel_Update_DropsReservedKeys(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	if err := db.Create(pc).Error; err != nil {
		t.Fatal(err)
	}

	m := NewAdminMutation(NewContext(a))
	err := m.PrivateChannel().Update(pc.ID, 1, map[string]any{
		"name":       "b",
		"owner_id":   uint(99),
		"key_cipher": []byte("xx"),
	})
	if err != nil {
		t.Fatal(err)
	}

	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if got.OwnerID != 1 {
		t.Errorf("owner_id was overwritten: %d", got.OwnerID)
	}
	if len(got.KeyCipher) != 0 {
		t.Errorf("key_cipher was overwritten via Update")
	}
	if got.Name != "b" {
		t.Errorf("name not updated: %q", got.Name)
	}
}

func TestPrivateChannel_Update_ForbidsAcrossOwner(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	if err := db.Create(pc).Error; err != nil {
		t.Fatal(err)
	}

	m := NewAdminMutation(NewContext(a))
	// user 2 attempts to update user 1's channel — gorm returns no error, just 0 rows affected
	err := m.PrivateChannel().Update(pc.ID, 2, map[string]any{"name": "hacked"})
	if err != nil {
		t.Fatal(err)
	}

	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if got.Name != "a" {
		t.Fatalf("cross-owner update succeeded: %q", got.Name)
	}
}

func TestPrivateChannel_UpdateKey(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	if err := db.Create(pc).Error; err != nil {
		t.Fatal(err)
	}

	m := NewAdminMutation(NewContext(a))
	if err := m.PrivateChannel().UpdateKey(pc.ID, 1, []byte("newcipher"), "wxyz"); err != nil {
		t.Fatal(err)
	}
	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if string(got.KeyCipher) != "newcipher" || got.KeyLast4 != "wxyz" {
		t.Fatalf("UpdateKey didn't apply: %+v", got)
	}
}

func TestPrivateChannel_Delete_ForbidsAcrossOwner(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	if err := db.Create(pc).Error; err != nil {
		t.Fatal(err)
	}

	m := NewAdminMutation(NewContext(a))
	// user 2 tries to delete user 1's channel — should affect 0 rows
	if err := m.PrivateChannel().Delete(pc.ID, 2); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Model(&models.PrivateChannel{}).Where("id = ?", pc.ID).Count(&count)
	if count != 1 {
		t.Fatalf("cross-owner delete succeeded; count=%d", count)
	}

	// owner can delete it
	if err := m.PrivateChannel().Delete(pc.ID, 1); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.PrivateChannel{}).Where("id = ?", pc.ID).Count(&count)
	if count != 0 {
		t.Fatalf("owner delete failed; count=%d", count)
	}
}

func TestPrivateChannel_AdminDisable(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	if err := db.Create(pc).Error; err != nil {
		t.Fatal(err)
	}

	m := NewAdminMutation(NewContext(a))
	if err := m.PrivateChannel().AdminDisable(pc.ID); err != nil {
		t.Fatal(err)
	}
	// Read via UpdateColumn-style direct query (because gorm default:1 makes struct literal status=0 ambiguous)
	var status int
	db.Model(&models.PrivateChannel{}).Where("id = ?", pc.ID).Select("status").Scan(&status)
	if status != 0 {
		t.Fatalf("status not disabled: %d", status)
	}
}

// --- DeleteByOwner / DeleteSharesByTarget (cascade-clear on user delete) ---

func TestPrivateChannel_DeleteByOwner_RemovesAllChannelsForOwner(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "ch-a", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "ch-b", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 2, Name: "other-user-ch", Status: 1})

	m := NewAdminMutation(NewContext(a))
	if err := m.PrivateChannel().DeleteByOwner(1); err != nil {
		t.Fatal(err)
	}

	q := NewAdminQuery(NewContext(a))
	list, _, err := q.PrivateChannel().ListOwnedBy(1, ListOptions{Page: 1, PageSize: 100}, PrivateChannelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 channels for owner=1, got %d", len(list))
	}
	other, _, err := q.PrivateChannel().ListOwnedBy(2, ListOptions{Page: 1, PageSize: 100}, PrivateChannelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 {
		t.Fatalf("DeleteByOwner must not affect other owner; got %d rows for owner=2", len(other))
	}
}

func TestPrivateChannel_DeleteByOwner_NoRowsIsNotError(t *testing.T) {
	a, _ := setupTestApp(t)
	m := NewAdminMutation(NewContext(a))
	if err := m.PrivateChannel().DeleteByOwner(9999); err != nil {
		t.Fatalf("missing owner must not error: %v", err)
	}
}

func TestPrivateChannelShare_DeleteSharesByTarget_RemovesAllSharesForUser(t *testing.T) {
	a, db := setupTestApp(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1}
	db.Create(pc)
	db.Create(&models.PrivateChannelShare{ChannelID: pc.ID, TargetType: models.PrivateShareTargetUser, TargetID: 2})
	db.Create(&models.PrivateChannelShare{ChannelID: pc.ID, TargetType: models.PrivateShareTargetUser, TargetID: 3})
	// Group share to user 2's group must NOT be touched by a user-target delete.
	db.Create(&models.PrivateChannelShare{ChannelID: pc.ID, TargetType: models.PrivateShareTargetGroup, TargetID: 2})

	m := NewAdminMutation(NewContext(a))
	if err := m.PrivateChannelShare().DeleteSharesByTarget(models.PrivateShareTargetUser, 2); err != nil {
		t.Fatal(err)
	}

	q := NewAdminQuery(NewContext(a))
	// user 2 user-target share removed
	u2, err := q.PrivateChannelShare().ListSharesForUser(2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(u2) != 0 {
		t.Fatalf("user 2 user-target shares not removed: %+v", u2)
	}
	// user 3 still has its share
	u3, _ := q.PrivateChannelShare().ListSharesForUser(3, nil)
	if len(u3) != 1 {
		t.Fatalf("user 3 share wrongly affected; got %d", len(u3))
	}
	// group=2 share row still present
	var groupShareCount int64
	db.Model(&models.PrivateChannelShare{}).
		Where("target_type = ? AND target_id = ?", models.PrivateShareTargetGroup, 2).
		Count(&groupShareCount)
	if groupShareCount != 1 {
		t.Fatalf("group-target share wrongly affected by user-target delete; remaining=%d", groupShareCount)
	}
}

func TestPrivateChannelShare_DeleteSharesByTarget_NoRowsIsNotError(t *testing.T) {
	a, _ := setupTestApp(t)
	m := NewAdminMutation(NewContext(a))
	if err := m.PrivateChannelShare().DeleteSharesByTarget(models.PrivateShareTargetUser, 9999); err != nil {
		t.Fatalf("missing target must not error: %v", err)
	}
}

// --- CountByBaseURLPrefix (admin BaseURL allowlist usage lookup) ---

func TestPrivateChannel_CountByBaseURLPrefix_MatchesPrefixAndReturnsOwners(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v1/"},
		OwnerID:     1, Name: "a", Status: 1,
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v1/"},
		OwnerID:     1, Name: "b", Status: 1,
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.anthropic.com/v1/"},
		OwnerID:     2, Name: "c", Status: 1,
	})

	q := NewAdminQuery(NewContext(a))
	count, owners, err := q.PrivateChannel().CountByBaseURLPrefix("https://api.openai.com")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count=%d want 2", count)
	}
	if len(owners) != 2 {
		t.Fatalf("owners len=%d want 2: %+v", len(owners), owners)
	}
	// All returned rows should have BaseURL starting with the prefix; owner_id must be 1.
	for _, p := range owners {
		if p.OwnerID != 1 {
			t.Fatalf("unexpected owner_id=%d in %+v", p.OwnerID, owners)
		}
		if p.ChannelName != "a" && p.ChannelName != "b" {
			t.Fatalf("unexpected channel_name=%q in %+v", p.ChannelName, owners)
		}
	}
}

func TestPrivateChannel_CountByBaseURLPrefix_NoMatchReturnsZero(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v1/"},
		OwnerID:     1, Name: "a", Status: 1,
	})

	q := NewAdminQuery(NewContext(a))
	count, owners, err := q.PrivateChannel().CountByBaseURLPrefix("https://other.com")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count=%d want 0", count)
	}
	if len(owners) != 0 {
		t.Fatalf("owners must be empty; got %+v", owners)
	}
}

// --- ListOwnedBy / ListAcrossOwners search semantics (Task 25) ---
//
// Pre-Task-25 behavior used `name LIKE ? OR models LIKE ?` for the Search filter,
// which:
//   - silently matched against the JSON-encoded `models` text blob (non-indexed
//     full-table scan; surprising hits like search "gpt" returning every channel
//     that lists "gpt-4o" in its models array),
//   - and conflated "search by name" with "filter by model".
//
// These tests pin the new contract:
//   - Search matches Name only (substring, case-insensitive per SQLite default).
//   - ModelName is a separate precise filter (matches JSON token `"<model>"`).

func TestPrivateChannel_ListOwnedBy_SearchMatchesNameOnly(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "openai-prod", Status: 1,
		Models: datatypes.JSONSlice[string]{"gpt-4"},
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "anthropic-prod", Status: 1,
		Models: datatypes.JSONSlice[string]{"claude-opus"},
	})

	q := NewAdminQuery(NewContext(a))

	// "openai" matches the name "openai-prod" only.
	rows, total, err := q.PrivateChannel().ListOwnedBy(
		1, ListOptions{Page: 1, PageSize: 100},
		PrivateChannelFilter{Search: "openai"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Name != "openai-prod" {
		t.Fatalf("name search broken: total=%d rows=%+v", total, rows)
	}

	// "gpt" must NOT match: pre-Task-25 the models-LIKE branch would have hit
	// the "gpt-4" entry in openai-prod.Models. Now Search is name-only.
	rows2, total2, err := q.PrivateChannel().ListOwnedBy(
		1, ListOptions{Page: 1, PageSize: 100},
		PrivateChannelFilter{Search: "gpt"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 0 || len(rows2) != 0 {
		t.Fatalf("search must match name only, not models blob: total=%d rows=%+v", total2, rows2)
	}
}

func TestPrivateChannel_ListOwnedBy_ModelNamePreciseFilter(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "a", Status: 1,
		Models: datatypes.JSONSlice[string]{"gpt-4"},
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "b", Status: 1,
		Models: datatypes.JSONSlice[string]{"claude-opus"},
	})
	// Substring-collision case: "gpt-4-turbo" must NOT match a ModelName filter
	// for "gpt-4" — the JSON-quoted boundary guards against the substring.
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "c", Status: 1,
		Models: datatypes.JSONSlice[string]{"gpt-4-turbo"},
	})

	q := NewAdminQuery(NewContext(a))
	rows, total, err := q.PrivateChannel().ListOwnedBy(
		1, ListOptions{Page: 1, PageSize: 100},
		PrivateChannelFilter{ModelName: "gpt-4"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Name != "a" {
		t.Fatalf("model filter broken: total=%d rows=%+v", total, rows)
	}
}

func TestPrivateChannel_ListOwnedBy_NoFilterReturnsAll(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "a", Status: 1,
		Models: datatypes.JSONSlice[string]{"gpt-4"},
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "b", Status: 1,
		Models: datatypes.JSONSlice[string]{"claude-opus"},
	})

	q := NewAdminQuery(NewContext(a))
	rows, total, err := q.PrivateChannel().ListOwnedBy(
		1, ListOptions{Page: 1, PageSize: 100},
		PrivateChannelFilter{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("no filter must return all: total=%d rows=%+v", total, rows)
	}
}

func TestPrivateChannel_ListAcrossOwners_SearchAndModelNameFilter(t *testing.T) {
	a, db := setupTestApp(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1, Name: "openai-prod", Status: 1,
		Models: datatypes.JSONSlice[string]{"gpt-4"},
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     2, Name: "anthropic-prod", Status: 1,
		Models: datatypes.JSONSlice[string]{"claude-opus"},
	})

	q := NewAdminQuery(NewContext(a))

	// Search "openai" hits name across owners; "gpt" no longer hits via models.
	rows, total, err := q.PrivateChannel().ListAcrossOwners(
		ListOptions{Page: 1, PageSize: 100},
		PrivateChannelFilter{Search: "gpt"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(rows) != 0 {
		t.Fatalf("admin search must match name only: total=%d rows=%+v", total, rows)
	}

	// ModelName "gpt-4" precisely matches the owner=1 channel.
	rows2, total2, err := q.PrivateChannel().ListAcrossOwners(
		ListOptions{Page: 1, PageSize: 100},
		PrivateChannelFilter{ModelName: "gpt-4"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 1 || len(rows2) != 1 || rows2[0].OwnerID != 1 {
		t.Fatalf("admin model filter broken: total=%d rows=%+v", total2, rows2)
	}
}

func TestPrivateChannel_CountByBaseURLPrefix_OwnersListCappedAt50(t *testing.T) {
	a, db := setupTestApp(t)
	for i := 0; i < 60; i++ {
		db.Create(&models.PrivateChannel{
			ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v1/"},
			OwnerID:     uint(i + 1),
			Name:        "c" + strconv.Itoa(i),
			Status:      1,
		})
	}

	q := NewAdminQuery(NewContext(a))
	count, owners, err := q.PrivateChannel().CountByBaseURLPrefix("https://api.openai.com")
	if err != nil {
		t.Fatal(err)
	}
	if count != 60 {
		t.Fatalf("count=%d want 60 (total count must not be capped)", count)
	}
	if len(owners) != 50 {
		t.Fatalf("owners len=%d want 50 (preview list capped at 50)", len(owners))
	}
}
