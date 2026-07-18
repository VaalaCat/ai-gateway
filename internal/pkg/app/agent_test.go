package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// Compile-time check that the interface set exists and has expected methods.
func TestAgentApplicationInterfaceShape(t *testing.T) {
	var _ AgentApplication = (*stubAgentApp)(nil)
	var _ BodyStore = (*stubBodyStore)(nil)
	var _ ReplayBody = (*stubReplayBody)(nil)
	var _ AgentCache = (*stubAgentCache)(nil)
	var _ TransportPool = (*stubTransportPool)(nil)
}

func TestBodyContracts(t *testing.T) {
	store := &stubBodyStore{}
	body, err := store.Capture(context.Background(), bytes.NewBufferString("body"), BodyLimits{
		MemoryThreshold: 2,
		HardLimit:       8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if body.Size() != 4 {
		t.Fatalf("Size() = %d, want 4", body.Size())
	}
	got, err := body.Bytes(4)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "body" {
		t.Fatalf("Bytes() = %q, want body", got)
	}
}

// TestAgentApplicationNilReturns: AgentApplication 是纯依赖容器接口，
// stub 返回 nil 必须可被直接调用（不应 panic）。
func TestAgentApplicationNilReturns(t *testing.T) {
	var app AgentApplication = stubAgentApp{}
	if app.GetCache() != nil {
		t.Fatal("stub GetCache should be nil")
	}
	if app.GetLogger() != nil {
		t.Fatal("stub GetLogger should be nil")
	}
	if app.GetConfig() != nil {
		t.Fatal("stub GetConfig should be nil")
	}
	if app.GetTransportPool() != nil {
		t.Fatal("stub GetTransportPool should be nil")
	}
}

// TestAgentCacheEmbedsStore: 边界 — AgentCache 必须 *组合* Store，
// 否则 master 端的 Store-only 方法（GetToken 等）就拿不到。
func TestAgentCacheEmbedsStore(t *testing.T) {
	var c AgentCache = stubAgentCache{}
	// GetToken 来自嵌入的 Store
	if got := c.GetToken(context.Background(), "k"); got != nil {
		t.Fatal("stub Store.GetToken should be nil")
	}
}

// --- stubs ---

type stubAgentApp struct{}

func (stubAgentApp) GetCache() AgentCache                  { return nil }
func (stubAgentApp) GetLogger() *zap.Logger                { return nil }
func (stubAgentApp) GetConfig() *config.AgentRuntimeConfig { return nil }
func (stubAgentApp) GetTransportPool() TransportPool       { return nil }

func (stubAgentApp) GetBodyStore() BodyStore     { return nil }
func (stubAgentApp) RelayTimeout() time.Duration { return 0 }

type stubBodyStore struct{}

func (*stubBodyStore) Capture(_ context.Context, src io.Reader, _ BodyLimits) (ReplayBody, error) {
	b, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	return &stubReplayBody{body: b}, nil
}

type stubReplayBody struct {
	body []byte
}

func (b *stubReplayBody) Size() int64 { return int64(len(b.body)) }
func (b *stubReplayBody) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.body)), nil
}
func (b *stubReplayBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.body)) > limit {
		return nil, io.ErrShortBuffer
	}
	return append([]byte(nil), b.body...), nil
}
func (b *stubReplayBody) Close() error { return nil }

type stubAgentCache struct {
	stubStore
}

func (stubAgentCache) FindTokenRoute(uint, string) *models.AgentRoute { return nil }
func (stubAgentCache) FindAdminChannelRoute(uint, string) *models.AgentRoute {
	return nil
}
func (stubAgentCache) EffectiveRequestLimiters(uint, uint) []*models.RequestLimiter {
	return nil
}
func (stubAgentCache) EffectiveAttemptLimiters(uint, uint, string, uint) []*models.RequestLimiter {
	return nil
}

type stubTransportPool struct{}

func (stubTransportPool) Get(*models.Channel) *http.Transport { return nil }
func (stubTransportPool) Invalidate(uint, string)             {}
func (stubTransportPool) CloseIdleConnections()               {}

// stubStore 满足 Store 接口的最小实现（全部返回零值）。
type stubStore struct{}

func (stubStore) GetToken(context.Context, string) *models.Token     { return nil }
func (stubStore) SetToken(*models.Token)                             {}
func (stubStore) DeleteToken(string)                                 {}
func (stubStore) GetTokenByID(context.Context, uint) *models.Token   { return nil }
func (stubStore) DeleteTokenByID(uint)                               {}
func (stubStore) LoadTokens([]models.Token)                          {}
func (stubStore) TokenCount() int                                    { return 0 }
func (stubStore) GetChannel(uint) *models.Channel                    { return nil }
func (stubStore) SetChannel(*models.Channel)                         {}
func (stubStore) DeleteChannel(uint)                                 {}
func (stubStore) LoadChannels([]models.Channel)                      {}
func (stubStore) ChannelCount() int                                  { return 0 }
func (stubStore) GetModelConfig(string) *models.ModelConfig          { return nil }
func (stubStore) SetModelConfig(*models.ModelConfig)                 {}
func (stubStore) DeleteModelConfig(string)                           {}
func (stubStore) LoadModelConfigs([]models.ModelConfig)              {}
func (stubStore) ModelConfigCount() int                              { return 0 }
func (stubStore) GetChannelsForModel(string) []*models.Channel       { return nil }
func (stubStore) RebuildModelIndex()                                 {}
func (stubStore) GetAllModelNames() []string                         { return nil }
func (stubStore) GetUser(context.Context, uint) *protocol.SyncedUser { return nil }
func (stubStore) SetUserQuota(uint, int64)                           {}
func (stubStore) GetVisiblePrivateChannelsForUser(uint, string) []*protocol.SyncedPrivateChannel {
	return nil
}
func (stubStore) ListVisibleBYOKModelNamesForUser(uint) []string  { return nil }
func (stubStore) GetAgent(string) *models.Agent                   { return nil }
func (stubStore) SetAgent(*models.Agent)                          {}
func (stubStore) UpdateAgentAutoAddresses(string, []AgentAddress) {}
func (stubStore) DeleteAgent(string)                              {}
func (stubStore) GetAgentsByTag(string) []*models.Agent           { return nil }
func (stubStore) GetAllAgents() []*models.Agent                   { return nil }
func (stubStore) LoadAgents([]models.Agent)                       {}
func (stubStore) AgentCount() int                                 { return 0 }
func (stubStore) Version() int64                                  { return 0 }
func (stubStore) SetVersion(int64)                                {}
func (stubStore) LoadSettings([]models.Setting)                   {}
func (stubStore) Settings() settings.AgentSettings                { return settings.AgentSettings{} }
func (stubStore) TraceMaxBodySize() int                           { return 0 }
func (stubStore) FallbackSleepMs() int                            { return 0 }
func (stubStore) GetSystemTestToken() *models.Token               { return nil }
func (stubStore) HandleSyncEvent(string, string, []byte)          {}
