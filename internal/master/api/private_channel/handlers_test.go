package private_channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/glebarez/sqlite"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// assertAPIStatus type-asserts err as *api.APIError and verifies its HTTP status.
// Returns the *APIError for further inspection.
func assertAPIStatus(t *testing.T, err error, want int) *api.APIError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with status %d, got nil", want)
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != want {
		t.Fatalf("status = %d, want %d (msg=%q)", apiErr.Status, want, apiErr.Error())
	}
	return apiErr
}

// newHandlerTestCtx builds a Handler + Context + DB seeded with default group + model_config.
func newHandlerTestCtx(t *testing.T) (*Handler, *app.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed default group: %v", err)
	}
	db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o"})

	cipher, err := byokcrypto.NewFromConfig("", "test-jwt-secret-32-bytes-or-longer!!")
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())

	h := NewHandler(application, byokcrypto.NewStaticProvider(cipher))
	ctx := &app.Context{
		App:      application,
		UserInfo: &app.UserInfo{UserID: 1, GroupID: 1},
	}
	return h, ctx, db
}

func TestCreate_HappyPath(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	req := CreateRequest{
		Name:    "my-key",
		Type:    1,
		Key:     "sk-abcdefg",
		BaseURL: "https://api.openai.com",
		Models:  []string{"gpt-4o"},
	}
	resp, err := h.Create(ctx, req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Value.KeyLast4 != "defg" {
		t.Fatalf("last4 wrong: %q", resp.Value.KeyLast4)
	}
	var pc models.PrivateChannel
	db.First(&pc, resp.Value.ID)
	if pc.OwnerID != 1 || len(pc.KeyCipher) == 0 {
		t.Fatalf("not persisted with owner+cipher: %+v", pc)
	}
}

func TestCreate_RejectsCrossOwnerInjection(t *testing.T) {
	// CreateRequest doesn't expose owner_id; OwnerID always = c.UserInfo.UserID.
	h, ctx, db := newHandlerTestCtx(t)
	req := CreateRequest{
		Name:    "x",
		Type:    1,
		Key:     "sk-x",
		BaseURL: "https://api.openai.com",
		Models:  []string{"gpt-4o"},
	}
	resp, err := h.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var pc models.PrivateChannel
	db.First(&pc, resp.Value.ID)
	if pc.OwnerID != 1 {
		t.Fatalf("owner_id should be 1, got %d", pc.OwnerID)
	}
}

func TestCreate_ValidatorRejects(t *testing.T) {
	h, ctx, _ := newHandlerTestCtx(t)
	req := CreateRequest{
		Name:    "x",
		Type:    1,
		Key:     "sk-x",
		BaseURL: "http://10.0.0.1", // not in allowlist
		Models:  []string{"gpt-4o"},
	}
	if _, err := h.Create(ctx, req); err == nil {
		t.Fatal("invalid base_url should reject")
	}
}

func TestPortalList_OnlyShowsOwnChannels(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "mine", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 2, Name: "theirs", Status: 1})

	resp, err := h.PortalList(ctx, ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || resp.Data[0].Name != "mine" {
		t.Fatalf("list bleed: total=%d data=%+v", resp.Total, resp.Data)
	}
}

func TestPortalGet_404OnOtherOwner(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 2, Name: "theirs", Status: 1}
	db.Create(pc)
	if _, err := h.PortalGet(ctx, api.IDPathRequest{ID: strconv.FormatUint(uint64(pc.ID), 10)}); err == nil {
		t.Fatal("cross-owner Get should 404")
	}
}

func TestPortalGet_404OnNonexistent(t *testing.T) {
	h, ctx, _ := newHandlerTestCtx(t)
	if _, err := h.PortalGet(ctx, api.IDPathRequest{ID: "99999"}); err == nil {
		t.Fatal("nonexistent Get should 404")
	}
}

func TestPortalGet_404OnBadID(t *testing.T) {
	h, ctx, _ := newHandlerTestCtx(t)
	if _, err := h.PortalGet(ctx, api.IDPathRequest{ID: "not-numeric"}); err == nil {
		t.Fatal("bad ID should 404")
	}
}

func TestPortalGet_OwnSucceeds(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "mine", Status: 1}
	db.Create(pc)
	resp, err := h.PortalGet(ctx, api.IDPathRequest{ID: strconv.FormatUint(uint64(pc.ID), 10)})
	if err != nil {
		t.Fatalf("own Get failed: %v", err)
	}
	if resp.Name != "mine" {
		t.Fatalf("wrong channel: %+v", resp)
	}
}

func TestPortalUpdate_OwnerCheck(t *testing.T) {
	h, _, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 99, Name: "x", Status: 1}
	db.Create(pc)
	// Build a ctx that represents user 1 (not 99)
	bobCtx := &app.Context{App: h.App, UserInfo: &app.UserInfo{UserID: 1, GroupID: 1}}
	_, err := h.PortalUpdate(bobCtx, UpdateRequest{
		ID:     strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{"name": "hacked"},
	})
	if err == nil {
		t.Fatal("cross-owner update should 404")
	}
}

func TestPortalUpdate_ReservedFieldsStripped(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1}
	db.Create(pc)
	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID: strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{
			"owner_id": uint(99), // must be stripped
			"name":     "renamed",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if got.OwnerID != 1 || got.Name != "renamed" {
		t.Fatalf("reserved key leaked: %+v", got)
	}
}

func TestPortalUpdate_BaseURLValidated(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1}
	db.Create(pc)
	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID:     strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{"base_url": "http://10.0.0.1/v1"},
	})
	if err == nil {
		t.Fatal("invalid base_url in patch should reject")
	}
}

func TestPortalDelete_CleansUp(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1}
	db.Create(pc)
	_, err := h.PortalDelete(ctx, api.IDPathRequest{ID: strconv.FormatUint(uint64(pc.ID), 10)})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&models.PrivateChannel{}).Where("id = ?", pc.ID).Count(&count)
	if count != 0 {
		t.Fatalf("not deleted, count=%d", count)
	}
}

func TestPortalDelete_OwnerCheck(t *testing.T) {
	h, _, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 99, Name: "x", Status: 1}
	db.Create(pc)
	bobCtx := &app.Context{App: h.App, UserInfo: &app.UserInfo{UserID: 1, GroupID: 1}}
	_, err := h.PortalDelete(bobCtx, api.IDPathRequest{ID: strconv.FormatUint(uint64(pc.ID), 10)})
	if err == nil {
		t.Fatal("cross-owner delete should 404")
	}
	var count int64
	db.Model(&models.PrivateChannel{}).Where("id = ?", pc.ID).Count(&count)
	if count != 1 {
		t.Fatalf("still present? count=%d", count)
	}
}

func TestPortalUpdateKey_ChangesCipherAndLast4(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	cipher := h.Provider.GetCipher()
	oldCT, _ := cipher.Seal("sk-OLD-1234", 1)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1, KeyCipher: oldCT, KeyLast4: "1234"}
	db.Create(pc)

	_, err := h.PortalUpdateKey(ctx, UpdateKeyRequest{
		ID:  strconv.FormatUint(uint64(pc.ID), 10),
		Key: "sk-NEW-abcd",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if bytes.Equal(got.KeyCipher, oldCT) {
		t.Fatal("cipher not changed")
	}
	if got.KeyLast4 != "abcd" {
		t.Fatalf("last4 not updated: %q", got.KeyLast4)
	}
	// Verify we can decrypt the new cipher with the original owner ID:
	plain, err := cipher.Open(got.KeyCipher, 1)
	if err != nil || plain != "sk-NEW-abcd" {
		t.Fatalf("new cipher mismatch: plain=%q err=%v", plain, err)
	}
}

func TestPortalUpdateKey_OwnerCheck(t *testing.T) {
	h, _, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 99, Name: "x", Status: 1, KeyCipher: []byte("x"), KeyLast4: "xxxx"}
	db.Create(pc)
	bobCtx := &app.Context{App: h.App, UserInfo: &app.UserInfo{UserID: 1, GroupID: 1}}
	_, err := h.PortalUpdateKey(bobCtx, UpdateKeyRequest{
		ID:  strconv.FormatUint(uint64(pc.ID), 10),
		Key: "sk-new",
	})
	if err == nil {
		t.Fatal("cross-owner key update should 404")
	}
}

func TestPickTestModelForPrivate_Fallbacks(t *testing.T) {
	if got := pickTestModelForPrivate(&models.PrivateChannel{ChannelCore: models.ChannelCore{TestModel: "claude"}}); got != "claude" {
		t.Fatalf("TestModel priority broken: %q", got)
	}
	if got := pickTestModelForPrivate(&models.PrivateChannel{Models: datatypes.JSONSlice[string]{"gpt-4o"}}); got != "gpt-4o" {
		t.Fatalf("Models fallback broken: %q", got)
	}
	if got := pickTestModelForPrivate(&models.PrivateChannel{}); got != "gpt-3.5-turbo" {
		t.Fatalf("default fallback broken: %q", got)
	}
}

func TestPortalAvailableModels(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	// newHandlerTestCtx already seeds "gpt-4o"
	db.Create(&models.ModelConfig{ModelName: "claude-3-5-sonnet"})
	resp, err := h.PortalAvailableModels(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Models) < 2 {
		t.Fatalf("want at least 2 models, got %+v", resp.Models)
	}
	// Sorted
	for i := 1; i < len(resp.Models); i++ {
		if resp.Models[i-1] > resp.Models[i] {
			t.Fatalf("not sorted: %+v", resp.Models)
		}
	}
}

func TestPortalSupportedTypes(t *testing.T) {
	h, ctx, _ := newHandlerTestCtx(t)
	resp, err := h.PortalSupportedTypes(ctx, api.EmptyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Types) < 2 {
		t.Fatalf("want at least 2 provider types, got %+v", resp.Types)
	}
	// Sorted by ID
	for i := 1; i < len(resp.Types); i++ {
		if resp.Types[i-1].ID > resp.Types[i].ID {
			t.Fatalf("not sorted: %+v", resp.Types)
		}
	}
	// openai (id=1) should be present with non-empty default URL
	foundOpenAI := false
	for _, tp := range resp.Types {
		if tp.ID == 1 {
			foundOpenAI = true
			if tp.DefaultURL == "" {
				t.Fatalf("openai default URL should not be empty")
			}
		}
	}
	if !foundOpenAI {
		t.Fatal("openai (id=1) missing from supported types")
	}
}

func TestAdminList_AcrossOwners(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "alice-1", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 2, Name: "bob-1", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 3, Name: "carol-1", Status: 1})

	resp, err := h.AdminList(ctx, AdminListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 3 {
		t.Fatalf("admin list should show all 3 channels across owners, got %d", resp.Total)
	}
}

func TestAdminList_FilterByOwnerID(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 2, Name: "b", Status: 1})

	resp, _ := h.AdminList(ctx, AdminListRequest{OwnerID: "2"})
	if resp.Total != 1 || resp.Data[0].OwnerID != 2 {
		t.Fatalf("filter by owner_id failed: %+v", resp)
	}
}

func TestAdminList_NoPlaintextKey(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	cipher := h.Provider.GetCipher()
	ct, _ := cipher.Seal("sk-secret-PLAIN-XYZ", 1)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1, KeyCipher: ct, KeyLast4: "-XYZ"})

	resp, _ := h.AdminList(ctx, AdminListRequest{})
	body, _ := json.Marshal(resp)
	if strings.Contains(string(body), "sk-secret") {
		t.Fatal("plaintext key leaked in admin list response")
	}
}

func TestAdminGet_AnyOwner(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 99, Name: "x", Status: 1}
	db.Create(pc)
	resp, err := h.AdminGet(ctx, api.IDPathRequest{ID: strconv.FormatUint(uint64(pc.ID), 10)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OwnerID != 99 {
		t.Fatalf("admin Get should expose any owner: %+v", resp)
	}
}

func TestAdminDisable_StatusFlipsAndPublishes(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1}
	db.Create(pc)
	_, err := h.AdminDisable(ctx, api.IDPathRequest{ID: strconv.FormatUint(uint64(pc.ID), 10)})
	if err != nil {
		t.Fatal(err)
	}
	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if got.Status != 0 {
		t.Fatalf("status should be 0 after disable: %d", got.Status)
	}
}

func TestAdminDisable_NotFound(t *testing.T) {
	h, ctx, _ := newHandlerTestCtx(t)
	if _, err := h.AdminDisable(ctx, api.IDPathRequest{ID: "99999"}); err == nil {
		t.Fatal("nonexistent disable should 404")
	}
}

// === §1.5 handler-level BYOK gating: PATCH must re-check group BYOK switch ===

// TestPatchUpdate_DisabledBYOKBlocks covers §1.5: even an innocuous PATCH (e.g. weight)
// must be rejected once an admin flips the owner's group BYOK switch to false.
func TestPatchUpdate_DisabledBYOKBlocks(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1}
	db.Create(pc)

	// Admin disables group BYOK for the default group (id=1, seeded in newHandlerTestCtx).
	f := false
	if err := db.Model(&models.UserGroup{}).Where("id = 1").Update("byok_enabled", &f).Error; err != nil {
		t.Fatalf("disable group BYOK: %v", err)
	}

	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID:     strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{"weight": uint(5)},
	})
	assertAPIStatus(t, err, http.StatusForbidden)
}

// TestPatchUpdate_ModelMappingValidated covers §1.5: PATCHing model_mapping alone must
// re-run the model registry check so an unknown model reference is rejected at 400.
func TestPatchUpdate_ModelMappingValidated(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1}
	db.Create(pc)

	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID: strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{
			"model_mapping": map[string]any{"unknown-model": "gpt-4o"},
		},
	})
	assertAPIStatus(t, err, http.StatusBadRequest)
}

// TestPatchUpdateKey_DisabledBYOKBlocks covers §1.5 on the UpdateKey path: rotating the
// key must also re-check the group BYOK switch (admin can have flipped it since create).
func TestPatchUpdateKey_DisabledBYOKBlocks(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	cipher := h.Provider.GetCipher()
	oldCT, _ := cipher.Seal("sk-OLD-1234", 1)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "x", Status: 1, KeyCipher: oldCT, KeyLast4: "1234"}
	db.Create(pc)

	f := false
	if err := db.Model(&models.UserGroup{}).Where("id = 1").Update("byok_enabled", &f).Error; err != nil {
		t.Fatalf("disable group BYOK: %v", err)
	}

	_, err := h.PortalUpdateKey(ctx, UpdateKeyRequest{
		ID:  strconv.FormatUint(uint64(pc.ID), 10),
		Key: "sk-NEW-abcd",
	})
	assertAPIStatus(t, err, http.StatusForbidden)

	// Cipher untouched after rejection.
	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if !bytes.Equal(got.KeyCipher, oldCT) {
		t.Fatal("cipher should not change when BYOK is disabled")
	}
}

// === §4.8 test endpoint path routing by provider type ===

// TestTestPathForType covers §4.8: PortalTest must hit the correct upstream path
// for the channel's provider type (e.g. /v1/messages for Anthropic, /v1/chat/completions
// for OpenAI / Azure). Unknown types fall back to /v1/chat/completions.
func TestTestPathForType(t *testing.T) {
	cases := []struct {
		typ      int
		wantPath string
	}{
		{newAPIConstant.ChannelTypeOpenAI, "/v1/chat/completions"},
		{newAPIConstant.ChannelTypeAnthropic, "/v1/messages"},
		{newAPIConstant.ChannelTypeAzure, "/v1/chat/completions"},
		{9999, "/v1/chat/completions"}, // default fallback
	}
	for _, c := range cases {
		if got := testPathForType(c.typ); got != c.wantPath {
			t.Fatalf("typ=%d got=%s want=%s", c.typ, got, c.wantPath)
		}
	}
}

func TestModelInChannelWhitelist(t *testing.T) {
	pc := &models.PrivateChannel{
		Models: datatypes.JSONSlice[string]{"gpt-4o", "claude-3-5-sonnet"},
	}
	if !modelInChannelWhitelist(pc, "gpt-4o") {
		t.Fatal("expected gpt-4o in whitelist")
	}
	if modelInChannelWhitelist(pc, "evil-model") {
		t.Fatal("expected evil-model NOT in whitelist")
	}
	if modelInChannelWhitelist(&models.PrivateChannel{}, "gpt-4o") {
		t.Fatal("expected empty Models to reject any model")
	}
}

func TestResolveTestPathEmptyEndpointType(t *testing.T) {
	// 空 endpointType → 走 testPathForType(pc.Type) 旧逻辑
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: newAPIConstant.ChannelTypeOpenAI}}
	path, err := resolveTestPath(pc, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions, got %s", path)
	}
}

func TestResolveTestPathFromEndpointsJSON(t *testing.T) {
	// pc.Endpoints 配了 chat_completions 自定义路径，优先用它
	pc := &models.PrivateChannel{
		ChannelCore: models.ChannelCore{
			Type:      newAPIConstant.ChannelTypeOpenAI,
			Endpoints: `{"chat_completions":"/custom/chat","messages":"/custom/messages"}`,
		},
	}
	path, err := resolveTestPath(pc, "chat_completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/custom/chat" {
		t.Fatalf("expected /custom/chat, got %s", path)
	}
}

func TestResolveTestPathFallbackToFixed(t *testing.T) {
	// pc.Endpoints 空 → fallback 到固定路径
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Endpoints: ""}}
	for _, c := range []struct {
		endpointType string
		want         string
	}{
		{"chat_completions", "/v1/chat/completions"},
		{"responses", "/v1/responses"},
		{"messages", "/v1/messages"},
	} {
		path, err := resolveTestPath(pc, c.endpointType)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.endpointType, err)
		}
		if path != c.want {
			t.Fatalf("%s: expected %s, got %s", c.endpointType, c.want, path)
		}
	}
}

func TestResolveTestPathInvalidEndpointType(t *testing.T) {
	pc := &models.PrivateChannel{}
	_, err := resolveTestPath(pc, "embeddings")
	if err == nil {
		t.Fatal("expected error for invalid endpoint_type")
	}
}

func TestResolveTestPathEndpointsJSONMissingProtocol(t *testing.T) {
	// pc.Endpoints 配了别的 protocol 但没配请求的 → fallback
	pc := &models.PrivateChannel{
		ChannelCore: models.ChannelCore{
			Endpoints: `{"messages":"/custom/messages"}`,
		},
	}
	path, err := resolveTestPath(pc, "chat_completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/v1/chat/completions" {
		t.Fatalf("expected fallback /v1/chat/completions, got %s", path)
	}
}

// === §1.6 handler-level: PATCH base_url="" must be rejected at 400 ===

func TestPatchUpdate_EmptyBaseURLRejected(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v1"}, OwnerID: 1, Name: "x", Status: 1}
	db.Create(pc)

	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID:     strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{"base_url": ""},
	})
	assertAPIStatus(t, err, http.StatusBadRequest)

	// base_url untouched after rejection.
	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if got.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("base_url should not change when blanked patch is rejected, got %q", got.BaseURL)
	}
}

func TestPortalUpdate_ModelsPatchPersistsJSON(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.ModelConfig{ModelName: "claude-3-5-sonnet"})
	pc := &models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1,
		Name:        "x",
		Status:      1,
		Models:      datatypes.JSONSlice[string]{"gpt-4o"},
	}
	db.Create(pc)

	// PATCH 来自 BindURIAndBodyMap 解码后是 []any（JSON unmarshal 进 any 的默认类型）
	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID: strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{
			"models": []any{"gpt-4o", "claude-3-5-sonnet"},
		},
	})
	if err != nil {
		t.Fatalf("patch models: %v", err)
	}

	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if len(got.Models) != 2 || got.Models[0] != "gpt-4o" || got.Models[1] != "claude-3-5-sonnet" {
		t.Fatalf("models not persisted: %+v", []string(got.Models))
	}
}

func TestPortalUpdate_ModelMappingPatchPersistsJSON(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.ModelConfig{ModelName: "gpt-4"})
	pc := &models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1,
		Name:        "x",
		Status:      1,
		Models:      datatypes.JSONSlice[string]{"gpt-4o"},
	}
	db.Create(pc)

	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID: strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{
			"model_mapping": map[string]any{"gpt-4": "gpt-4o"},
		},
	})
	if err != nil {
		t.Fatalf("patch model_mapping: %v", err)
	}

	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if v := got.ModelMapping.Data()["gpt-4"]; v != "gpt-4o" {
		t.Fatalf("mapping not persisted: data=%+v", got.ModelMapping.Data())
	}
}

func TestPortalUpdate_PartialPatchPreservesJSONColumns(t *testing.T) {
	// PATCH 里没有 models / model_mapping 时，normalize 不应当误写零值
	// （normalizeJSONColumns 只对存在的 key 生效）。
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{
		ChannelCore:  models.ChannelCore{Type: 1},
		OwnerID:      1,
		Name:         "x",
		Status:       1,
		Models:       datatypes.JSONSlice[string]{"gpt-4o"},
		ModelMapping: datatypes.NewJSONType(map[string]string{"alias": "gpt-4o"}),
	}
	db.Create(pc)

	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID:     strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{"name": "renamed"},
	})
	if err != nil {
		t.Fatalf("partial patch: %v", err)
	}

	var got models.PrivateChannel
	db.First(&got, pc.ID)
	if got.Name != "renamed" {
		t.Fatalf("name should be renamed, got %q", got.Name)
	}
	if len(got.Models) != 1 || got.Models[0] != "gpt-4o" {
		t.Fatalf("models should be preserved, got %+v", []string(got.Models))
	}
	if v := got.ModelMapping.Data()["alias"]; v != "gpt-4o" {
		t.Fatalf("model_mapping should be preserved, got %+v", got.ModelMapping.Data())
	}
}

func TestPortalUpdate_RejectsEmptyModels(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1,
		Name:        "x",
		Status:      1,
		Models:      datatypes.JSONSlice[string]{"gpt-4o"},
	}
	db.Create(pc)

	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID:     strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{"models": []any{}},
	})
	apiErr := assertAPIStatus(t, err, http.StatusBadRequest)
	if !strings.Contains(apiErr.Error(), "models must not be empty") {
		t.Fatalf("unexpected error: %q", apiErr.Error())
	}
}

func TestPortalUpdate_SubsetCheckRunsForNonEmptyPatch(t *testing.T) {
	// 加入 NonEmpty validator 后，非空 PATCH 路径仍须落到 subset 校验——
	// 这条覆盖的是"NonEmpty 不会短路掉下游 validator"，不是 registry 顺序
	// 不变性（两个 validator 的输入互不相交，顺序错位无法靠输入区分）。
	h, ctx, db := newHandlerTestCtx(t)
	pc := &models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1},
		OwnerID:     1,
		Name:        "x",
		Status:      1,
		Models:      datatypes.JSONSlice[string]{"gpt-4o"},
	}
	db.Create(pc)

	_, err := h.PortalUpdate(ctx, UpdateRequest{
		ID: strconv.FormatUint(uint64(pc.ID), 10),
		Fields: map[string]any{
			"models": []any{"gpt-4o", "not-registered"},
		},
	})
	apiErr := assertAPIStatus(t, err, http.StatusBadRequest)
	if !strings.Contains(apiErr.Error(), "not-registered") {
		t.Fatalf("expected subset error mentioning 'not-registered', got %q", apiErr.Error())
	}
}
