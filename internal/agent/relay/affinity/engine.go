package affinity

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// ConfigReader 读当前同步配置快照。由 app.AgentCache（cache.Store）结构化满足，
// affinity 借此读 AffinityEnabled / AffinityTTLSec 而不 import app（避免成环）。
type ConfigReader interface {
	Settings() settings.AgentSettings
}

// AffinityStatus values reported in usage logs.
const (
	StatusHit      = "hit"
	StatusFallback = "fallback"
	StatusNone     = "none"
)

// Subject 是 Policy 决策的身份载体。当前只用到 UserID/RealModel；
// 未来分 scope 规则（group/token/channel 的正向/反向配置）在此扩展，不改 Policy 签名。
type Subject struct {
	UserID    uint
	RealModel string
}

// Decision 拆 Record/Apply 两个布尔，为"只记录不应用 / 只应用不记录"等反向配置留口。
type Decision struct {
	Record bool // 是否记录粘性
	Apply  bool // 是否应用粘性（重排置顶）
}

// Policy 决定对某 Subject 是否记录/应用粘性。
type Policy interface {
	Decide(s Subject) Decision
}

// globalPolicy 只看全局开关。
type globalPolicy struct{ cfg ConfigReader }

func (p globalPolicy) Decide(Subject) Decision {
	on := p.cfg != nil && p.cfg.Settings().AffinityEnabled != 0
	return Decision{Record: on, Apply: on}
}

// Engine 是注入到 plan/publish/exec 的门面，组合 Store + Policy + ConfigReader。
type Engine struct {
	store  Store
	policy Policy
	cfg    ConfigReader
}

// New 用全局策略装配 Engine。cfg 提供开关与 TTL。
func New(cfg ConfigReader) *Engine {
	return &Engine{store: newTTLStore(), policy: globalPolicy{cfg: cfg}, cfg: cfg}
}

// Decide 转交策略。
func (e *Engine) Decide(s Subject) Decision { return e.policy.Decide(s) }

// Lookup 查粘性记录。
func (e *Engine) Lookup(k Key) (Entry, bool) { return e.store.Lookup(k) }

// Remember 记录/续期；TTL<=0 视为关闭，不写。
func (e *Engine) Remember(k Key, src state.ChannelSource, sourceID uint) {
	ttl := e.ttl()
	if ttl <= 0 {
		return
	}
	e.store.Remember(k, Entry{Source: src, SourceID: sourceID, ExpiresAt: time.Now().Add(ttl)})
}

// Forget 剔除（粘性 channel 硬失败时调用）。
func (e *Engine) Forget(k Key) { e.store.Forget(k) }

func (e *Engine) ttl() time.Duration {
	if e.cfg == nil {
		return 0
	}
	return time.Duration(e.cfg.Settings().AffinityTTLSec) * time.Second
}
