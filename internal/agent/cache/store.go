package cache

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache/loaders"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

var _ app.Store = (*Store)(nil)

type Store struct {
	tokenStore             *tokenStore
	users                  entitycache.EntityCache[uint, *protocol.SyncedUser]
	channels               entitycache.EntityCache[uint, *models.Channel]
	modelConfigs           entitycache.EntityCache[string, *models.ModelConfig]
	agents                 entitycache.EntityCache[string, *models.Agent]
	userGroups             entitycache.EntityCache[uint, *models.UserGroup]
	modelChannels          utils.SyncMap[string, []*models.Channel]
	globalRoutings         entitycache.EntityCache[string, *protocol.SyncedRouting]
	userRoutings           entitycache.EntityCache[uint, *protocol.UserRoutingMap]
	tokenRoutings          entitycache.EntityCache[uint, *protocol.TokenRoutingMap]
	visiblePrivateChannels entitycache.EntityCache[uint, *protocol.VisiblePrivateChannelSet]

	RouteIndex *RouteIndex

	LimiterIndex *LimiterIndex

	version  atomic.Int64
	mu       sync.Mutex // protects index rebuild
	agentsMu sync.Mutex // protects configured agents, runtime addresses, and effective snapshots

	agentCapabilitiesMu          sync.RWMutex
	agentCapabilities            map[string][]string
	configuredAgents             map[string]*models.Agent
	directAddressSessionStarted  bool
	directAddressMaster          string
	configuredSnapshotReady      bool
	acceptUnknownDirectAddresses bool
	directAddressLatest          map[string]directAddressVersion
	directAddressOverlays        map[string]string // key presence wins; an empty value is a tombstone

	// settings 持有 master 同步过来的全局配置快照。
	// 读路径走 atomic.Load(无锁,hot path);写路径(applySetting)用 settingsMu
	// 串行化 read-modify-write,防止 event bus 并发 handler 丢失 update。
	settingsMu sync.Mutex
	settings   atomic.Pointer[settings.AgentSettings]

	logger               *zap.Logger
	resolveLogSuppressor *diagnostics.Suppressor

	scripts *scriptStore

	onChannelChange []func(old, new *models.Channel)
	cacheLifecycle  *entitycache.Lifecycle
}

// NewStore 装配 agent 端缓存 Store。
// client 用于 LRU 实体的 miss 拉取（可为 nil；nil 时 LRU 实体只读缓存）。
// cfg 决定 LRU 容量与负缓存 TTL；零值/非法值由 normalize 兜底为默认。
//
// 选择性 LRU：tokens / users 走 LRU；channels / modelConfigs / agents / userGroups
// 仍是 admin 维护的小规模实体，走 FullCache。
func NewStore(client app.WSClient, cfg config.AgentCacheConfig) *Store {
	cacheLifecycle := entitycache.NewLifecycle()
	s := &Store{
		channels:              entitycache.NewFullCache[uint, *models.Channel](),
		modelConfigs:          entitycache.NewFullCache[string, *models.ModelConfig](),
		agents:                entitycache.NewFullCache[string, *models.Agent](),
		agentCapabilities:     make(map[string][]string),
		configuredAgents:      make(map[string]*models.Agent),
		directAddressLatest:   make(map[string]directAddressVersion),
		directAddressOverlays: make(map[string]string),
		userGroups:            entitycache.NewFullCache[uint, *models.UserGroup](),
		RouteIndex:            NewRouteIndex(),
		LimiterIndex:          NewLimiterIndex(),
		cacheLifecycle:        cacheLifecycle,
		resolveLogSuppressor:  diagnostics.NewSuppressor(diagnostics.SuppressorOptions{}),
	}
	{
		snap := settings.AgentSettings{}
		for k, v := range settings.Defaults() {
			_ = settings.Apply(&snap, k, v) // 默认值不会越界,error 安全忽略
		}
		s.settings.Store(&snap)
	}
	refreshCfg := func() entitycache.RefreshConfig {
		set := s.Settings()
		return entitycache.RefreshConfig{
			LoadTimeout:        time.Duration(set.CacheLoadTimeoutMs) * time.Millisecond,
			RefreshAfter:       time.Duration(set.CacheRefreshAfterMs) * time.Millisecond,
			RefreshTimeout:     time.Duration(set.CacheRefreshTimeoutMs) * time.Millisecond,
			RefreshMaxRetries:  set.CacheRefreshMaxRetries,
			RefreshBackoffBase: time.Duration(set.CacheRefreshBackoffBaseMs) * time.Millisecond,
			RefreshBackoffMax:  time.Duration(set.CacheRefreshBackoffMaxMs) * time.Millisecond,
		}
	}

	s.logger = zap.NewNop()
	s.scripts = newScriptStore(s.logger)

	negTTL := resolveNegativeTTL(cfg.NegativeTTLSeconds)

	tokenCap := cfg.TokenCapacity
	if tokenCap <= 0 {
		tokenCap = 20000
	}
	userCap := cfg.UserCapacity
	if userCap <= 0 {
		userCap = 20000
	}

	users, err := newUserLRU(client, userCap, negTTL, refreshCfg, cacheLifecycle)
	if err != nil {
		panic(err)
	}
	s.users = users

	s.globalRoutings = entitycache.NewFullCache[string, *protocol.SyncedRouting]()

	routingCap := cfg.UserRoutingsCapacity
	if routingCap <= 0 {
		routingCap = 5000
	}
	var routingLoader entitycache.Loader[uint, *protocol.UserRoutingMap]
	if client != nil {
		routingLoader = &loaders.UserRoutingsLoader{Client: client}
	}
	userRoutings, err := entitycache.NewLRUCache(entitycache.Config[uint, *protocol.UserRoutingMap]{
		Capacity:    routingCap,
		Loader:      routingLoader,
		NegativeTTL: negTTL,
		Refresh:     refreshCfg,
		Lifecycle:   cacheLifecycle,
	})
	if err != nil {
		panic(err)
	}
	s.userRoutings = userRoutings

	var tokenRoutingLoader entitycache.Loader[uint, *protocol.TokenRoutingMap]
	if client != nil {
		tokenRoutingLoader = &loaders.TokenRoutingsLoader{Client: client}
	}
	tokenRoutings, err := entitycache.NewLRUCache(entitycache.Config[uint, *protocol.TokenRoutingMap]{
		Capacity:    routingCap,
		Loader:      tokenRoutingLoader,
		NegativeTTL: negTTL,
		Refresh:     refreshCfg,
		Lifecycle:   cacheLifecycle,
	})
	if err != nil {
		panic(err)
	}
	s.tokenRoutings = tokenRoutings
	s.tokenStore = newTokenStoreLRU(client, s.users, s.tokenRoutings, tokenCap, negTTL, refreshCfg, cacheLifecycle)

	pchanCap := cfg.PrivateChannelsCapacity
	if pchanCap <= 0 {
		pchanCap = 5000
	}
	var pchanLoader entitycache.Loader[uint, *protocol.VisiblePrivateChannelSet]
	if client != nil {
		pchanLoader = &loaders.PrivateChannelsVisibleLoader{Client: client}
	}
	privateChans, err := entitycache.NewLRUCache(
		entitycache.Config[uint, *protocol.VisiblePrivateChannelSet]{
			Capacity:    pchanCap,
			Loader:      pchanLoader,
			NegativeTTL: negTTL,
			Refresh:     refreshCfg,
			Lifecycle:   cacheLifecycle,
		})
	if err != nil {
		panic(err)
	}
	s.visiblePrivateChannels = privateChans

	return s
}

type directAddressVersion struct {
	sessionGeneration uint64
	sequence          uint64
}

// SetLogger 注入 zap.Logger，用于 routing apply / resolve 等可观测性日志。
// 默认 NewStore 使用 zap.NewNop()；server 装配时调用以接入实际 logger。
func (s *Store) SetLogger(l *zap.Logger) {
	if l == nil {
		l = zap.NewNop()
	}
	s.logger = l
	if s.scripts != nil {
		s.scripts.setLogger(l)
	}
}

func newUserLRU(client app.WSClient, capacity int, negTTL time.Duration, refreshCfg func() entitycache.RefreshConfig, lifecycle *entitycache.Lifecycle) (entitycache.EntityCache[uint, *protocol.SyncedUser], error) {
	var loader entitycache.Loader[uint, *protocol.SyncedUser]
	if client != nil {
		loader = &loaders.UserLoader{Client: client}
	}
	return entitycache.NewLRUCache(entitycache.Config[uint, *protocol.SyncedUser]{
		Capacity:    capacity,
		Loader:      loader,
		NegativeTTL: negTTL,
		Refresh:     refreshCfg,
		Lifecycle:   lifecycle,
	})
}

func newTokenStoreLRU(client app.WSClient, users entitycache.EntityCache[uint, *protocol.SyncedUser], tokenRoutings entitycache.EntityCache[uint, *protocol.TokenRoutingMap], capacity int, negTTL time.Duration, refreshCfg func() entitycache.RefreshConfig, lifecycle *entitycache.Lifecycle) *tokenStore {
	ts := &tokenStore{}
	var loader entitycache.Loader[string, *models.Token]
	if client != nil {
		loader = &loaders.TokenLoader{Client: client, Users: users, TokenRoutings: tokenRoutings}
	}
	primary, err := entitycache.NewLRUCache(entitycache.Config[string, *models.Token]{
		Capacity:    capacity,
		Loader:      loader,
		NegativeTTL: negTTL,
		Refresh:     refreshCfg,
		Lifecycle:   lifecycle,
		OnEvict: func(_ string, tok *models.Token) {
			if tok != nil {
				ts.byID.Delete(tok.ID)
			}
		},
	})
	if err != nil {
		panic(err)
	}
	ts.primary = primary
	return ts
}

func (s *Store) Close() {
	if s != nil && s.cacheLifecycle != nil {
		s.cacheLifecycle.Close()
	}
}

func (s *Store) Done() <-chan struct{} {
	if s == nil || s.cacheLifecycle == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return s.cacheLifecycle.Done()
}

func (s *Store) ResourceCounts() (loads, refreshes int64) {
	if s == nil || s.cacheLifecycle == nil {
		return 0, 0
	}
	return s.cacheLifecycle.ResourceCounts()
}

// === Token API ===

func (s *Store) GetToken(ctx context.Context, key string) *models.Token {
	t, _, err := s.tokenStore.Get(ctx, key)
	if reason := classifyResolveErr(err); reason != "" && reason != "not_found" {
		s.logger.Warn("token auth resolve failed", zap.String("reason", reason))
	}
	return t
}

func (s *Store) SetToken(token *models.Token) {
	s.tokenStore.Set(token)
}

func (s *Store) DeleteToken(key string) {
	s.tokenStore.Delete(key)
}

func (s *Store) GetTokenByID(ctx context.Context, id uint) *models.Token {
	t, _, err := s.tokenStore.GetByID(ctx, id)
	if reason := classifyResolveErr(err); reason != "" && reason != "not_found" {
		s.logger.Warn("token auth resolve failed", zap.String("reason", reason))
	}
	return t
}

func (s *Store) DeleteTokenByID(id uint) {
	s.tokenStore.DeleteByID(id)
}

func (s *Store) TokenCount() int { return s.tokenStore.Len() }

// === User API ===

func (s *Store) GetUser(ctx context.Context, id uint) *protocol.SyncedUser {
	u, _, _ := s.users.Get(ctx, id)
	return u
}

func (s *Store) SetUser(u *protocol.SyncedUser) { s.users.Set(u.ID, u) }
func (s *Store) DeleteUser(id uint)             { s.users.Delete(id) }
func (s *Store) UserCount() int                 { return s.users.Len() }

// SetUserQuota 更新已缓存 user 的 Quota 字段;用于配额扣减后的原地刷新。
// 若 user 不在缓存中则静默忽略（不触发 loader 拉取）。
func (s *Store) SetUserQuota(id uint, quota int64) {
	u, ok := s.users.Peek(id)
	if !ok || u == nil {
		return
	}
	nu := *u
	nu.Quota = quota
	s.users.Set(id, &nu)
}

// === Channel ===

func (s *Store) GetChannel(id uint) *models.Channel {
	v, _ := s.channels.Peek(id)
	return v
}
func (s *Store) SetChannel(ch *models.Channel) {
	old, _ := s.channels.Peek(ch.ID)
	s.channels.Set(ch.ID, ch)
	s.emitChannelChange(old, ch)
}

func (s *Store) DeleteChannel(id uint) {
	old, _ := s.channels.Peek(id)
	s.channels.Delete(id)
	s.emitChannelChange(old, nil)
}

func (s *Store) ChannelCount() int { return s.channels.Len() }

// === ModelConfig ===

func (s *Store) GetModelConfig(modelName string) *models.ModelConfig {
	v, _ := s.modelConfigs.Peek(modelName)
	return v
}
func (s *Store) SetModelConfig(mc *models.ModelConfig) { s.modelConfigs.Set(mc.ModelName, mc) }
func (s *Store) DeleteModelConfig(modelName string)    { s.modelConfigs.Delete(modelName) }
func (s *Store) ModelConfigCount() int                 { return s.modelConfigs.Len() }

// === Agent ===

func (s *Store) GetAgent(agentID string) *models.Agent {
	v, _ := s.agents.Peek(agentID)
	return cloneAgent(v)
}
func (s *Store) SetAgent(agent *models.Agent) {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	s.setConfiguredAgentLocked(agent)
}
func (s *Store) DeleteAgent(agentID string) {
	s.agentsMu.Lock()
	s.deleteConfiguredAgentLocked(agentID)
	s.agentsMu.Unlock()
	s.DeleteAgentCapabilities(agentID)
}
func (s *Store) AgentCount() int { return s.agents.Len() }

func (s *Store) SetAgentCapabilities(agentID string, capabilities []string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	normalized := protocol.NormalizeAgentCapabilities(capabilities)
	s.agentCapabilitiesMu.Lock()
	if len(normalized) == 0 {
		delete(s.agentCapabilities, agentID)
	} else {
		s.agentCapabilities[agentID] = append([]string(nil), normalized...)
	}
	s.agentCapabilitiesMu.Unlock()
}

func (s *Store) GetAgentCapabilities(agentID string) []string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	s.agentCapabilitiesMu.RLock()
	capabilities := append([]string(nil), s.agentCapabilities[agentID]...)
	s.agentCapabilitiesMu.RUnlock()
	if len(capabilities) == 0 {
		return nil
	}
	return capabilities
}

func (s *Store) DeleteAgentCapabilities(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	s.agentCapabilitiesMu.Lock()
	delete(s.agentCapabilities, agentID)
	s.agentCapabilitiesMu.Unlock()
}

func (s *Store) ClearAgentCapabilities() {
	s.agentCapabilitiesMu.Lock()
	s.agentCapabilities = make(map[string][]string)
	s.agentCapabilitiesMu.Unlock()
}

// === UserGroup ===

func (s *Store) GetUserGroup(id uint) *models.UserGroup {
	v, _ := s.userGroups.Peek(id)
	return v
}
func (s *Store) SetUserGroup(g *models.UserGroup) { s.userGroups.Set(g.ID, g) }
func (s *Store) DeleteUserGroup(id uint)          { s.userGroups.Delete(id) }
func (s *Store) UserGroupCount() int              { return s.userGroups.Len() }

// === Bulk Load (FullSync) ===

func (s *Store) LoadTokens(tokens []models.Token) {
	for i := range tokens {
		s.tokenStore.Set(&tokens[i])
	}
}
func (s *Store) LoadChannels(channels []models.Channel) {
	for i := range channels {
		s.channels.Set(channels[i].ID, &channels[i])
	}
}
func (s *Store) LoadModelConfigs(configs []models.ModelConfig) {
	for i := range configs {
		s.modelConfigs.Set(configs[i].ModelName, &configs[i])
	}
}
func (s *Store) LoadAgents(agents []models.Agent) {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	// behavior change: this call is the authoritative configured snapshot.
	// Enter that state before pruning so each delete stays O(1).
	s.configuredSnapshotReady = true
	s.acceptUnknownDirectAddresses = false
	present := make(map[string]struct{}, len(agents))
	for i := range agents {
		if strings.TrimSpace(agents[i].AgentID) == "" {
			continue
		}
		present[agents[i].AgentID] = struct{}{}
		s.setConfiguredAgentLocked(&agents[i])
	}
	for agentID := range s.configuredAgents {
		if _, ok := present[agentID]; !ok {
			s.deleteConfiguredAgentLocked(agentID)
		}
	}
	for agentID := range s.directAddressOverlays {
		if _, ok := present[agentID]; !ok {
			s.deleteConfiguredAgentLocked(agentID)
		}
	}
	for agentID := range s.directAddressLatest {
		if _, ok := present[agentID]; !ok {
			s.deleteConfiguredAgentLocked(agentID)
		}
	}
}
func (s *Store) LoadUserGroups(groups []models.UserGroup) {
	for i := range groups {
		s.userGroups.Set(groups[i].ID, &groups[i])
	}
}
func (s *Store) LoadUsers(users []protocol.SyncedUser) {
	for i := range users {
		s.users.Set(users[i].ID, &users[i])
	}
}

// === Settings ===

func (s *Store) LoadSettings(settings []models.Setting) {
	for _, setting := range settings {
		s.applySetting(setting.Key, setting.Value)
	}
}

// applySetting 用 master 推下来的 key/value 更新 settings snapshot。
// 解析或越界错误静默忽略(forward-compat,不让单条坏数据冲垮整个 store)。
// 用 settingsMu 串行化 RMW 临界区,防 setting bus 并发 handler 丢失 update。
func (s *Store) applySetting(key, value string) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cur := s.settings.Load()
	if cur == nil {
		cur = &settings.AgentSettings{}
	}
	next := *cur
	if err := settings.Apply(&next, key, value); err != nil {
		return
	}
	s.settings.Store(&next)
}

// === Settings / Trace ===

// Settings 返回当前同步配置快照(value copy,不可变,无锁读)。
func (s *Store) Settings() settings.AgentSettings {
	cur := s.settings.Load()
	if cur == nil {
		return settings.AgentSettings{}
	}
	return *cur
}

// TraceMaxBodySize 兼容老调用方(internal/agent/relay/handler.go:125-126 等)。
// 新代码请走 Settings().TraceMaxBodySize 直读。
func (s *Store) TraceMaxBodySize() int { return s.Settings().TraceMaxBodySize }

// FallbackSleepMs 实现 exec.SleepReader 接口。
// 单独抽出方法是为了避免 exec 包 import cache 包(叶子原则)。
func (s *Store) FallbackSleepMs() int { return s.Settings().FallbackSleepMs }

// === Version ===

func (s *Store) Version() int64     { return s.version.Load() }
func (s *Store) SetVersion(v int64) { s.version.Store(v) }

// AdvanceVersion moves the local global version forward without requiring
// adjacent values. Global versions may have gaps and stale pages may arrive.
func (s *Store) AdvanceVersion(version int64) {
	for {
		current := s.version.Load()
		if version <= current {
			return
		}
		if s.version.CompareAndSwap(current, version) {
			return
		}
	}
}

// === Model Index (派生) ===

func (s *Store) GetChannelsForModel(model string) []*models.Channel {
	v, ok := s.modelChannels.Load(model)
	if !ok {
		return nil
	}
	return v
}

// HasRealModel 判断 name 是否有 channel 支撑的真实模型（不含 routing）。
func (s *Store) HasRealModel(name string) bool {
	return len(s.GetChannelsForModel(name)) > 0
}

// GetAllModelNames 返回所有暴露给 /v1/models 的 model 名：
// - 真实 model（来自 channel.Models 派生的 modelChannels 索引）
// - 全局 enabled routing 的 name
// 用户级 routing 不进全局列表（避免命名冲突；用户调 /v1/models 时由 handler 叠加该用户的 user routing）。
func (s *Store) GetAllModelNames() []string {
	names := s.modelChannels.Keys()
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		seen[n] = true
	}
	s.globalRoutings.Range(func(name string, r *protocol.SyncedRouting) bool {
		if r != nil && r.Enabled && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
		return true
	})
	return names
}

// RebuildModelIndex 从 channels 重建 model→channels 派生索引。
func (s *Store) RebuildModelIndex() {
	s.mu.Lock()
	defer s.mu.Unlock()

	index := make(map[string][]*models.Channel)
	s.channels.Range(func(_ uint, ch *models.Channel) bool {
		if ch.Status != consts.StatusEnabled {
			return true
		}
		for m := range strings.SplitSeq(ch.Models, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				index[m] = append(index[m], ch)
			}
		}
		return true
	})
	s.modelChannels.Range(func(key string, _ []*models.Channel) bool {
		s.modelChannels.Delete(key)
		return true
	})
	for model, channels := range index {
		s.modelChannels.Store(model, channels)
	}
}

// === Limiter Index (派生) ===

// EffectiveRequestLimiters / EffectiveAttemptLimiters 委托给 LimiterIndex，
// 供 relay 侧 Gate 通过 app.AgentCache 接口读取（src: "admin"|"private"）。
func (s *Store) EffectiveRequestLimiters(userID, groupID uint) []*models.RequestLimiter {
	return s.LimiterIndex.EffectiveRequestLimiters(userID, groupID)
}
func (s *Store) EffectiveAttemptLimiters(userID, groupID uint, src string, channelID uint) []*models.RequestLimiter {
	return s.LimiterIndex.EffectiveAttemptLimiters(userID, groupID, src, channelID)
}

// === Agent helpers ===

// BeginDirectAddressSession removes runtime addresses from the previous
// control snapshot while preserving authoritative manual configuration.
func (s *Store) BeginDirectAddressSession(masterInstanceID string) {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()

	s.directAddressSessionStarted = true
	s.directAddressMaster = strings.TrimSpace(masterInstanceID)
	s.configuredSnapshotReady = false
	s.acceptUnknownDirectAddresses = true
	s.directAddressLatest = make(map[string]directAddressVersion)
	s.directAddressOverlays = make(map[string]string)
	for agentID := range s.configuredAgents {
		s.publishEffectiveAgentLocked(agentID)
	}
}

// ApplyDirectAddressesUpdate applies a newer event from the active Master
// epoch. The version high-water advances even when a manual address wins.
func (s *Store) ApplyDirectAddressesUpdate(update protocol.AgentDirectAddressesUpdate) bool {
	update.MasterInstanceID = strings.TrimSpace(update.MasterInstanceID)
	update.AgentID = strings.TrimSpace(update.AgentID)
	if update.MasterInstanceID == "" || update.AgentID == "" || update.SessionGeneration == 0 || update.Sequence == 0 {
		return false
	}
	for _, address := range update.HTTPAddresses {
		if strings.TrimSpace(address.URL) == "" || strings.TrimSpace(address.Tag) != "auto-detected" {
			return false
		}
	}

	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	if update.MasterInstanceID != s.directAddressMaster {
		return false
	}
	if s.configuredAgents[update.AgentID] == nil &&
		(s.configuredSnapshotReady || !s.acceptUnknownDirectAddresses) {
		return false
	}
	latest := s.directAddressLatest[update.AgentID]
	if update.SessionGeneration < latest.sessionGeneration || update.Sequence <= latest.sequence {
		return false
	}
	s.directAddressLatest[update.AgentID] = directAddressVersion{
		sessionGeneration: update.SessionGeneration,
		sequence:          update.Sequence,
	}
	addresses := ""
	if len(update.HTTPAddresses) > 0 {
		addrJSON, err := json.Marshal(update.HTTPAddresses)
		if err != nil {
			return false
		}
		addresses = string(addrJSON)
	}
	s.directAddressOverlays[update.AgentID] = addresses
	if s.configuredAgents[update.AgentID] != nil {
		s.publishEffectiveAgentLocked(update.AgentID)
	}
	return true
}

// UpdateAgentAutoAddresses updates in-memory auto-detected addresses for an
// agent without overriding manually configured addresses. It remains for
// callers that replace an already-authorized local snapshot.
func (s *Store) UpdateAgentAutoAddresses(agentID string, addrs []agentproxy.Address) {
	addresses := ""
	if len(addrs) > 0 {
		addrJSON, err := json.Marshal(addrs)
		if err != nil {
			return
		}
		addresses = string(addrJSON)
	}

	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	if s.configuredAgents[agentID] == nil {
		return
	}
	s.directAddressOverlays[agentID] = addresses
	s.publishEffectiveAgentLocked(agentID)
}

// GetAgentsByTag returns all active agents that have the given tag.
func (s *Store) GetAgentsByTag(tag string) []*models.Agent {
	var result []*models.Agent
	s.agents.Range(func(_ string, agent *models.Agent) bool {
		if agent.Status != consts.StatusEnabled {
			return true
		}
		for t := range strings.SplitSeq(agent.Tags, ",") {
			if strings.TrimSpace(t) == tag {
				result = append(result, cloneAgent(agent))
				break
			}
		}
		return true
	})
	return result
}

// GetAllAgents returns all cached agents.
func (s *Store) GetAllAgents() []*models.Agent {
	var result []*models.Agent
	s.agents.Range(func(_ string, agent *models.Agent) bool {
		result = append(result, cloneAgent(agent))
		return true
	})
	return result
}

func cloneAgent(agent *models.Agent) *models.Agent {
	if agent == nil {
		return nil
	}
	next := *agent
	return &next
}

func (s *Store) setConfiguredAgentLocked(agent *models.Agent) {
	if agent == nil || strings.TrimSpace(agent.AgentID) == "" {
		return
	}
	configured := cloneAgent(agent)
	s.configuredAgents[configured.AgentID] = configured
	s.publishEffectiveAgentLocked(configured.AgentID)
}

func (s *Store) deleteConfiguredAgentLocked(agentID string) {
	s.agents.Delete(agentID)
	delete(s.configuredAgents, agentID)
	delete(s.directAddressLatest, agentID)
	delete(s.directAddressOverlays, agentID)
	if !s.configuredSnapshotReady {
		// A delete before the first authoritative snapshot makes unknown runtime
		// ownership ambiguous. Drop unknown overlays and stay relay-only until
		// LoadAgents establishes the configured base.
		s.acceptUnknownDirectAddresses = false
		for pendingAgentID := range s.directAddressOverlays {
			if s.configuredAgents[pendingAgentID] == nil {
				delete(s.directAddressOverlays, pendingAgentID)
				delete(s.directAddressLatest, pendingAgentID)
			}
		}
	}
}

func (s *Store) publishEffectiveAgentLocked(agentID string) {
	configured := s.configuredAgents[agentID]
	if configured == nil {
		s.agents.Delete(agentID)
		return
	}

	effective := *configured
	if !hasConfiguredAgentAddresses(configured.HTTPAddresses) {
		if addresses, ok := s.directAddressOverlays[agentID]; ok {
			effective.HTTPAddresses = addresses
		} else if s.directAddressSessionStarted {
			effective.HTTPAddresses = ""
		}
	}
	s.agents.Set(agentID, &effective)
}

func hasConfiguredAgentAddresses(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw != "" && raw != "[]" && raw != "null"
}

// GetSystemTestToken finds the system test token by name.
// 走 Range 遍历主存储——LRU 模式下只看缓存中的 token，未缓存的不会被找到。
func (s *Store) GetSystemTestToken() *models.Token {
	var result *models.Token
	s.tokenStore.primary.Range(func(_ string, t *models.Token) bool {
		if t.Name == "__system_test__" {
			result = t
			return false
		}
		return true
	})
	return result
}

// === HandleSyncEvent ===

func (s *Store) HandleSyncEvent(entity, action string, data []byte) {
	switch entity {
	case events.EntityToken:
		var token models.Token
		if err := json.Unmarshal(data, &token); err != nil {
			return
		}
		if action == events.ActionDelete {
			s.tokenStore.Apply(entitycache.ActionDelete, &token)
		} else {
			s.tokenStore.Apply(entitycache.ActionSet, &token)
		}
	case events.EntityChannel:
		var ch models.Channel
		if err := json.Unmarshal(data, &ch); err != nil {
			return
		}
		old, _ := s.channels.Peek(ch.ID)
		if action == events.ActionDelete {
			s.channels.Apply(entitycache.ActionDelete, ch.ID, nil)
			s.emitChannelChange(old, nil)
		} else {
			s.channels.Apply(entitycache.ActionSet, ch.ID, &ch)
			s.emitChannelChange(old, &ch)
		}
		s.RebuildModelIndex()
	case events.EntityModelV1, events.EntityModel:
		var mc models.ModelConfig
		if err := json.Unmarshal(data, &mc); err != nil {
			return
		}
		if action == events.ActionDelete {
			s.modelConfigs.Apply(entitycache.ActionDelete, mc.ModelName, nil)
		} else {
			s.modelConfigs.Apply(entitycache.ActionSet, mc.ModelName, &mc)
		}
	case events.EntitySetting:
		var setting models.Setting
		if err := json.Unmarshal(data, &setting); err == nil {
			s.applySetting(setting.Key, setting.Value)
		}
	case events.EntityAgent:
		var agent models.Agent
		if err := json.Unmarshal(data, &agent); err != nil {
			return
		}
		if action == events.ActionDelete {
			s.DeleteAgent(agent.AgentID)
			return
		}
		s.SetAgent(&agent)
	case events.EntityAgentRoute:
		var route models.AgentRoute
		if err := json.Unmarshal(data, &route); err == nil {
			if action == events.ActionDelete {
				s.RouteIndex.Delete(route.ID)
			} else {
				s.RouteIndex.Put(&route)
			}
		}
	case events.EntityRequestLimiter:
		var l models.RequestLimiter
		if err := json.Unmarshal(data, &l); err == nil {
			if action == events.ActionDelete {
				s.LimiterIndex.DeleteLimiter(l.ID)
			} else {
				s.LimiterIndex.PutLimiter(&l)
			}
		}
	case events.EntityLimiterBinding:
		var b models.LimiterBinding
		if err := json.Unmarshal(data, &b); err == nil {
			if action == events.ActionDelete {
				s.LimiterIndex.DeleteBinding(b.ID)
			} else {
				s.LimiterIndex.PutBinding(&b)
			}
		}
	case events.EntityUserGroup:
		var g models.UserGroup
		if err := json.Unmarshal(data, &g); err != nil {
			return
		}
		if action == events.ActionDelete {
			s.userGroups.Apply(entitycache.ActionDelete, g.ID, nil)
		} else {
			s.userGroups.Apply(entitycache.ActionSet, g.ID, &g)
		}
	case events.EntityUser:
		var u protocol.SyncedUser
		if err := json.Unmarshal(data, &u); err != nil {
			return
		}
		if action == events.ActionDelete {
			s.users.Apply(entitycache.ActionDelete, u.ID, nil)
		} else {
			s.users.Apply(entitycache.ActionSet, u.ID, &u)
		}
	case events.EntityModelRouting:
		var r models.ModelRouting
		if err := json.Unmarshal(data, &r); err != nil {
			return
		}
		s.applyModelRoutingEvent(action, &r)
	case events.EntityPrivateChannel:
		var payload protocol.PrivateChannelInvalidatePayload
		if err := json.Unmarshal(data, &payload); err != nil {
			return
		}
		s.logger.Info("private channel invalidation received",
			zap.String("action", action),
			zap.Int("affected_users", len(payload.AffectedUserIDs)))
		for _, uid := range payload.AffectedUserIDs {
			s.InvalidateVisiblePrivateChannels(uid)
		}
	case events.EntityPrivateChannelShare:
		var payload protocol.PrivateChannelInvalidatePayload
		if err := json.Unmarshal(data, &payload); err != nil {
			return
		}
		for _, uid := range payload.AffectedUserIDs {
			s.InvalidateVisiblePrivateChannels(uid)
		}
	case events.EntityScript:
		var sc models.AdminScript
		if err := json.Unmarshal(data, &sc); err != nil {
			return
		}
		if action == events.ActionDelete {
			s.scripts.remove(sc.ID)
		} else {
			s.scripts.set(sc)
		}
	}
}

// applyModelRoutingEvent 把 master 推送的 ModelRouting 事件应用到 agent 缓存。
// 全局 routing 直接写入 globalRoutings；用户级 routing 失效该 user 整块 cache，
// 下次 ResolveRouting LRU miss 时由 Loader 重新拉取（避免增量合并的复杂度）。
func (s *Store) applyModelRoutingEvent(action string, r *models.ModelRouting) {
	if r.Scope == models.RoutingScopeGlobal {
		if action == events.ActionDelete {
			s.DeleteGlobalRouting(r.Name)
			s.logger.Info("routing apply",
				zap.String("name", r.Name),
				zap.String("scope", r.Scope),
				zap.String("action", action),
				zap.Int("member_count", 0),
				zap.Uint("user_id", r.UserID),
			)
			return
		}
		// 投影成 protocol.SyncedRouting 写入 cache
		var members []protocol.RoutingMember
		_ = json.Unmarshal([]byte(r.Members), &members)
		s.SetGlobalRouting(r.Name, &protocol.SyncedRouting{
			ID: r.ID, Name: r.Name, Scope: r.Scope, UserID: r.UserID, TokenID: r.TokenID,
			Members: members, Enabled: r.Enabled,
		})
		s.logger.Info("routing apply",
			zap.String("name", r.Name),
			zap.String("scope", r.Scope),
			zap.String("action", action),
			zap.Int("member_count", len(members)),
			zap.Uint("user_id", r.UserID),
		)
		return
	}
	if r.Scope == models.RoutingScopeToken {
		s.InvalidateTokenRoutings(r.TokenID)
	} else {
		s.InvalidateUserRoutings(r.UserID)
	}
	s.logger.Info("routing apply",
		zap.String("name", r.Name),
		zap.String("scope", r.Scope),
		zap.String("action", action),
		zap.Int("member_count", 0), // user 范围以失效 cache 表达，count 不可知
		zap.Uint("user_id", r.UserID),
		zap.Uint("token_id", r.TokenID),
	)
}

// === Routing API ===

// LoadGlobalRoutings 全量替换 globalRoutings 缓存（用于 FullSync）。
// 把 models.ModelRouting 投影成 protocol.SyncedRouting；只加载 enabled=true 的条目。
func (s *Store) LoadGlobalRoutings(items []models.ModelRouting) {
	// 清空现有再写入
	var keys []string
	s.globalRoutings.Range(func(k string, _ *protocol.SyncedRouting) bool {
		keys = append(keys, k)
		return true
	})
	for _, k := range keys {
		s.globalRoutings.Delete(k)
	}
	for i := range items {
		r := &items[i]
		if !r.Enabled {
			continue
		}
		var members []protocol.RoutingMember
		_ = json.Unmarshal([]byte(r.Members), &members)
		s.globalRoutings.Set(r.Name, &protocol.SyncedRouting{
			ID: r.ID, Name: r.Name, Scope: r.Scope, UserID: r.UserID, TokenID: r.TokenID,
			Members: members, Enabled: r.Enabled,
		})
	}
}

// GlobalRoutingCount 返回当前缓存的全局 routing 数（用于 stats / 日志）。
func (s *Store) GlobalRoutingCount() int {
	return s.globalRoutings.Len()
}

// UserRoutingsCount 返回当前 LRU 中缓存的 user-scope routing 块数（每个 user 一块）。
func (s *Store) UserRoutingsCount() int {
	return s.userRoutings.Len()
}

func (s *Store) TokenRoutingsCount() int {
	return s.tokenRoutings.Len()
}

// SetGlobalRouting 写入全局 routing。WS push / FullSync 调用。
func (s *Store) SetGlobalRouting(name string, r *protocol.SyncedRouting) {
	s.globalRoutings.Set(name, r)
}

// DeleteGlobalRouting 删除全局 routing。
func (s *Store) DeleteGlobalRouting(name string) {
	s.globalRoutings.Delete(name)
}

// GetGlobalRouting 返回 enabled 的全局 routing；disabled 或 not found 都返回 nil。
// disabled 等同于运行时"临时移除"——校验层另有一份不过滤 enabled 的查询。
func (s *Store) GetGlobalRouting(ctx context.Context, name string) *protocol.SyncedRouting {
	v, _, err := s.globalRoutings.Get(ctx, name)
	if reason := classifyResolveErr(err); reason != "" {
		s.logResolveDegrade("global_routing", reason, zap.String("name", name))
	}
	if v != nil && v.Enabled {
		return v
	}
	return nil
}

// ListGlobalRoutingNames 返回所有 enabled 全局 routing 名，按字典序排序。
// 用于 /v1/models 暴露 routing 作为可调用 model 名。
func (s *Store) ListGlobalRoutingNames() []string {
	var names []string
	s.globalRoutings.Range(func(k string, v *protocol.SyncedRouting) bool {
		if v != nil && v.Enabled {
			names = append(names, k)
		}
		return true
	})
	sort.Strings(names)
	return names
}

// ListUserRoutingNames 返回当前用户 enabled user-scope routing 名，按字典序排序。
// userID==0 或无 entry 时返回 nil。LRU miss 触发 loader 失败时同样返回 nil。
func (s *Store) ListUserRoutingNames(ctx context.Context, userID uint) []string {
	if userID == 0 {
		return nil
	}
	m, ok, err := s.userRoutings.Get(ctx, userID)
	if reason := classifyResolveErr(err); reason != "" {
		s.logResolveDegrade("user_routing", reason, zap.Uint("user_id", userID))
	}
	if !ok || m == nil {
		return nil
	}
	var names []string
	for name, r := range m.Routings {
		if r != nil && r.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (s *Store) ListTokenRoutingNames(ctx context.Context, tokenID uint) []string {
	if tokenID == 0 {
		return nil
	}
	m, ok, err := s.tokenRoutings.Get(ctx, tokenID)
	if reason := classifyResolveErr(err); reason != "" {
		s.logResolveDegradeFor("token_routing", reason, strconv.FormatUint(uint64(tokenID), 10), zap.Uint("token_id", tokenID))
	}
	if !ok || m == nil {
		return nil
	}
	var names []string
	for name, r := range m.Routings {
		if r != nil && r.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// SetUserRoutings 用整块替换某 user 的全部 user-scope routings。
// WS push 按 user 粒度推送时调用。
func (s *Store) SetUserRoutings(userID uint, routings map[string]*protocol.SyncedRouting) {
	s.userRoutings.Set(userID, &protocol.UserRoutingMap{Routings: routings})
}

// InvalidateUserRoutings 清掉某 user 的整块 cache，下次 ResolveRouting LRU miss
// 时由 Loader 重新拉取（B3.4 接入）。
func (s *Store) InvalidateUserRoutings(userID uint) {
	s.userRoutings.Delete(userID)
}

func (s *Store) SetTokenRoutings(tokenID uint, routings map[string]*protocol.SyncedRouting) {
	s.tokenRoutings.Set(tokenID, &protocol.TokenRoutingMap{Routings: routings})
}

func (s *Store) InvalidateTokenRoutings(tokenID uint) {
	s.tokenRoutings.Delete(tokenID)
}

// ResolveRouting 按 token > user > global 的优先级解析顶层路由。
func (s *Store) ResolveRouting(ctx context.Context, name string, owner protocol.RoutingOwner) *protocol.SyncedRouting {
	if owner.TokenID > 0 {
		m, ok, err := s.tokenRoutings.Get(ctx, owner.TokenID)
		if reason := classifyResolveErr(err); reason != "" {
			s.logResolveDegradeFor("token_routing", reason, strconv.FormatUint(uint64(owner.TokenID), 10), zap.Uint("token_id", owner.TokenID))
		}
		if ok && m != nil {
			if r, has := m.Routings[name]; has && r.Enabled {
				return r
			}
		}
	}
	if owner.UserID > 0 {
		m, ok, err := s.userRoutings.Get(ctx, owner.UserID)
		if reason := classifyResolveErr(err); reason != "" {
			s.logResolveDegrade("user_routing", reason, zap.Uint("user_id", owner.UserID))
		}
		if ok && m != nil {
			if r, has := m.Routings[name]; has && r.Enabled {
				return r
			}
		}
	}
	return s.GetGlobalRouting(ctx, name)
}

// === PrivateChannel API ===

// GetVisiblePrivateChannelsForUser 返回某 user 可见的、enabled 的、对应 model 的
// private channels（未投影成 *models.Channel——投影在 upstream/private_channel_adapter
// 内进行，避免 Store 与 channel 表示层耦合）。
// getVisiblePrivateSet 取某 user 的可见私有通道集;解析降级走统一 logResolveDegrade
// (master_unreachable 等记 Warn,not_found 记 Debug)。
func (s *Store) getVisiblePrivateSet(userID uint) *protocol.VisiblePrivateChannelSet {
	set, _, err := s.visiblePrivateChannels.Get(context.Background(), userID)
	if reason := classifyResolveErr(err); reason != "" {
		s.logResolveDegrade("private_channel_visible", reason, zap.Uint("user_id", userID))
	}
	return set
}

func (s *Store) GetVisiblePrivateChannelsForUser(userID uint, model string) []*protocol.SyncedPrivateChannel {
	if userID == 0 {
		return nil
	}
	set := s.getVisiblePrivateSet(userID)
	if set == nil {
		return nil
	}
	var out []*protocol.SyncedPrivateChannel
	for i := range set.Channels {
		if set.Channels[i].Status != consts.StatusEnabled {
			continue
		}
		if !modelInList(model, set.Channels[i].Models) {
			continue
		}
		out = append(out, &set.Channels[i])
	}
	return out
}

// ListVisibleBYOKModelNamesForUser 返回某 user 全部 enabled BYOK channel 的
// Models 字段并集（去重，保序：channel 内部 Models 原序，跨 channel 先到先得）。
// userID == 0 / 缓存 miss / 无 enabled channel 时返回 nil。
// 复用 visiblePrivateChannels LRU 缓存层，不引入新缓存。
func (s *Store) ListVisibleBYOKModelNamesForUser(userID uint) []string {
	if userID == 0 {
		return nil
	}
	set := s.getVisiblePrivateSet(userID)
	if set == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for i := range set.Channels {
		if set.Channels[i].Status != consts.StatusEnabled {
			continue
		}
		for _, name := range set.Channels[i].Models {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

// InvalidateVisiblePrivateChannels 删该 user 的整块 cache，下次 LRU miss 由 loader
// 重新拉取（与 user_routings 同模式：不增量合并，整块失效）。
// share 表变更 / channel CRUD / user 离开 group 都触发。
func (s *Store) InvalidateVisiblePrivateChannels(userID uint) {
	s.visiblePrivateChannels.Delete(userID)
}

// VisiblePrivateChannelsCount 返回当前 LRU 中缓存的 user-scope private channel 块数。
func (s *Store) VisiblePrivateChannelsCount() int {
	return s.visiblePrivateChannels.Len()
}

// OverrideVisiblePrivateChannels 直接写入 visiblePrivateChannels LRU，
// 绕过 loader——仅供跨包测试用，调用方必须在测试上下文中（testing.Testing()）。
// 非测试上下文调用会 panic，避免生产代码意外污染用户 BYOK 缓存。
func (s *Store) OverrideVisiblePrivateChannels(userID uint, channels []protocol.SyncedPrivateChannel) {
	if !testing.Testing() {
		panic("cache.Store.OverrideVisiblePrivateChannels called outside test context")
	}
	s.visiblePrivateChannels.Set(userID, &protocol.VisiblePrivateChannelSet{
		UserID:   userID,
		Channels: channels,
	})
}

// modelInList 检查 model 是否在切片中（精确匹配；不支持通配符——byok 不参与 model_routing 通配语义）。
func modelInList(model string, list []string) bool {
	return slices.Contains(list, model)
}

// resolveNegativeTTL 把 config 层语义翻译成 LRU 层 TTL:
//
//	0  → 默认 600s
//	<0 → 0（LRU 层 0 即禁用负缓存）
//	>0 → 原值秒
func resolveNegativeTTL(seconds int) time.Duration {
	switch {
	case seconds == 0:
		return 600 * time.Second
	case seconds < 0:
		return 0
	default:
		return time.Duration(seconds) * time.Second
	}
}

// OnChannelChange 注册一个 channel upsert 时的回调。
// old 可能是 nil（首次出现）；new 可能是 nil（删除）。
// 同步调用，回调函数应保持轻量。
func (s *Store) OnChannelChange(fn func(old, new *models.Channel)) {
	s.mu.Lock()
	s.onChannelChange = append(s.onChannelChange, fn)
	s.mu.Unlock()
}

func (s *Store) emitChannelChange(old, new *models.Channel) {
	s.mu.Lock()
	fns := s.onChannelChange
	s.mu.Unlock()
	for _, fn := range fns {
		fn(old, new)
	}
}

// CacheSnapshot 收集每实体/索引的 Stats 用于 heartbeat 上报。
// 单一 provider 列表：实体缓存（LRU/Full）+ 实现 NamedCacheStat 的索引。
// 新增缓存请加进 namedStats() 或让其实现 NamedCacheStat；
// TestCacheSnapshot_Complete 反射兜底拦截遗漏。
func (s *Store) CacheSnapshot() map[string]protocol.CacheEntityStats {
	snap := map[string]protocol.CacheEntityStats{}
	putLRU := func(name string, stats entitycache.Stats) {
		snap[name] = protocol.CacheEntityStats{
			Kind:          "lru",
			Hits:          stats.Hits,
			Misses:        stats.Misses,
			Evictions:     stats.Evictions,
			NegativeHits:  stats.NegativeHits,
			LoadErrors:    stats.LoadErrors,
			Invalidations: stats.Invalidations,
			Size:          stats.Size,
			Capacity:      stats.Capacity,
		}
	}
	putLRU("token", s.tokenStore.PrimaryStats())
	putLRU("user", s.users.Stats())
	putLRU("channel", s.channels.Stats())
	putLRU("model_config", s.modelConfigs.Stats())
	putLRU("agent", s.agents.Stats())
	putLRU("user_group", s.userGroups.Stats())
	putLRU("model_routing", s.globalRoutings.Stats())
	putLRU("user_routings", s.userRoutings.Stats())
	putLRU("token_routings", s.tokenRoutings.Stats())
	putLRU("private_channels_visible", s.visiblePrivateChannels.Stats())
	for _, ncs := range s.namedStats() {
		snap[ncs.CacheName()] = ncs.CacheStat()
	}
	return snap
}

// namedStats 返回所有自描述索引。新增索引在此登记一行（或靠 TestCacheSnapshot_Complete 拦截）。
func (s *Store) namedStats() []NamedCacheStat {
	out := []NamedCacheStat{}
	if s.RouteIndex != nil {
		out = append(out, s.RouteIndex)
	}
	if s.LimiterIndex != nil {
		out = append(out, s.LimiterIndex)
	}
	return out
}
