package cache

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
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
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

var _ app.Store = (*Store)(nil)

type Store struct {
	tokenStore     *tokenStore
	users          entitycache.EntityCache[uint, *protocol.SyncedUser]
	channels       entitycache.EntityCache[uint, *models.Channel]
	modelConfigs   entitycache.EntityCache[string, *models.ModelConfig]
	agents         entitycache.EntityCache[string, *models.Agent]
	userGroups     entitycache.EntityCache[uint, *models.UserGroup]
	modelChannels  utils.SyncMap[string, []*models.Channel]
	globalRoutings        entitycache.EntityCache[string, *protocol.SyncedRouting]
	userRoutings          entitycache.EntityCache[uint, *protocol.UserRoutingMap]
	visiblePrivateChannels entitycache.EntityCache[uint, *protocol.VisiblePrivateChannelSet]

	RouteIndex *RouteIndex

	version atomic.Int64
	mu      sync.Mutex // protects index rebuild

	// settings 持有 master 同步过来的全局配置快照。
	// 读路径走 atomic.Load(无锁,hot path);写路径(applySetting)用 settingsMu
	// 串行化 read-modify-write,防止 event bus 并发 handler 丢失 update。
	settingsMu sync.Mutex
	settings   atomic.Pointer[settings.AgentSettings]

	logger *zap.Logger

	onChannelChange []func(old, new *models.Channel)
}

// NewStore 装配 agent 端缓存 Store。
// client 用于 LRU 实体的 miss 拉取（可为 nil；nil 时 LRU 实体只读缓存）。
// cfg 决定 LRU 容量与负缓存 TTL；零值/非法值由 normalize 兜底为默认。
//
// 选择性 LRU：tokens / users 走 LRU；channels / modelConfigs / agents / userGroups
// 仍是 admin 维护的小规模实体，走 FullCache。
func NewStore(client app.WSClient, cfg config.AgentCacheConfig) *Store {
	s := &Store{
		channels:     entitycache.NewFullCache[uint, *models.Channel](),
		modelConfigs: entitycache.NewFullCache[string, *models.ModelConfig](),
		agents:       entitycache.NewFullCache[string, *models.Agent](),
		userGroups:   entitycache.NewFullCache[uint, *models.UserGroup](),
		RouteIndex:   NewRouteIndex(),
	}
	{
		snap := settings.AgentSettings{}
		for k, v := range settings.Defaults() {
			_ = settings.Apply(&snap, k, v) // 默认值不会越界,error 安全忽略
		}
		s.settings.Store(&snap)
	}
	s.logger = zap.NewNop()

	negTTLSec := cfg.NegativeTTLSeconds
	if negTTLSec <= 0 {
		negTTLSec = 30
	}
	negTTL := time.Duration(negTTLSec) * time.Second

	tokenCap := cfg.TokenCapacity
	if tokenCap <= 0 {
		tokenCap = 20000
	}
	userCap := cfg.UserCapacity
	if userCap <= 0 {
		userCap = 20000
	}

	users, err := newUserLRU(client, userCap, negTTL)
	if err != nil {
		panic(err)
	}
	s.users = users
	s.tokenStore = newTokenStoreLRU(client, s.users, tokenCap, negTTL)

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
	})
	if err != nil {
		panic(err)
	}
	s.userRoutings = userRoutings

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
		})
	if err != nil {
		panic(err)
	}
	s.visiblePrivateChannels = privateChans

	return s
}

// SetLogger 注入 zap.Logger，用于 routing apply / resolve 等可观测性日志。
// 默认 NewStore 使用 zap.NewNop()；server 装配时调用以接入实际 logger。
func (s *Store) SetLogger(l *zap.Logger) {
	if l == nil {
		l = zap.NewNop()
	}
	s.logger = l
}

func newUserLRU(client app.WSClient, capacity int, negTTL time.Duration) (entitycache.EntityCache[uint, *protocol.SyncedUser], error) {
	var loader entitycache.Loader[uint, *protocol.SyncedUser]
	if client != nil {
		loader = &loaders.UserLoader{Client: client}
	}
	return entitycache.NewLRUCache(entitycache.Config[uint, *protocol.SyncedUser]{
		Capacity:    capacity,
		Loader:      loader,
		NegativeTTL: negTTL,
	})
}

func newTokenStoreLRU(client app.WSClient, users entitycache.EntityCache[uint, *protocol.SyncedUser], capacity int, negTTL time.Duration) *tokenStore {
	ts := &tokenStore{}
	var loader entitycache.Loader[string, *models.Token]
	if client != nil {
		loader = &loaders.TokenLoader{Client: client, Users: users}
	}
	primary, err := entitycache.NewLRUCache(entitycache.Config[string, *models.Token]{
		Capacity:    capacity,
		Loader:      loader,
		NegativeTTL: negTTL,
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

// === Token API ===

func (s *Store) GetToken(ctx context.Context, key string) *models.Token {
	t, _, _ := s.tokenStore.Get(ctx, key)
	return t
}

func (s *Store) SetToken(token *models.Token) {
	s.tokenStore.Set(token)
}

func (s *Store) DeleteToken(key string) {
	s.tokenStore.Delete(key)
}

func (s *Store) GetTokenByID(ctx context.Context, id uint) *models.Token {
	t, _, _ := s.tokenStore.GetByID(ctx, id)
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
func (s *Store) DeleteUser(id uint)              { s.users.Delete(id) }
func (s *Store) UserCount() int                  { return s.users.Len() }

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
	return v
}
func (s *Store) SetAgent(agent *models.Agent) { s.agents.Set(agent.AgentID, agent) }
func (s *Store) DeleteAgent(agentID string)   { s.agents.Delete(agentID) }
func (s *Store) AgentCount() int              { return s.agents.Len() }

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
	for i := range agents {
		s.agents.Set(agents[i].AgentID, &agents[i])
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

// === Model Index (派生) ===

func (s *Store) GetChannelsForModel(model string) []*models.Channel {
	v, ok := s.modelChannels.Load(model)
	if !ok {
		return nil
	}
	return v
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

// === Agent helpers ===

// UpdateAgentAutoAddresses updates in-memory auto-detected addresses for an
// agent without overriding manually configured addresses.
func (s *Store) UpdateAgentAutoAddresses(agentID string, addrs []agentproxy.Address) {
	current, ok := s.agents.Peek(agentID)
	if !ok || current == nil {
		return
	}
	if hasManualAgentAddresses(current.HTTPAddresses) {
		return
	}

	next := *current
	if len(addrs) == 0 {
		next.HTTPAddresses = ""
	} else {
		addrJSON, err := json.Marshal(addrs)
		if err != nil {
			return
		}
		next.HTTPAddresses = string(addrJSON)
	}
	s.agents.Set(next.AgentID, &next)
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
				result = append(result, agent)
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
		result = append(result, agent)
		return true
	})
	return result
}

func hasManualAgentAddresses(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return false
	}
	addrs := agentproxy.ParseAddresses(raw)
	if len(addrs) == 0 {
		// Unknown format: keep existing value to avoid accidental override.
		return true
	}
	for _, addr := range addrs {
		if strings.TrimSpace(addr.Tag) != "auto-detected" {
			return true
		}
	}
	return false
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
			s.agents.Apply(entitycache.ActionDelete, agent.AgentID, nil)
		} else {
			s.agents.Apply(entitycache.ActionSet, agent.AgentID, &agent)
		}
	case events.EntityAgentRoute:
		var route models.AgentRoute
		if err := json.Unmarshal(data, &route); err == nil {
			if action == events.ActionDelete {
				s.RouteIndex.Delete(route.ID)
			} else {
				s.RouteIndex.Put(&route)
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
			ID: r.ID, Name: r.Name, Scope: r.Scope, UserID: r.UserID,
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
	// scope=user：失效该 user 的整块 cache
	s.InvalidateUserRoutings(r.UserID)
	s.logger.Info("routing apply",
		zap.String("name", r.Name),
		zap.String("scope", r.Scope),
		zap.String("action", action),
		zap.Int("member_count", 0), // user 范围以失效 cache 表达，count 不可知
		zap.Uint("user_id", r.UserID),
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
			ID: r.ID, Name: r.Name, Scope: r.Scope, UserID: r.UserID,
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
func (s *Store) GetGlobalRouting(name string) *protocol.SyncedRouting {
	v, _, _ := s.globalRoutings.Get(context.Background(), name)
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
func (s *Store) ListUserRoutingNames(userID uint) []string {
	if userID == 0 {
		return nil
	}
	m, ok, _ := s.userRoutings.Get(context.Background(), userID)
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

// ResolveRouting 按 spec §1.4 优先级解析：用户 routing > 全局 routing > nil（让上层当真实 model 处理）。
func (s *Store) ResolveRouting(name string, userID uint) *protocol.SyncedRouting {
	if userID > 0 {
		if m, ok, _ := s.userRoutings.Get(context.Background(), userID); ok && m != nil {
			if r, has := m.Routings[name]; has && r.Enabled {
				return r
			}
		}
	}
	return s.GetGlobalRouting(name)
}

// === PrivateChannel API ===

// GetVisiblePrivateChannelsForUser 返回某 user 可见的、enabled 的、对应 model 的
// private channels（未投影成 *models.Channel——投影在 upstream/private_channel_adapter
// 内进行，避免 Store 与 channel 表示层耦合）。
func (s *Store) GetVisiblePrivateChannelsForUser(userID uint, model string) []*protocol.SyncedPrivateChannel {
	if userID == 0 {
		return nil
	}
	// 用 Peek 区分 hit/miss：Peek 命中说明本地 LRU 已有；否则 Get 会触发 loader。
	// 负缓存条目对 Peek 表现为 miss，与"需要外部拉取"语义对齐。
	_, localHit := s.visiblePrivateChannels.Peek(userID)
	if localHit {
		metrics.BYOKVisibleSetCacheHit.Inc()
	} else {
		metrics.BYOKVisibleSetCacheMiss.Inc()
	}
	set, _, _ := s.visiblePrivateChannels.Get(context.Background(), userID)
	// 写入或负缓存后，size 可能变化；Get 内部 Add 不暴露事件，这里 Set 当前 Len。
	metrics.BYOKVisibleSetCacheSize.Set(float64(s.visiblePrivateChannels.Len()))
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
	set, _, _ := s.visiblePrivateChannels.Get(context.Background(), userID)
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
	metrics.BYOKVisibleSetCacheSize.Set(float64(s.visiblePrivateChannels.Len()))
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

// CacheSnapshot 收集每实体的 Stats 用于 heartbeat 上报。
// LRU 模式实体含完整字段；Full 模式实体仅 Size 有意义、其他字段为 0。
func (s *Store) CacheSnapshot() map[string]protocol.CacheEntityStats {
	snap := map[string]protocol.CacheEntityStats{}
	put := func(name string, stats entitycache.Stats) {
		snap[name] = protocol.CacheEntityStats{
			Hits:         stats.Hits,
			Misses:       stats.Misses,
			Evictions:    stats.Evictions,
			NegativeHits: stats.NegativeHits,
			Size:         stats.Size,
			Capacity:     stats.Capacity,
		}
	}
	put("token", s.tokenStore.PrimaryStats())
	put("user", s.users.Stats())
	put("channel", s.channels.Stats())
	put("model_config", s.modelConfigs.Stats())
	put("agent", s.agents.Stats())
	put("user_group", s.userGroups.Stats())
	put("model_routing", s.globalRoutings.Stats())
	put("user_routings", s.userRoutings.Stats())
	put("private_channels_visible", s.visiblePrivateChannels.Stats())
	return snap
}
