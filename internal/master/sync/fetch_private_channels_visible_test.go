package sync

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// newTestCipher 用随机 32 字节 KEK 构造一个 Cipher，供各测试独立使用。
func newTestCipher(t *testing.T) *byokcrypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	c, err := byokcrypto.NewFromConfig(base64.StdEncoding.EncodeToString(key), "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// setupPrivChanDB 创建内存 SQLite + AutoMigrate，返回 dao.AdminQuery。
func setupPrivChanDB(t *testing.T) (*dbApp, dao.AdminQuery) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	a := &dbApp{db: db}
	q := dao.NewAdminQuery(dao.NewContext(a))
	return a, q
}

func TestPrivateChannelsVisibleFetch_OwnerOnly(t *testing.T) {
	cipher := newTestCipher(t)
	a, q := setupPrivChanDB(t)
	db := a.db

	db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"})
	ct, err := cipher.Seal("sk-real-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "chan-a", Status: 1, KeyCipher: ct, KeyLast4: "-key", Models: datatypes.JSONSlice[string]{"gpt-4o"}})

	h := &privateChannelsVisibleFetchHandler{cipher: cipher}
	data, side, found, err := h.Fetch(context.Background(), q, "1")
	if err != nil {
		t.Fatalf("fetch err=%v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if side != nil {
		t.Fatal("side should be nil for private channel fetch")
	}

	var set protocol.VisiblePrivateChannelSet
	if err := json.Unmarshal(data, &set); err != nil {
		t.Fatal(err)
	}
	if set.UserID != 1 {
		t.Fatalf("UserID = %d, want 1", set.UserID)
	}
	if len(set.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(set.Channels))
	}
	if set.Channels[0].KeyPlaintext != "sk-real-key" {
		t.Fatalf("plaintext not injected: got %q", set.Channels[0].KeyPlaintext)
	}
}

func TestPrivateChannelsVisibleFetch_BadUserID(t *testing.T) {
	cipher := newTestCipher(t)
	_, q := setupPrivChanDB(t)

	h := &privateChannelsVisibleFetchHandler{cipher: cipher}
	_, _, found, err := h.Fetch(context.Background(), q, "not-a-number")
	if err != nil {
		t.Fatalf("expected no error for bad key, got %v", err)
	}
	if found {
		t.Fatal("expected not-found for unparseable key")
	}
}

func TestPrivateChannelsVisibleFetch_NoUser(t *testing.T) {
	cipher := newTestCipher(t)
	_, q := setupPrivChanDB(t)

	h := &privateChannelsVisibleFetchHandler{cipher: cipher}
	_, _, found, _ := h.Fetch(context.Background(), q, "999")
	if found {
		t.Fatal("expected not-found for missing user")
	}
}

func TestPrivateChannelProjectionCarriesAutoBanRuntimeState(t *testing.T) {
	cipher := newTestCipher(t)
	ciphertext, err := cipher.Seal("sk-private", 7)
	if err != nil {
		t.Fatal(err)
	}
	state := models.ChannelDisableState{Tripped: true, Reason: "consecutive_errors", TrippedAt: 123}
	pc := &models.PrivateChannel{
		ChannelCore: models.ChannelCore{
			ID: 9, AutoBanState: datatypes.NewJSONType(state), AutoBanRevision: 11,
		},
		OwnerID: 7, KeyCipher: ciphertext,
	}

	got, err := (&privateChannelsVisibleFetchHandler{cipher: cipher}).project(pc)
	if err != nil {
		t.Fatal(err)
	}
	if got.AutoBanState.Data() != state || got.AutoBanRevision != 11 {
		t.Fatalf("projection lost auto-ban runtime state: %+v", got.ChannelCore)
	}
}

func TestPrivateChannelsVisibleFetch_SkipUndecryptable(t *testing.T) {
	cipherB := newTestCipher(t)
	// cipherA uses a completely different KEK — cipherB cannot open its ciphertext
	cipherA := newTestCipher(t)

	a, q := setupPrivChanDB(t)
	db := a.db

	db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"})

	badCT, err := cipherA.Seal("dead-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "bad", Status: 1, KeyCipher: badCT, Models: datatypes.JSONSlice[string]{"gpt-4o"}})

	goodCT, err := cipherB.Seal("good-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "good", Status: 1, KeyCipher: goodCT, Models: datatypes.JSONSlice[string]{"gpt-4o"}})

	h := &privateChannelsVisibleFetchHandler{cipher: cipherB}
	data, _, found, err := h.Fetch(context.Background(), q, "1")
	if err != nil || !found {
		t.Fatalf("fetch err=%v found=%v", err, found)
	}

	var set protocol.VisiblePrivateChannelSet
	if err := json.Unmarshal(data, &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Channels) != 1 {
		t.Fatalf("expected 1 channel (undecryptable skipped), got %d", len(set.Channels))
	}
	if set.Channels[0].KeyPlaintext != "good-key" {
		t.Fatalf("undecryptable not skipped: got %q", set.Channels[0].KeyPlaintext)
	}
}

func TestPrivateChannelsVisibleFetch_NilCipher(t *testing.T) {
	a, _ := setupPrivChanDB(t)
	db := a.db
	db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"})

	// Build q fresh so the handler is the only thing that differs
	q := dao.NewAdminQuery(dao.NewContext(a))

	h := &privateChannelsVisibleFetchHandler{cipher: nil}
	_, _, found, err := h.Fetch(context.Background(), q, "1")
	if err == nil {
		t.Fatal("expected error when cipher is nil")
	}
	if found {
		t.Fatal("expected not-found on cipher error")
	}
}

// TestPrivateChannelsVisibleFetch_EmptyReturnsNotFound 行为修复：用户存在但无可见
// 私有通道时返回 found=false，使 agent 走 NegativeTTL 自愈，而非永久缓存空集。
func TestPrivateChannelsVisibleFetch_EmptyReturnsNotFound(t *testing.T) {
	cipher := newTestCipher(t)
	a, q := setupPrivChanDB(t)
	a.db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"})
	// 注意：不创建任何 PrivateChannel。

	h := &privateChannelsVisibleFetchHandler{cipher: cipher}
	_, _, found, err := h.Fetch(context.Background(), q, "1")
	if err != nil {
		t.Fatalf("fetch err=%v", err)
	}
	if found {
		t.Fatal("expected found=false for user with zero visible channels")
	}
}

// TestPrivateChannelsVisibleFetch_AllUndecryptableReturnsNotFound: 通道存在但全部解密失败
// → 投影后 set.Channels 为空 → found=false（走 NegativeTTL 自愈），与零通道场景一致。
func TestPrivateChannelsVisibleFetch_AllUndecryptableReturnsNotFound(t *testing.T) {
	cipherB := newTestCipher(t)
	cipherA := newTestCipher(t) // 不同 KEK，cipherB 无法解开 cipherA 的密文

	a, q := setupPrivChanDB(t)
	a.db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"})

	ct1, err := cipherA.Seal("dead-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	ct2, err := cipherA.Seal("dead-2", 1)
	if err != nil {
		t.Fatal(err)
	}
	a.db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "bad1", Status: 1, KeyCipher: ct1, Models: datatypes.JSONSlice[string]{"gpt-4o"}})
	a.db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "bad2", Status: 1, KeyCipher: ct2, Models: datatypes.JSONSlice[string]{"gpt-4o"}})

	h := &privateChannelsVisibleFetchHandler{cipher: cipherB}
	_, _, found, err := h.Fetch(context.Background(), q, "1")
	if err != nil {
		t.Fatalf("fetch err=%v", err)
	}
	if found {
		t.Fatal("expected found=false when all channels are undecryptable")
	}
}

// TestPrivateChannelAuthSync 验证持久化配置通过主节点同步仍保留原文。
func TestPrivateChannelAuthSync(t *testing.T) {
	// 1. 保存包含认证配置的私有渠道。
	cipher := newTestCipher(t)
	a, q := setupPrivChanDB(t)
	if err := a.db.Create(&models.User{ID: 1, GroupID: 1, Username: "auth-user"}).Error; err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal("sk-secret", 1)
	if err != nil {
		t.Fatal(err)
	}
	auth := models.ChannelAuth{Default: &models.AuthType{Header: "Authorization", Value: "Bearer ${CHANNEL_KEY}"}, Protocols: map[string]models.AuthType{"messages": {Header: "X-Token", Value: "${CHANNEL_KEY}"}}}
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Auth: datatypes.NewJSONType(auth)}, OwnerID: 1, Name: "auth-channel", Status: 1, KeyCipher: sealed, Models: datatypes.JSONSlice[string]{"gpt-4o"}}
	if err := a.db.Create(pc).Error; err != nil {
		t.Fatal(err)
	}
	// 2. Fetch 的 JSON 载荷保留整体和协议认证。
	h := &privateChannelsVisibleFetchHandler{cipher: cipher}
	data, _, found, err := h.Fetch(context.Background(), q, "1")
	if err != nil || !found {
		t.Fatalf("fetch: found=%v err=%v", found, err)
	}
	var set protocol.VisiblePrivateChannelSet
	if err := json.Unmarshal(data, &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Channels) != 1 || !reflect.DeepEqual(auth, set.Channels[0].Auth.Data()) {
		t.Fatal("auth lost in sync")
	}
	// 3. 转换结果中的指针和 map 不修改数据库对象。
	synced, err := h.project(pc)
	if err != nil {
		t.Fatal(err)
	}
	synced.Auth.Data().Default.Value = "changed"
	delete(synced.Auth.Data().Protocols, "messages")
	if !reflect.DeepEqual(pc.Auth.Data(), auth) {
		t.Fatal("auth shared across sync projection")
	}
}
