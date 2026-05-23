package sync

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
