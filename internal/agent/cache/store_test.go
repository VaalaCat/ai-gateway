package cache

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestStoreTokenCRUD(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})

	token := &models.Token{ID: 1, Key: "sk-test", UserID: 1, Status: 1}
	s.SetToken(token)

	got := s.GetToken(context.Background(), "sk-test")
	if got == nil {
		t.Fatal("token not found")
	}
	if got.ID != 1 {
		t.Errorf("id = %d, want 1", got.ID)
	}

	s.DeleteToken("sk-test")
	if s.GetToken(context.Background(), "sk-test") != nil {
		t.Error("token should be deleted")
	}
}

func TestStoreModelIndex(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})

	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: 1, Priority: 1, Weight: 1}, Models: "gpt-4o,gpt-3.5-turbo"})
	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: 1, Priority: 0, Weight: 2}, Models: "gpt-4o"})
	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 3, Status: 1, Priority: 0, Weight: 1}, Models: "claude-sonnet-4-20250514"})
	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 4, Status: 0, Priority: 0, Weight: 1}, Models: "gpt-4o"}) // disabled

	s.RebuildModelIndex()

	gpt4o := s.GetChannelsForModel("gpt-4o")
	if len(gpt4o) != 2 { // channels 1 and 2 (not 4, which is disabled)
		t.Errorf("gpt-4o channels = %d, want 2", len(gpt4o))
	}

	claude := s.GetChannelsForModel("claude-sonnet-4-20250514")
	if len(claude) != 1 {
		t.Errorf("claude channels = %d, want 1", len(claude))
	}

	none := s.GetChannelsForModel("nonexistent")
	if len(none) != 0 {
		t.Errorf("nonexistent should be empty, got %d", len(none))
	}
}

func TestHandleSyncEvent(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})

	// LRU apply-if-present：cache 为空时 push create 不 warm
	tokenJSON := `{"id":1,"key":"sk-new","user_id":1,"status":1,"expired_at":-1}`
	s.HandleSyncEvent(events.EntityToken, events.ActionCreate, []byte(tokenJSON))
	if s.GetToken(context.Background(), "sk-new") != nil {
		t.Error("LRU mode: push token.create on absent key must not warm cache")
	}

	// 先用 SetToken（FullSync 路径）warm cache
	s.SetToken(&models.Token{ID: 1, Key: "sk-new", UserID: 1, Status: 1, ExpiredAt: -1})

	// push update 应覆写已缓存的 token
	tokenJSON = `{"id":1,"key":"sk-new","user_id":1,"status":0,"expired_at":-1}`
	s.HandleSyncEvent(events.EntityToken, events.ActionUpdate, []byte(tokenJSON))
	got := s.GetToken(context.Background(), "sk-new")
	if got == nil {
		t.Fatal("token should still exist after push update event")
	}
	if got.Status != 0 {
		t.Errorf("token status = %d, want 0 after push update", got.Status)
	}

	// push delete 应移除
	tokenJSON = `{"id":1,"key":"sk-new"}`
	s.HandleSyncEvent(events.EntityToken, events.ActionDelete, []byte(tokenJSON))
	if s.GetToken(context.Background(), "sk-new") != nil {
		t.Error("token should be deleted after push delete")
	}
}

func TestCounts(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetToken(&models.Token{ID: 1, Key: "sk-1", Status: 1})
	s.SetToken(&models.Token{ID: 2, Key: "sk-2", Status: 1})
	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: 1}, Models: "gpt-4o"})

	if s.TokenCount() != 2 {
		t.Errorf("tokens = %d, want 2", s.TokenCount())
	}
	if s.ChannelCount() != 1 {
		t.Errorf("channels = %d, want 1", s.ChannelCount())
	}
}

func TestStore_GetChannel(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	if ch := s.GetChannel(999); ch != nil {
		t.Error("expected nil for missing channel")
	}
	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 1, Name: "ch1", Status: 1}, Models: "gpt-4o"})
	ch := s.GetChannel(1)
	if ch == nil || ch.Name != "ch1" {
		t.Error("expected channel ch1")
	}
}

func TestStore_DeleteChannel(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 1, Name: "ch1", Status: 1}})
	s.DeleteChannel(1)
	if ch := s.GetChannel(1); ch != nil {
		t.Error("expected nil after delete")
	}
}

func TestStore_ModelConfig(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	if mc := s.GetModelConfig("gpt-4o"); mc != nil {
		t.Error("expected nil for missing model config")
	}
	s.SetModelConfig(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 2.5})
	mc := s.GetModelConfig("gpt-4o")
	if mc == nil || mc.InputPrice != 2.5 {
		t.Error("expected model config with price 2.5")
	}
	s.DeleteModelConfig("gpt-4o")
	if mc := s.GetModelConfig("gpt-4o"); mc != nil {
		t.Error("expected nil after delete")
	}
}

func TestStore_Version(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	if v := s.Version(); v != 0 {
		t.Errorf("expected initial version 0, got %d", v)
	}
	s.SetVersion(42)
	if v := s.Version(); v != 42 {
		t.Errorf("expected version 42, got %d", v)
	}
}

func TestStore_LoadBulk(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	tokens := []models.Token{
		{ID: 1, Key: "k1", Status: 1},
		{ID: 2, Key: "k2", Status: 1},
	}
	s.LoadTokens(tokens)
	if s.TokenCount() != 2 {
		t.Errorf("expected 2 tokens, got %d", s.TokenCount())
	}

	channels := []models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Name: "c1", Status: 1}, Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 2, Name: "c2", Status: 1}, Models: "claude-3"},
	}
	s.LoadChannels(channels)
	if s.ChannelCount() != 2 {
		t.Errorf("expected 2 channels, got %d", s.ChannelCount())
	}

	configs := []models.ModelConfig{
		{ModelName: "gpt-4o", InputPrice: 2.5},
		{ModelName: "claude-3", InputPrice: 3.0},
	}
	s.LoadModelConfigs(configs)
	if s.ModelConfigCount() != 2 {
		t.Errorf("expected 2 model configs, got %d", s.ModelConfigCount())
	}
}

func TestStore_GetAllModelNames(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: 1}, Models: "gpt-4o,claude-3"})
	s.RebuildModelIndex()
	names := s.GetAllModelNames()
	if len(names) != 2 {
		t.Errorf("expected 2 model names, got %d", len(names))
	}
}

func TestStore_HandleSyncEvent_Channels(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})

	// Channel create
	chJSON := []byte(`{"id":1,"name":"ch1","models":"gpt-4o","status":1}`)
	s.HandleSyncEvent(events.EntityChannel, events.ActionCreate, chJSON)
	if s.GetChannel(1) == nil {
		t.Error("expected channel after sync create")
	}

	// Channel delete
	s.HandleSyncEvent(events.EntityChannel, events.ActionDelete, chJSON)
	if s.GetChannel(1) != nil {
		t.Error("expected nil after channel sync delete")
	}
}

func TestStore_Settings_TraceMaxBodySize(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	// Default should be 64KB (来自 settings.Defaults())
	if got := s.TraceMaxBodySize(); got != 64*1024 {
		t.Errorf("default TraceMaxBodySize = %d, want %d", got, 64*1024)
	}
	// 通过 LoadSettings 更新,不再暴露 SetTraceMaxBodySize
	s.LoadSettings([]models.Setting{{Key: "trace_max_body_size", Value: "1048576"}})
	if got := s.TraceMaxBodySize(); got != 1024*1024 {
		t.Errorf("TraceMaxBodySize = %d, want %d", got, 1024*1024)
	}
}

func TestStore_LoadSettings(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	settings := []models.Setting{
		{Key: "trace_max_body_size", Value: "131072"},
		{Key: "unknown_key", Value: "ignored"},
	}
	s.LoadSettings(settings)
	if got := s.TraceMaxBodySize(); got != 131072 {
		t.Errorf("TraceMaxBodySize = %d, want 131072", got)
	}
}

func TestStore_HandleSyncEvent_Setting(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	settingJSON := []byte(`{"key":"trace_max_body_size","value":"262144"}`)
	s.HandleSyncEvent(events.EntitySetting, events.ActionUpdate, settingJSON)
	if got := s.TraceMaxBodySize(); got != 262144 {
		t.Errorf("TraceMaxBodySize = %d, want 262144", got)
	}
}

func TestStore_HandleSyncEvent_ModelConfig(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})

	// Model create
	mcJSON := []byte(`{"model_name":"gpt-4o","input_price":2.5}`)
	s.HandleSyncEvent(events.EntityModel, events.ActionCreate, mcJSON)
	if s.GetModelConfig("gpt-4o") == nil {
		t.Error("expected model config after sync create")
	}

	// Model delete
	s.HandleSyncEvent(events.EntityModel, events.ActionDelete, mcJSON)
	if s.GetModelConfig("gpt-4o") != nil {
		t.Error("expected nil after model sync delete")
	}

	// Also test with "model_config" entity name
	s.HandleSyncEvent(events.EntityModelV1, events.ActionCreate, mcJSON)
	if s.GetModelConfig("gpt-4o") == nil {
		t.Error("expected model config after model_config sync create")
	}
}

func TestStoreUpdateAgentAutoAddresses_ReplacesAutoDetected(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetAgent(&models.Agent{
		AgentID:       "agent-a",
		HTTPAddresses: `[{"url":"http://10.0.0.1:8139","tag":"auto-detected"}]`,
	})

	s.UpdateAgentAutoAddresses("agent-a", []agentproxy.Address{
		{URL: "http://10.0.0.2:8139", Tag: "auto-detected"},
	})

	agent := s.GetAgent("agent-a")
	if agent == nil {
		t.Fatal("expected agent to exist")
	}

	var addrs []agentproxy.Address
	if err := json.Unmarshal([]byte(agent.HTTPAddresses), &addrs); err != nil {
		t.Fatalf("parse http_addresses failed: %v", err)
	}
	if len(addrs) != 1 || addrs[0].URL != "http://10.0.0.2:8139" {
		t.Fatalf("unexpected addresses: %#v", addrs)
	}
}

func TestStoreUpdateAgentAutoAddresses_DoesNotOverrideManual(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	manual := `[{"url":"http://manual.internal:8139","tag":"internal"}]`
	s.SetAgent(&models.Agent{
		AgentID:       "agent-b",
		HTTPAddresses: manual,
	})

	s.UpdateAgentAutoAddresses("agent-b", []agentproxy.Address{
		{URL: "http://10.0.0.3:8139", Tag: "auto-detected"},
	})

	agent := s.GetAgent("agent-b")
	if agent == nil {
		t.Fatal("expected agent to exist")
	}
	if agent.HTTPAddresses != manual {
		t.Fatalf("manual addresses should not be overridden, got: %s", agent.HTTPAddresses)
	}
}

func TestStore_UserGroup_RoundTrip(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	g := &models.UserGroup{ID: 5, Name: "x", Status: 1}
	s.SetUserGroup(g)
	if got := s.GetUserGroup(5); got == nil || got.Name != "x" {
		t.Fatalf("missing group: %+v", got)
	}
	if s.UserGroupCount() != 1 {
		t.Fatalf("count = %d", s.UserGroupCount())
	}
	s.DeleteUserGroup(5)
	if s.GetUserGroup(5) != nil {
		t.Fatalf("group not deleted")
	}
}

func TestStore_User_RoundTrip(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetUser(&protocol.SyncedUser{ID: 42, GroupID: 7})
	got := s.GetUser(context.Background(), 42)
	if got == nil || got.GroupID != 7 {
		t.Fatalf("user mismatch: %+v", got)
	}
}

func TestStore_HandleSyncEvent_UserGroup(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	g := models.UserGroup{ID: 5, Name: "x", Status: 1}
	data, _ := json.Marshal(g)
	s.HandleSyncEvent(events.EntityUserGroup, events.ActionCreate, data)
	if s.GetUserGroup(5) == nil {
		t.Fatalf("group not stored")
	}
	s.HandleSyncEvent(events.EntityUserGroup, events.ActionDelete, data)
	if s.GetUserGroup(5) != nil {
		t.Fatalf("group not deleted")
	}
}

func TestStore_HandleSyncEvent_User(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})

	// LRU apply-if-present：cache 为空时 push update 不 warm
	u := protocol.SyncedUser{ID: 42, GroupID: 7}
	data, _ := json.Marshal(u)
	s.HandleSyncEvent(events.EntityUser, events.ActionUpdate, data)
	if s.GetUser(context.Background(), 42) != nil {
		t.Fatalf("LRU mode: push user.update on absent key must not warm cache")
	}

	// 先用 SetUser（FullSync 路径）warm cache
	s.SetUser(&protocol.SyncedUser{ID: 42, GroupID: 7})

	// push update 应覆写
	u2 := protocol.SyncedUser{ID: 42, GroupID: 99}
	data2, _ := json.Marshal(u2)
	s.HandleSyncEvent(events.EntityUser, events.ActionUpdate, data2)
	got := s.GetUser(context.Background(), 42)
	if got == nil {
		t.Fatalf("user should still exist after push update")
	}
	if got.GroupID != 99 {
		t.Errorf("user GroupID = %d, want 99 after push update", got.GroupID)
	}

	// push delete 应移除
	s.HandleSyncEvent(events.EntityUser, events.ActionDelete, data2)
	if s.GetUser(context.Background(), 42) != nil {
		t.Fatalf("user should be deleted after push delete")
	}
}

func TestHandleSyncEvent_TokenApplyIfPresent_DropsAbsent(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	tokenJSON := `{"id":99,"key":"sk-cold","user_id":1,"status":1,"expired_at":-1}`
	s.HandleSyncEvent(events.EntityToken, events.ActionCreate, []byte(tokenJSON))
	if got := s.GetToken(context.Background(), "sk-cold"); got != nil {
		t.Errorf("LRU apply-if-present: push create on absent must not warm, got %+v", got)
	}
}

func TestHandleSyncEvent_UserApplyIfPresent_DropsAbsent(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	u := protocol.SyncedUser{ID: 77, GroupID: 3}
	data, _ := json.Marshal(u)
	s.HandleSyncEvent(events.EntityUser, events.ActionCreate, data)
	if got := s.GetUser(context.Background(), 77); got != nil {
		t.Errorf("LRU apply-if-present: push user.create on absent must not warm, got %+v", got)
	}
}
