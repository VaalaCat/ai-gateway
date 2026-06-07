package cache

import (
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// candidate 是"一条 limiter + 它被哪种绑定命中"，targetType 用于就近覆盖的精细度比较。
// 注意：渠道来源用 string("admin"/"private")而非 state.ChannelSource，避免 cache→relay/state 依赖，
// 也让 app.AgentCache 接口能直接声明 Effective* 方法（app 不能 import relay/state）。
type candidate struct {
	limiter    *models.RequestLimiter
	targetType string
}

// LimiterIndex 持有同步下发的 limiters + bindings，重建候选索引，
// 并按 spec §6.1 就近覆盖解析出每个 (Metric,KeyBy) 维度的生效 limiter。
// 放在 cache 包（同 RouteIndex），由 Store 持有；relay 侧通过接口读取，避免循环依赖。
type LimiterIndex struct {
	mu       sync.RWMutex
	limiters map[uint]*models.RequestLimiter
	bindings map[uint]*models.LimiterBinding

	global    []candidate
	byGroup   map[uint][]candidate
	byUser    map[uint][]candidate
	byChannel map[uint][]candidate // channel 绑定按 admin Channel.ID
}

func NewLimiterIndex() *LimiterIndex {
	return &LimiterIndex{
		limiters: map[uint]*models.RequestLimiter{},
		bindings: map[uint]*models.LimiterBinding{},
	}
}

func (li *LimiterIndex) LoadLimiters(ls []models.RequestLimiter) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.limiters = make(map[uint]*models.RequestLimiter, len(ls))
	for i := range ls {
		l := ls[i]
		li.limiters[l.ID] = &l
	}
	li.rebuild()
}

func (li *LimiterIndex) LoadBindings(bs []models.LimiterBinding) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.bindings = make(map[uint]*models.LimiterBinding, len(bs))
	for i := range bs {
		b := bs[i]
		li.bindings[b.ID] = &b
	}
	li.rebuild()
}

func (li *LimiterIndex) PutLimiter(l *models.RequestLimiter) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.limiters[l.ID] = l
	li.rebuild()
}

func (li *LimiterIndex) DeleteLimiter(id uint) {
	li.mu.Lock()
	defer li.mu.Unlock()
	delete(li.limiters, id)
	li.rebuild()
}

func (li *LimiterIndex) PutBinding(b *models.LimiterBinding) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.bindings[b.ID] = b
	li.rebuild()
}

func (li *LimiterIndex) DeleteBinding(id uint) {
	li.mu.Lock()
	defer li.mu.Unlock()
	delete(li.bindings, id)
	li.rebuild()
}

// rebuild 从 bindings+limiters 重建候选桶。调用方持锁。
func (li *LimiterIndex) rebuild() {
	li.global = nil
	li.byGroup = map[uint][]candidate{}
	li.byUser = map[uint][]candidate{}
	li.byChannel = map[uint][]candidate{}
	for _, b := range li.bindings {
		if !b.Enabled {
			continue
		}
		l, ok := li.limiters[b.LimiterID]
		if !ok || !l.Enabled {
			continue
		}
		c := candidate{limiter: l, targetType: b.TargetType}
		switch b.TargetType {
		case models.LimiterTargetGlobal:
			li.global = append(li.global, c)
		case models.LimiterTargetUserGroup:
			li.byGroup[b.TargetID] = append(li.byGroup[b.TargetID], c)
		case models.LimiterTargetUser:
			li.byUser[b.TargetID] = append(li.byUser[b.TargetID], c)
		case models.LimiterTargetChannel:
			li.byChannel[b.TargetID] = append(li.byChannel[b.TargetID], c)
		}
	}
}

// EffectiveRequestLimiters 返回请求级（KeyBy 不依赖渠道）的生效 limiter。
func (li *LimiterIndex) EffectiveRequestLimiters(userID, groupID uint) []*models.RequestLimiter {
	li.mu.RLock()
	defer li.mu.RUnlock()
	cands := make([]candidate, 0, len(li.global))
	cands = append(cands, li.global...)
	cands = append(cands, li.byGroup[groupID]...)
	cands = append(cands, li.byUser[userID]...)
	return resolve(cands, func(l *models.RequestLimiter) bool { return !l.ChannelKeyed() })
}

// EffectiveAttemptLimiters 返回尝试级（KeyBy 依赖渠道）的生效 limiter，含 ChannelScope 过滤。
// src 是渠道来源字符串："admin" | "private"（等于 string(state.SourceAdmin/SourcePrivate)）。
func (li *LimiterIndex) EffectiveAttemptLimiters(userID, groupID uint, src string, channelID uint) []*models.RequestLimiter {
	li.mu.RLock()
	defer li.mu.RUnlock()
	cands := make([]candidate, 0, len(li.global))
	cands = append(cands, li.global...)
	cands = append(cands, li.byChannel[channelID]...)
	return resolve(cands, func(l *models.RequestLimiter) bool {
		return l.ChannelKeyed() && scopeMatches(l.ChannelScope, src)
	})
}

func scopeMatches(scope, src string) bool {
	switch scope {
	case models.LimiterScopeAll:
		return true
	case models.LimiterScopePrivate:
		return src == "private"
	default: // admin（含空值兜底）
		return src == "admin"
	}
}

// resolve 实现 §6.1：按 (Metric,KeyBy) 维度分组，每组取绑定最具体的一条。
func resolve(cands []candidate, keep func(*models.RequestLimiter) bool) []*models.RequestLimiter {
	type dim struct{ metric, keyBy string }
	best := map[dim]candidate{}
	for _, c := range cands {
		if !c.limiter.Enabled || !keep(c.limiter) {
			continue
		}
		d := dim{c.limiter.Metric, c.limiter.KeyBy}
		if cur, ok := best[d]; !ok || moreSpecific(c, cur) {
			best[d] = c
		}
	}
	out := make([]*models.RequestLimiter, 0, len(best))
	for _, c := range best {
		out = append(out, c.limiter)
	}
	return out
}

func moreSpecific(a, b candidate) bool {
	sa, sb := specificity(a.targetType), specificity(b.targetType)
	if sa != sb {
		return sa > sb
	}
	if a.limiter.Priority != b.limiter.Priority {
		return a.limiter.Priority > b.limiter.Priority
	}
	return a.limiter.ID < b.limiter.ID
}

// CacheName / CacheStat 实现 NamedCacheStat：报限流器/绑定条数（持锁读）。
func (li *LimiterIndex) CacheName() string { return "limiter_index" }

func (li *LimiterIndex) CacheStat() protocol.CacheEntityStats {
	li.mu.RLock()
	defer li.mu.RUnlock()
	nl, nb := len(li.limiters), len(li.bindings)
	return protocol.CacheEntityStats{
		Kind:  "index",
		Size:  nl,
		Extra: map[string]int64{"limiters": int64(nl), "bindings": int64(nb)},
	}
}

// Limiter 按 ID 返回限流器副本（快照/展示用）。不存在返回 nil。持锁读。
func (li *LimiterIndex) Limiter(id uint) *models.RequestLimiter {
	li.mu.RLock()
	defer li.mu.RUnlock()
	if l, ok := li.limiters[id]; ok {
		cp := *l
		return &cp
	}
	return nil
}

func specificity(targetType string) int {
	switch targetType {
	case models.LimiterTargetUser:
		return 3
	case models.LimiterTargetChannel:
		return 2
	case models.LimiterTargetUserGroup:
		return 1
	default:
		return 0 // global
	}
}
