package billing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

// evaluateLimit 判定一个渠道当前是否应被禁用。OR 语义:cutoff 优先,其后逐条 rule,命中即停。
// usageFn 返回该 rule 对应指标在其窗口内的当前用量。reason 为机读 kind:
// "cutoff" 或 "<metric>/<window>"。autoRecover = 周期窗口(非 lifetime/cutoff)。
func evaluateLimit(limit models.ChannelLimit, now time.Time, usageFn func(models.LimitRule) (int64, error)) (shouldDisable bool, reason string, autoRecover bool, err error) {
	if limit.DisableAt > 0 && now.Unix() >= limit.DisableAt {
		return true, "cutoff", false, nil
	}
	for _, r := range limit.Rules {
		v, e := usageFn(r)
		if e != nil {
			return false, "", false, e
		}
		if v >= r.Threshold {
			return true, r.Metric + "/" + r.Window, r.Window != models.LimitWindowLifetime, nil
		}
	}
	return false, "", false, nil
}

// metricValue 按规则的 metric / cost_basis 从窗口用量取对应数值。
// cost: cost_basis=="billed" 取折后实付额度,否则(raw/空)取折前原价;calls: 取请求次数。
func metricValue(r models.LimitRule, u dao.ChannelUsage) int64 {
	if r.Metric == models.LimitMetricCost {
		if r.CostBasis == models.CostBasisBilled {
			return u.BilledCost
		}
		return u.RawCost
	}
	return u.Calls
}

const (
	statusEnabled  = 1
	statusDisabled = 0
)

// reconcile 依当前 status + state 与 shouldDisable 判定,算出目标 status / state / 是否需要落库。
// 区分手动/自动靠 state.Tripped:评估器禁用才置 true;手动禁用为 false,评估器永不自动复活。
func reconcile(status int, state models.ChannelLimitState, shouldDisable bool, reason string, autoRecover bool, now int64) (int, models.ChannelLimitState, bool) {
	if shouldDisable {
		if status == statusEnabled {
			return statusDisabled, models.ChannelLimitState{
				Tripped: true, Reason: reason, AutoRecover: autoRecover, TrippedAt: now,
			}, true
		}
		// 已禁用:若是评估器自己禁的(Tripped),按需更新 reason/autoRecover;手动禁的(无 Tripped)不碰。
		if state.Tripped && (state.Reason != reason || state.AutoRecover != autoRecover) {
			next := state
			next.Reason = reason
			next.AutoRecover = autoRecover
			return status, next, true
		}
		return status, state, false
	}
	// 不该禁用
	if status == statusDisabled {
		if state.Tripped && state.AutoRecover {
			return statusEnabled, models.ChannelLimitState{}, true // 周期窗口自动恢复
		}
		return status, state, false // 永久 trip 或手动禁:保持
	}
	// status == enabled
	if state.Tripped {
		return statusEnabled, models.ChannelLimitState{}, true // 清残留 state
	}
	return status, state, false
}

// LimitEvaluator 周期评估所有配了限额的 admin channel,翻 Status + 写 LimitState。
type LimitEvaluator struct {
	App           dao.AppProvider
	Bus           app.EventBus
	Logger        *zap.Logger
	interval      time.Duration
	stopCh        chan struct{}
	lifecycleMu   sync.Mutex
	started       bool
	closing       bool
	closeOnce     sync.Once
	workerCancel  context.CancelCauseFunc
	workers       conc.WaitGroup
	done          chan struct{}
	tick          func(context.Context, time.Time) error
	activeWorkers atomic.Int64
	activeTimers  atomic.Int64
	inflight      atomic.Int64
}

func NewLimitEvaluator(application dao.AppProvider, bus app.EventBus, logger *zap.Logger, interval time.Duration) *LimitEvaluator {
	e := &LimitEvaluator{App: application, Bus: bus, Logger: logger, interval: interval, stopCh: make(chan struct{}), done: make(chan struct{})}
	e.tick = e.TickContext
	return e
}

// Start 起后台 ticker;每 tick 跑一轮 Tick(now)。
func (e *LimitEvaluator) Start() {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if e.started || e.closing {
		return
	}
	e.started = true
	if e.interval <= 0 {
		e.interval = 30 * time.Second
	}
	workerCtx, cancel := context.WithCancelCause(context.Background())
	e.workerCancel = cancel
	e.activeWorkers.Add(1)
	e.activeTimers.Add(1)
	e.workers.Go(func() {
		defer e.activeWorkers.Add(-1)
		defer e.activeTimers.Add(-1)
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if workerCtx.Err() != nil {
					return
				}
				e.inflight.Add(1)
				err := e.tick(workerCtx, time.Now().UTC())
				e.inflight.Add(-1)
				if workerCtx.Err() != nil {
					return
				}
				if err != nil && e.Logger != nil {
					e.Logger.Error("channel_limit_tick_failed", zap.Error(err))
				}
			case <-e.stopCh:
				return
			case <-workerCtx.Done():
				return
			}
		}
	})
}

func (e *LimitEvaluator) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("limit evaluator: nil close context")
	}
	e.closeOnce.Do(func() {
		e.lifecycleMu.Lock()
		e.closing = true
		if e.workerCancel != nil {
			e.workerCancel(context.Cause(ctx))
		}
		close(e.stopCh)
		e.lifecycleMu.Unlock()
		go func() {
			e.workers.Wait()
			close(e.done)
		}()
	})
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (e *LimitEvaluator) Stop(ctx context.Context) error { return e.Close(ctx) }

func (e *LimitEvaluator) Done() <-chan struct{} { return e.done }

func (e *LimitEvaluator) ResourceCounts() app.ResourceCounts {
	return app.ResourceCounts{LifecycleWorkers: e.activeWorkers.Load(), Timers: e.activeTimers.Load(), Inflight: e.inflight.Load()}
}

// Tick 跑一轮评估。now 应为 UTC。
func (e *LimitEvaluator) Tick(now time.Time) error {
	return e.TickContext(context.Background(), now)
}

func (e *LimitEvaluator) TickContext(ctx context.Context, now time.Time) error {
	daoCtx := dao.NewContextWithContext(e.App, ctx)
	q := dao.NewAdminQuery(daoCtx).Channel()
	m := dao.NewAdminMutation(daoCtx).Channel()

	channels, err := q.ListAll()
	if err != nil {
		return err
	}
	for i := range channels {
		ch := channels[i]
		limit := ch.Limit.Data()
		if limit.DisableAt == 0 && len(limit.Rules) == 0 {
			continue // 未配限额
		}
		usageFn := func(r models.LimitRule) (int64, error) {
			wf, e2 := dao.ResolveWindow(r.Window, r.Days, now)
			if e2 != nil {
				return 0, e2
			}
			u, e2 := q.ChannelWindowUsage(ch.ID, wf)
			if e2 != nil {
				return 0, e2
			}
			return metricValue(r, u), nil
		}
		shouldDisable, reason, autoRecover, e2 := evaluateLimit(limit, now, usageFn)
		if e2 != nil {
			if e.Logger != nil {
				e.Logger.Error("channel_limit_eval_failed", zap.Uint("channel_id", ch.ID), zap.Error(e2))
			}
			continue
		}
		newStatus, newState, changed := reconcile(ch.Status, ch.LimitState.Data(), shouldDisable, reason, autoRecover, now.Unix())
		if !changed {
			continue
		}
		if err := m.Update(ch.ID, map[string]any{
			"status":      newStatus,
			"limit_state": datatypes.NewJSONType(newState),
		}); err != nil {
			if e.Logger != nil {
				e.Logger.Error("channel_limit_update_failed", zap.Uint("channel_id", ch.ID), zap.Error(err))
			}
			continue
		}
		if e.Bus != nil {
			updated, err := q.GetByID(ch.ID)
			if err == nil {
				_ = events.PublishChannelUpdate(ctx, e.Bus, *updated)
			}
		}
	}
	return nil
}
