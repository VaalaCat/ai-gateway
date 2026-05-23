package sync

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// dbApp 是测试专用的最小 AppProvider，满足 dao.AppProvider 接口。
type dbApp struct{ db *gorm.DB }

func (a *dbApp) GetDB() *gorm.DB { return a.db }

type fakeHandler struct {
	called bool
	found  bool
	data   json.RawMessage
}

func (h *fakeHandler) Fetch(_ context.Context, _ dao.AdminQuery, _ string) (json.RawMessage, json.RawMessage, bool, error) {
	h.called = true
	return h.data, nil, h.found, nil
}

func TestFetchRegistry_RegisterAndResolve(t *testing.T) {
	r := &FetchRegistry{handlers: map[string]FetchHandler{}}
	h := &fakeHandler{found: true, data: []byte(`{}`)}
	r.Register("test", h)

	got, ok := r.Resolve("test")
	if !ok || got != h {
		t.Fatalf("Resolve: got (%v, %v)", got, ok)
	}

	if _, ok := r.Resolve("unknown"); ok {
		t.Fatal("unknown entity must resolve as not found")
	}
}

func TestNewFetchRegistry_RegistersTokenAndUser(t *testing.T) {
	r := NewFetchRegistry(nil)
	for _, e := range []string{"token", "user", "private_channel"} {
		if _, ok := r.Resolve(e); !ok {
			t.Fatalf("entity %q should be registered by default", e)
		}
	}
}

// setupSyncDB 创建内存 SQLite DB，完成 AutoMigrate，返回 (AdminQuery, AdminMutation)。
// 不依赖 master 包，避免 import cycle。
func setupSyncDB(t *testing.T) (dao.AdminQuery, dao.AdminMutation) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	app := &dbApp{db: db}
	ctx := dao.NewContext(app)
	return dao.NewAdminQuery(ctx), dao.NewAdminMutation(ctx)
}

func TestTokenFetchHandler_Found_IncludesSyncedUser(t *testing.T) {
	q, m := setupSyncDB(t)

	if err := m.User().Create(&models.User{
		Username: "alice", Password: "x", GroupID: 7, Status: consts.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	user, _ := q.User().GetByUsername("alice")
	tok := &models.Token{Key: "sk-fetch-test", UserID: user.ID, Status: consts.StatusEnabled, Name: "t1"}
	if err := m.Token().Create(tok); err != nil {
		t.Fatal(err)
	}

	data, side, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, "sk-fetch-test")
	if err != nil {
		t.Fatalf("Fetch err: %v", err)
	}
	if !found {
		t.Fatal("expected Found=true")
	}

	var got models.Token
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal token: %v", err)
	}
	if got.Key != "sk-fetch-test" {
		t.Fatalf("token Key mismatch: %q", got.Key)
	}

	if len(side) == 0 {
		t.Fatal("expected Side payload with SyncedUser")
	}
	var su protocol.SyncedUser
	if err := json.Unmarshal(side, &su); err != nil {
		t.Fatalf("unmarshal side: %v", err)
	}
	if su.ID != user.ID || su.GroupID != 7 {
		t.Fatalf("SyncedUser mismatch: %+v", su)
	}
}

func TestTokenFetchHandler_NotFound(t *testing.T) {
	q, _ := setupSyncDB(t)
	data, side, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, "sk-unknown")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if found || data != nil || side != nil {
		t.Fatalf("expected not-found empty, got found=%v data=%s side=%s", found, data, side)
	}
}

func TestTokenFetchHandler_GroupIDZeroNormalizedToDefault(t *testing.T) {
	q, m := setupSyncDB(t)
	if err := m.User().Create(&models.User{
		Username: "carol", Password: "x", GroupID: 0, Status: consts.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	user, _ := q.User().GetByUsername("carol")
	if err := m.Token().Create(&models.Token{
		Key: "sk-zero-group", UserID: user.ID, Status: consts.StatusEnabled, Name: "t-zero",
	}); err != nil {
		t.Fatal(err)
	}

	_, side, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, "sk-zero-group")
	if err != nil || !found {
		t.Fatalf("Fetch err=%v found=%v", err, found)
	}
	var su protocol.SyncedUser
	if err := json.Unmarshal(side, &su); err != nil {
		t.Fatal(err)
	}
	if su.GroupID != 1 {
		t.Fatalf("GroupID=0 should normalize to 1 (default group), got %d", su.GroupID)
	}
}

func TestUserFetchHandler_FoundReturnsSyncedUser(t *testing.T) {
	q, m := setupSyncDB(t)
	if err := m.User().Create(&models.User{
		Username: "bob", Password: "x", GroupID: 9, Status: consts.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	user, _ := q.User().GetByUsername("bob")

	data, side, found, err := userFetchHandler{}.Fetch(context.Background(), q,
		strconv.FormatUint(uint64(user.ID), 10))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !found {
		t.Fatal("expected Found=true")
	}
	if len(side) != 0 {
		t.Fatal("user fetch should not include Side payload")
	}

	var su protocol.SyncedUser
	if err := json.Unmarshal(data, &su); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if su.ID != user.ID || su.GroupID != 9 {
		t.Fatalf("SyncedUser mismatch: %+v", su)
	}
}

func TestUserFetchHandler_BadKey(t *testing.T) {
	q, _ := setupSyncDB(t)
	_, _, found, err := userFetchHandler{}.Fetch(context.Background(), q, "not-a-number")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if found {
		t.Fatal("invalid key should be Found=false")
	}
}

func TestUserFetchHandler_NotFound(t *testing.T) {
	q, _ := setupSyncDB(t)
	_, _, found, _ := userFetchHandler{}.Fetch(context.Background(), q, "999999")
	if found {
		t.Fatal("unknown user id should be Found=false")
	}
}

func TestUserFetchHandler_GroupIDZeroNormalizes(t *testing.T) {
	q, m := setupSyncDB(t)
	if err := m.User().Create(&models.User{
		Username: "dave", Password: "x", GroupID: 0, Status: consts.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	user, _ := q.User().GetByUsername("dave")
	data, _, found, err := userFetchHandler{}.Fetch(context.Background(), q,
		strconv.FormatUint(uint64(user.ID), 10))
	if err != nil || !found {
		t.Fatalf("Fetch err=%v found=%v", err, found)
	}
	var su protocol.SyncedUser
	if err := json.Unmarshal(data, &su); err != nil {
		t.Fatal(err)
	}
	if su.GroupID != 1 {
		t.Fatalf("GroupID=0 should normalize to 1, got %d", su.GroupID)
	}
}
