package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestTokenStore_SetGetByKeyAndID(t *testing.T) {
	primary := entitycache.NewFullCache[string, *models.Token]()
	ts := newTokenStore(primary, nil)

	tok := &models.Token{ID: 7, Key: "sk-a", UserID: 1, Status: 1}
	ts.Set(tok)

	got, ok, err := ts.Get(context.Background(), "sk-a")
	if !ok || err != nil || got.ID != 7 {
		t.Fatalf("Get by key: %+v ok=%v err=%v", got, ok, err)
	}
	got, ok, err = ts.GetByID(context.Background(), 7)
	if !ok || err != nil || got.Key != "sk-a" {
		t.Fatalf("Get by id: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestTokenStore_DeleteRemovesBothViews(t *testing.T) {
	primary := entitycache.NewFullCache[string, *models.Token]()
	ts := newTokenStore(primary, nil)

	tok := &models.Token{ID: 5, Key: "sk-b", UserID: 1, Status: 1}
	ts.Set(tok)
	ts.Delete("sk-b")

	if _, ok := primary.Peek("sk-b"); ok {
		t.Fatal("primary should be empty after Delete")
	}
	if _, ok, _ := ts.GetByID(context.Background(), 5); ok {
		t.Fatal("byID should be empty after Delete")
	}
}

func TestTokenStore_DeleteByID(t *testing.T) {
	primary := entitycache.NewFullCache[string, *models.Token]()
	ts := newTokenStore(primary, nil)
	tok := &models.Token{ID: 9, Key: "sk-c", UserID: 1, Status: 1}
	ts.Set(tok)
	ts.DeleteByID(9)
	if _, ok, _ := ts.Get(context.Background(), "sk-c"); ok {
		t.Fatal("Get by key should miss after DeleteByID")
	}
}

func TestTokenStore_GetByKeyMissNoFetcher(t *testing.T) {
	primary := entitycache.NewFullCache[string, *models.Token]()
	ts := newTokenStore(primary, nil)
	_, ok, err := ts.Get(context.Background(), "absent")
	if ok {
		t.Fatal("expect miss")
	}
	if err != nil && !errors.Is(err, entitycache.ErrNotFound) {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestTokenStore_LRUEvictAlsoClearsByID(t *testing.T) {
	ts := newTokenStoreWithLRU(t, 2)

	t1 := &models.Token{ID: 1, Key: "sk-1", UserID: 1, Status: 1}
	t2 := &models.Token{ID: 2, Key: "sk-2", UserID: 1, Status: 1}
	t3 := &models.Token{ID: 3, Key: "sk-3", UserID: 1, Status: 1}
	ts.Set(t1)
	ts.Set(t2)
	ts.Set(t3) // 触发 LRU 淘汰最旧的 t1

	if _, ok := ts.primary.Peek("sk-1"); ok {
		t.Fatal("sk-1 should be evicted from primary")
	}
	if _, ok := ts.byID.Load(1); ok {
		t.Fatal("byID for id=1 should be cleared by EvictCallback")
	}
}

func TestTokenStore_ApplyDelete_RemovesBothViews(t *testing.T) {
	primary := entitycache.NewFullCache[string, *models.Token]()
	ts := newTokenStore(primary, nil)
	tok := &models.Token{ID: 9, Key: "sk-x", UserID: 1, Status: 1}
	ts.Set(tok)

	ts.Apply(entitycache.ActionDelete, tok)
	if _, ok := primary.Peek("sk-x"); ok {
		t.Fatal("primary should be empty after Apply Delete")
	}
	if _, ok := ts.byID.Load(9); ok {
		t.Fatal("byID should be empty after Apply Delete")
	}
}

func TestTokenStore_ApplySet_LRUApplyIfPresent(t *testing.T) {
	ts := newTokenStoreWithLRU(t, 4)
	tok := &models.Token{ID: 1, Key: "sk-a", UserID: 1, Status: 1}
	ts.Apply(entitycache.ActionSet, tok)
	if _, ok := ts.primary.Peek("sk-a"); ok {
		t.Fatal("Apply(Set) on absent LRU key should NOT warm")
	}
	ts.Set(tok)
	upd := &models.Token{ID: 1, Key: "sk-a", UserID: 1, Status: 0}
	ts.Apply(entitycache.ActionSet, upd)
	got, _ := ts.primary.Peek("sk-a")
	if got == nil || got.Status != 0 {
		t.Fatalf("Apply(Set) should overwrite, got %+v", got)
	}
}

// newTokenStoreWithLRU 构造一个 LRU primary + byID OnEvict 联动的 tokenStore，便于测试。
func newTokenStoreWithLRU(t *testing.T, cap int) *tokenStore {
	t.Helper()
	ts := &tokenStore{}
	primary, err := entitycache.NewLRUCache[string, *models.Token](entitycache.Config[string, *models.Token]{
		Capacity: cap,
		OnEvict: func(_ string, tok *models.Token) {
			if tok != nil {
				ts.byID.Delete(tok.ID)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts.primary = primary
	return ts
}
