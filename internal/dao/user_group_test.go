package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

func TestUserGroup_CreateGetList(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	m := NewAdminMutation(ctx)

	g := &models.UserGroup{
		Name:              "team-a",
		Description:       "Team A",
		Status:            1,
		AllowedChannelIDs: datatypes.JSONSlice[uint]{1, 2, 3},
		Models:            `["gpt-4o"]`,
	}
	if err := m.UserGroup().Create(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.ID == 0 {
		t.Fatalf("Create did not assign ID")
	}

	got, err := q.UserGroup().GetByID(g.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "team-a" || len(got.AllowedChannelIDs) != 3 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}

	got2, err := q.UserGroup().GetByName("team-a")
	if err != nil || got2.ID != g.ID {
		t.Fatalf("GetByName failed: %v / %+v", err, got2)
	}

	list, total, err := q.UserGroup().List(ListOptions{Page: 1, PageSize: 10}, UserGroupListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total < 1 {
		t.Fatalf("expected >=1 group, got total=%d list=%d", total, len(list))
	}
}

func TestUserGroup_Update(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	m := NewAdminMutation(ctx)

	g := &models.UserGroup{Name: "team-b", Status: 1}
	if err := m.UserGroup().Create(g); err != nil {
		t.Fatal(err)
	}
	if err := m.UserGroup().Update(g.ID, map[string]any{"description": "updated"}); err != nil {
		t.Fatal(err)
	}
	got, _ := q.UserGroup().GetByID(g.ID)
	if got.Description != "updated" {
		t.Fatalf("description = %q", got.Description)
	}
}

func TestUserGroup_NameUnique(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	if err := m.UserGroup().Create(&models.UserGroup{Name: "dup", Status: 1}); err != nil {
		t.Fatal(err)
	}
	err := m.UserGroup().Create(&models.UserGroup{Name: "dup", Status: 1})
	if err == nil {
		t.Fatalf("expected unique-violation error, got nil")
	}
}

func TestUserGroup_ListSearchAndStatusFilter(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	m := NewAdminMutation(ctx)
	for _, name := range []string{"alpha-team", "beta-team", "gamma"} {
		if err := m.UserGroup().Create(&models.UserGroup{Name: name, Status: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.UserGroup().Create(&models.UserGroup{Name: "disabled-team", Status: 2}); err != nil {
		t.Fatal(err)
	}

	got, _, err := q.UserGroup().List(ListOptions{Page: 1, PageSize: 10}, UserGroupListFilter{Search: "team"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 {
		t.Fatalf("expected >=3 groups matching 'team', got %d", len(got))
	}

	enabled := 1
	got, _, _ = q.UserGroup().List(ListOptions{Page: 1, PageSize: 10}, UserGroupListFilter{Status: &enabled})
	for _, g := range got {
		if g.Status != 1 {
			t.Fatalf("status filter leaked %+v", g)
		}
	}
}

func TestUserGroup_CountUsers(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	m := NewAdminMutation(ctx)

	g := &models.UserGroup{Name: "team-c", Status: 1}
	if err := m.UserGroup().Create(g); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		u := &models.User{Username: pseudoUsername(t, i), Password: "x", GroupID: g.ID}
		if err := m.User().Create(u); err != nil {
			t.Fatal(err)
		}
	}
	n, err := q.UserGroup().CountUsers(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("CountUsers = %d, want 3", n)
	}
}

func TestUserGroup_DeleteAndReassign(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	m := NewAdminMutation(ctx)

	// Ensure default group exists at id=1
	def := &models.UserGroup{Name: "default", Status: 1}
	if err := m.UserGroup().Create(def); err != nil {
		t.Fatal(err)
	}
	if def.ID != 1 {
		t.Fatalf("expected default ID=1, got %d", def.ID)
	}

	g := &models.UserGroup{Name: "to-delete", Status: 1}
	if err := m.UserGroup().Create(g); err != nil {
		t.Fatal(err)
	}

	var memberIDs []uint
	for i := 0; i < 3; i++ {
		u := &models.User{Username: pseudoUsername(t, i), Password: "x", GroupID: g.ID}
		if err := m.User().Create(u); err != nil {
			t.Fatal(err)
		}
		memberIDs = append(memberIDs, u.ID)
	}

	affected, err := m.UserGroup().DeleteAndReassign(g.ID)
	if err != nil {
		t.Fatalf("DeleteAndReassign: %v", err)
	}
	if len(affected) != 3 {
		t.Fatalf("affected = %v, want 3 ids", affected)
	}

	if _, err := q.UserGroup().GetByID(g.ID); err == nil {
		t.Fatalf("group still exists after delete")
	}
	for _, uid := range memberIDs {
		u, err := q.User().GetByID(uid)
		if err != nil {
			t.Fatal(err)
		}
		if u.GroupID != 1 {
			t.Fatalf("user %d not reassigned to default, got group_id=%d", uid, u.GroupID)
		}
	}
}

func TestUserGroup_DeleteDefault_Rejected(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	if _, err := m.UserGroup().DeleteAndReassign(1); err == nil {
		t.Fatalf("expected error deleting default group, got nil")
	}
}

func pseudoUsername(t *testing.T, i int) string {
	return t.Name() + "-u" + string(rune('0'+i))
}
