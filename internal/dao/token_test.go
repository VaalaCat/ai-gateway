package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func TestTokenDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Token()
	m := NewAdminMutation(ctx).Token()

	// seed users first
	u1 := &models.User{Username: "user1"}
	u2 := &models.User{Username: "user2"}
	db.Create(u1)
	db.Create(u2)

	tk1 := &models.Token{UserID: u1.ID, Key: "sk-key1", Name: "Token One", Status: 1}
	tk2 := &models.Token{UserID: u1.ID, Key: "sk-key2", Name: "Token Two", Status: 1}
	tk3 := &models.Token{UserID: u2.ID, Key: "sk-key3", Name: "Token Three", Status: 1}
	for _, tk := range []*models.Token{tk1, tk2, tk3} {
		if err := db.Create(tk).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("GetByID", func(t *testing.T) {
		tk, err := q.GetByID(tk1.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if tk.Key != "sk-key1" {
			t.Fatalf("expected sk-key1, got %s", tk.Key)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("GetByKey", func(t *testing.T) {
		tk, err := q.GetByKey("sk-key2")
		if err != nil {
			t.Fatalf("GetByKey: %v", err)
		}
		if tk.Name != "Token Two" {
			t.Fatalf("expected Token Two, got %s", tk.Name)
		}
	})

	t.Run("List all", func(t *testing.T) {
		tokens, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, TokenListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		_ = tokens
	})

	t.Run("List with search", func(t *testing.T) {
		tokens, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, TokenListFilter{Search: "key1"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		_ = tokens
	})

	t.Run("List with UserID filter", func(t *testing.T) {
		uid := u1.ID
		tokens, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, TokenListFilter{UserID: &uid})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		_ = tokens
	})

	t.Run("Create", func(t *testing.T) {
		tk := &models.Token{UserID: u1.ID, Key: "sk-new", Name: "New"}
		if err := m.Create(tk); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if tk.ID == 0 {
			t.Fatal("expected ID set")
		}
	})

	t.Run("Update", func(t *testing.T) {
		if err := m.Update(tk1.ID, map[string]any{"name": "Updated"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		tk, _ := q.GetByID(tk1.ID)
		if tk.Name != "Updated" {
			t.Fatalf("expected Updated, got %s", tk.Name)
		}
	})

	t.Run("DisableAllForUser", func(t *testing.T) {
		if err := m.DisableAllForUser(u1.ID); err != nil {
			t.Fatalf("DisableAllForUser: %v", err)
		}
		st := 1
		uid := u1.ID
		tokens, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, TokenListFilter{UserID: &uid, Status: &st})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 0 {
			t.Fatalf("expected 0 active tokens for user1, got %d", total)
		}
		_ = tokens
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(tk3.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.GetByID(tk3.ID)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})
}

func TestTokenDAO_BulkSyncFromTemplate(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Token()
	m := NewAdminMutation(ctx).Token()

	u := &models.User{Username: "u1"}
	if err := db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	tplID := uint(7)
	otherTpl := uint(8)

	// alreadyInSync：models + channels 都和模版一致（顺序不同也算一致）
	alreadyInSync := &models.Token{UserID: u.ID, Key: "sk-1", Name: "in-sync", Status: 1,
		TemplateID: &tplID, Models: `["gpt-5","gpt-4"]`}
	alreadyInSync.AllowedChannelIDs = []uint{2, 1}

	// modelsDiff：models 不一致
	modelsDiff := &models.Token{UserID: u.ID, Key: "sk-2", Name: "models-diff", Status: 1,
		TemplateID: &tplID, Models: `["gpt-4"]`}
	modelsDiff.AllowedChannelIDs = []uint{1, 2}

	// channelsDiff：channels 不一致
	channelsDiff := &models.Token{UserID: u.ID, Key: "sk-3", Name: "channels-diff", Status: 1,
		TemplateID: &tplID, Models: `["gpt-4","gpt-5"]`}
	channelsDiff.AllowedChannelIDs = []uint{1}

	// otherTpl token：另一个模版下的 token，不应被改
	other := &models.Token{UserID: u.ID, Key: "sk-4", Name: "other", Status: 1,
		TemplateID: &otherTpl, Models: `["gpt-3"]`}
	other.AllowedChannelIDs = []uint{9}

	for _, tk := range []*models.Token{alreadyInSync, modelsDiff, channelsDiff, other} {
		if err := db.Create(tk).Error; err != nil {
			t.Fatal(err)
		}
	}

	tplModels := `["gpt-4","gpt-5"]`
	tplChannels := []uint{1, 2}

	t.Run("returns only changed IDs", func(t *testing.T) {
		changedIDs, total, err := m.BulkSyncFromTemplate(tplID, tplModels, tplChannels)
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 {
			t.Fatalf("total = %d, want 3", total)
		}
		want := map[uint]bool{modelsDiff.ID: true, channelsDiff.ID: true}
		if len(changedIDs) != 2 {
			t.Fatalf("got %d changedIDs, want 2 (%v)", len(changedIDs), changedIDs)
		}
		for _, id := range changedIDs {
			if !want[id] {
				t.Fatalf("unexpected id %d in changed; want only %v", id, want)
			}
		}

		// verify modelsDiff was overwritten
		got, _ := q.GetByID(modelsDiff.ID)
		if got.Models != tplModels {
			t.Fatalf("modelsDiff.Models = %q, want %q", got.Models, tplModels)
		}
		if !sameSetUint([]uint(got.AllowedChannelIDs), tplChannels) {
			t.Fatalf("modelsDiff.AllowedChannelIDs = %v, want %v", got.AllowedChannelIDs, tplChannels)
		}

		// alreadyInSync untouched
		stillInSync, _ := q.GetByID(alreadyInSync.ID)
		if stillInSync.Models != `["gpt-5","gpt-4"]` {
			t.Fatalf("alreadyInSync was unexpectedly modified: %q", stillInSync.Models)
		}

		// other template untouched
		stillOther, _ := q.GetByID(other.ID)
		if stillOther.Models != `["gpt-3"]` {
			t.Fatalf("other template was modified: %q", stillOther.Models)
		}
	})

	t.Run("second call is a no-op (idempotent)", func(t *testing.T) {
		changedIDs, total, err := m.BulkSyncFromTemplate(tplID, tplModels, tplChannels)
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 {
			t.Fatalf("total = %d, want 3", total)
		}
		if len(changedIDs) != 0 {
			t.Fatalf("expected 0 changed, got %d (%v)", len(changedIDs), changedIDs)
		}
	})

	t.Run("template with no tokens returns empty", func(t *testing.T) {
		changedIDs, total, err := m.BulkSyncFromTemplate(9999, tplModels, tplChannels)
		if err != nil {
			t.Fatal(err)
		}
		if total != 0 {
			t.Fatalf("total = %d, want 0", total)
		}
		if len(changedIDs) != 0 {
			t.Fatalf("expected 0, got %v", changedIDs)
		}
	})
}

// helper：忽略顺序比较两组 uint
func sameSetUint(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[uint]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := set[x]; !ok {
			return false
		}
	}
	return true
}

func TestTokenDAO_ListByTemplateID_And_ListByIDs(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Token()

	u := &models.User{Username: "u1"}
	if err := db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	tplID := uint(42)
	otherTpl := uint(43)
	t1 := &models.Token{UserID: u.ID, Key: "sk-a", Name: "a", Status: 1, TemplateID: &tplID}
	t2 := &models.Token{UserID: u.ID, Key: "sk-b", Name: "b", Status: 1, TemplateID: &tplID}
	t3 := &models.Token{UserID: u.ID, Key: "sk-c", Name: "c", Status: 1, TemplateID: &otherTpl}
	t4 := &models.Token{UserID: u.ID, Key: "sk-d", Name: "d", Status: 1} // no template
	for _, tk := range []*models.Token{t1, t2, t3, t4} {
		if err := db.Create(tk).Error; err != nil {
			t.Fatal(err)
		}
	}

	t.Run("ListByTemplateID returns only that template's tokens", func(t *testing.T) {
		got, err := q.ListByTemplateID(tplID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
		gotKeys := map[string]bool{got[0].Key: true, got[1].Key: true}
		if !gotKeys["sk-a"] || !gotKeys["sk-b"] {
			t.Fatalf("got keys %v", gotKeys)
		}
	})

	t.Run("ListByTemplateID empty", func(t *testing.T) {
		got, err := q.ListByTemplateID(9999)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d, want 0", len(got))
		}
	})

	t.Run("ListByIDs returns matching tokens", func(t *testing.T) {
		got, err := q.ListByIDs([]uint{t1.ID, t3.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
	})

	t.Run("ListByIDs empty input", func(t *testing.T) {
		got, err := q.ListByIDs(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d, want 0", len(got))
		}
	})
}
