package limiter

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// Resolver 提供已就近覆盖解析的生效 limiter（由 app.AgentCache / cache.Store 满足）。
type Resolver interface {
	EffectiveRequestLimiters(userID, groupID uint) []*models.RequestLimiter
	EffectiveAttemptLimiters(userID, groupID uint, src string, channelID uint) []*models.RequestLimiter
}

// Gate 是两段式限流闸门，实现 state.RateGate。支持 reject 与 wait（排队）。
type Gate struct {
	Resolver Resolver
	Store    PermitStore
	Settings SettingsReader // 可为 nil：nil 时用硬编码默认(queue_time 120s, keepalive 15s)
}

func NewGate(r Resolver, store PermitStore) *Gate {
	return &Gate{Resolver: r, Store: store}
}

// SettingsReader 读全局限流设置（由 app.AgentCache 满足）。
type SettingsReader interface {
	Settings() settings.AgentSettings
}

const pollInterval = 50 * time.Millisecond

type identity struct {
	userID  uint
	groupID uint
}

func identityOf(rctx *state.RelayContext) identity {
	if rctx.Input.UserInfo != nil {
		return identity{userID: rctx.Input.UserInfo.UserID, groupID: rctx.Input.UserInfo.GroupID}
	}
	return identity{}
}

type channelRef struct {
	source string
	id     uint
}

func refOf(a state.Attempt) channelRef {
	id := a.SourceID
	if id == 0 && a.Channel != nil {
		id = a.Channel.ID
	}
	return channelRef{source: string(a.Source), id: id}
}

func (g *Gate) AcquireRequest(rctx *state.RelayContext) (state.RateLease, error) {
	if g.disabled() {
		return &lease{}, nil
	}
	id := identityOf(rctx)
	ls := g.Resolver.EffectiveRequestLimiters(id.userID, id.groupID)
	return g.acquire(rctx, ls, id, channelRef{})
}

func (g *Gate) AcquireAttempt(rctx *state.RelayContext, a state.Attempt) (state.RateLease, error) {
	if g.disabled() {
		return &lease{}, nil
	}
	id := identityOf(rctx)
	ref := refOf(a)
	ls := g.Resolver.EffectiveAttemptLimiters(id.userID, id.groupID, ref.source, ref.id)
	return g.acquire(rctx, ls, id, ref)
}

// acquire：reject 优先排序 → 全占或全不占 → wait 循环（min queue_time、队列满即拒、ctx 取消）。
func (g *Gate) acquire(rctx *state.RelayContext, ls []*models.RequestLimiter, id identity, ref channelRef) (state.RateLease, error) {
	if len(ls) == 0 {
		return &lease{}, nil
	}
	sortRejectFirst(ls) // reject 优先：满的 reject 排前面，会先失败 → 立即拒
	held, failed := g.tryAcquireAll(ls, id, ref)
	if failed == nil {
		return held, nil
	}
	if failed.Action != models.LimiterActionWait {
		recordHit(rctx, "rejected", reasonOf(failed), 0, failed, bucketOf(failed, id, ref))
		return nil, state.ErrRateLimited
	}
	return g.acquireWaiting(rctx, ls, id, ref)
}

// acquireWaiting 在 min(queue_time) 截止前轮询重试；队列满或 reject 命中即拒，ctx 取消即中止。
func (g *Gate) acquireWaiting(rctx *state.RelayContext, ls []*models.RequestLimiter, id identity, ref channelRef) (state.RateLease, error) {
	start := time.Now()
	deadline := start.Add(g.minQueueTime(ls))
	ctx := requestCtx(rctx)
	var guard *streamGuard
	defer func() {
		if guard != nil {
			guard.stopKeepalive() // 拿到名额/失败前停保活并 join，再交给 backend 写
		}
	}()
	defer func() {
		if rctx.Inflight != nil {
			rctx.Inflight.Unqueue() // 任何出口都清排队标记（拿到名额/拒/断连/超时）
		}
	}()
	waited := false
	var lastWaited *models.RequestLimiter // 最近一次排队命中的 limiter，用于排队超时出口记录决策
	for time.Now().Before(deadline) {
		held, failed := g.tryAcquireAll(ls, id, ref)
		if failed == nil {
			if waited {
				recordHit(rctx, "queued", "", int(time.Since(start)/time.Millisecond), nil, "")
			}
			return held, nil
		}
		if failed.Action != models.LimiterActionWait {
			recordHit(rctx, "rejected", reasonOf(failed), 0, failed, bucketOf(failed, id, ref))
			return nil, state.ErrRateLimited
		}
		key := BucketKey{LimiterID: failed.ID, Bucket: bucketOf(failed, id, ref)}
		if !g.Store.AddWaiter(key, failed.QueueSize) {
			recordHit(rctx, "rejected", reasonOf(failed), 0, failed, key.Bucket)
			return nil, state.ErrRateLimited // 队列满
		}
		waited = true
		lastWaited = failed
		if rctx.Inflight != nil {
			rctx.Inflight.SetStage("ratelimit_wait")
			rctx.Inflight.MarkQueued(reasonOf(failed))
		}
		if guard == nil && rctx.Input.IsStream {
			guard = newStreamGuard(rctx, g.keepaliveDefault())
		}
		if guard != nil {
			guard.ensureOpen()
			guard.startKeepalive()
		}
		g.waitTick(ctx, failed.Metric, key, deadline)
		g.Store.RemoveWaiter(key)
		if ctx.Err() != nil {
			return nil, ctx.Err() // 客户端断连
		}
	}
	// 排队超时：记录 rejected 决策，等待时长计入 wait_ms，避免该 429 在 usage_log 隐身。
	if lastWaited != nil {
		recordHit(rctx, "rejected", reasonOf(lastWaited), int(time.Since(start)/time.Millisecond), lastWaited, bucketOf(lastWaited, id, ref))
	}
	return nil, state.ErrRateLimited // 排队超时
}

func sortRejectFirst(ls []*models.RequestLimiter) {
	sort.SliceStable(ls, func(i, j int) bool {
		return ls[i].Action == models.LimiterActionReject && ls[j].Action != models.LimiterActionReject
	})
}

func (g *Gate) waitTick(ctx context.Context, metric string, key BucketKey, deadline time.Time) {
	var freed <-chan struct{}
	if metric == models.LimiterMetricConcurrency {
		freed = g.Store.WaitC(key)
	}
	d := pollInterval
	if rem := time.Until(deadline); rem < d {
		d = rem
	}
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-freed:
	case <-timer.C:
	}
}

func (g *Gate) minQueueTime(ls []*models.RequestLimiter) time.Duration {
	min := 0
	for _, l := range ls {
		if l.Action != models.LimiterActionWait {
			continue
		}
		qt := l.QueueTimeMs
		if qt <= 0 {
			qt = g.queueTimeDefault()
		}
		if min == 0 || qt < min {
			min = qt
		}
	}
	if min == 0 {
		min = g.queueTimeDefault()
	}
	return time.Duration(min) * time.Millisecond
}

func (g *Gate) disabled() bool {
	return g.Settings != nil && g.Settings.Settings().RateLimiterEnabled == 0
}

func (g *Gate) queueTimeDefault() int {
	if g.Settings != nil {
		if v := g.Settings.Settings().QueueTimeMs; v > 0 {
			return v
		}
	}
	return 120000
}

func (g *Gate) keepaliveDefault() int {
	if g.Settings != nil {
		if v := g.Settings.Settings().SSEKeepaliveMs; v > 0 {
			return v
		}
	}
	return 15000
}

func requestCtx(rctx *state.RelayContext) context.Context {
	if rctx.Context != nil && rctx.Request != nil {
		return rctx.Request.Context()
	}
	return context.Background()
}

// tryAcquireAll 试占全部；成功 (lease,nil)；任一失败退回已占，返回 (nil, 失败的 limiter)。
func (g *Gate) tryAcquireAll(ls []*models.RequestLimiter, id identity, ref channelRef) (*lease, *models.RequestLimiter) {
	held := &lease{}
	for _, lim := range ls {
		key := BucketKey{LimiterID: lim.ID, Bucket: bucketOf(lim, id, ref)}
		rel, ok := g.tryOne(lim, key)
		if !ok {
			held.Release()
			return nil, lim
		}
		if rel != nil {
			held.releases = append(held.releases, rel)
		}
	}
	return held, nil
}

func (g *Gate) tryOne(lim *models.RequestLimiter, key BucketKey) (func(), bool) {
	switch lim.Metric {
	case models.LimiterMetricConcurrency:
		return g.Store.TryConcurrency(key, lim.Capacity)
	case models.LimiterMetricRate:
		return nil, g.Store.TryRate(key, lim.Capacity, lim.WindowMs)
	}
	return nil, true // 未知 metric：放行
}

func bucketOf(lim *models.RequestLimiter, id identity, ref channelRef) string {
	switch lim.KeyBy {
	case models.LimiterKeyPerUser:
		return "u:" + utoa(id.userID)
	case models.LimiterKeyPerGroup:
		return "g:" + utoa(id.groupID)
	case models.LimiterKeyPerChannel:
		return "c:" + ref.source + ":" + utoa(ref.id)
	case models.LimiterKeyPerChannelUser:
		return "cu:" + ref.source + ":" + utoa(ref.id) + ":" + utoa(id.userID)
	default: // shared
		return "shared"
	}
}

func utoa(u uint) string { return strconv.FormatUint(uint64(u), 10) }

// lease 聚合多条已占名额的 release。实现 state.RateLease。
type lease struct {
	releases []func()
}

func (l *lease) Release() {
	for _, r := range l.releases {
		r()
	}
}

// recordHit 把一条限流决策累积到 rctx.State.RateLimit，供 publish 落 usage_log。
// rctx / State 为 nil 时直接跳过（limiter 单测用无 State 的 rctx，不能 panic）。
func recordHit(rctx *state.RelayContext, decision, reason string, waitMs int, lim *models.RequestLimiter, bucket string) {
	if rctx == nil || rctx.State == nil {
		return
	}
	if rctx.State.RateLimit == nil {
		rctx.State.RateLimit = &state.RateLimitRecord{}
	}
	r := rctx.State.RateLimit
	if decisionRank(decision) > decisionRank(r.Decision) {
		r.Decision = decision
		if reason != "" {
			r.Reason = reason
		}
	}
	r.WaitMs += waitMs
	if lim != nil {
		r.Hits = append(r.Hits, models.RateLimitHit{
			LimiterID: lim.ID, Name: lim.Name,
			Dimension: lim.Metric + "/" + lim.KeyBy,
			Bucket:    bucket,
			Decision:  decision, WaitMs: waitMs,
		})
	}
}

// decisionRank 给决策定严苛度，取最严：rejected > queued > allow。
func decisionRank(d string) int {
	switch d {
	case "rejected":
		return 3
	case "queued":
		return 2
	case "allow":
		return 1
	}
	return 0
}

// reasonOf 给一条命中的 limiter 生成人话原因。
func reasonOf(lim *models.RequestLimiter) string {
	return fmt.Sprintf("%s(%s) over capacity %d", lim.Name, lim.Metric+"/"+lim.KeyBy, lim.Capacity)
}
