package billing

import (
	"context"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
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
	App      dao.AppProvider
	Bus      app.EventBus
	Logger   *zap.Logger
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewLimitEvaluator(application dao.AppProvider, bus app.EventBus, logger *zap.Logger, interval time.Duration) *LimitEvaluator {
	return &LimitEvaluator{App: application, Bus: bus, Logger: logger, interval: interval, stopCh: make(chan struct{})}
}

// Start 起后台 ticker;每 tick 跑一轮 Tick(now)。
func (e *LimitEvaluator) Start() {
	if e.interval <= 0 {
		e.interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := e.Tick(time.Now().UTC()); err != nil && e.Logger != nil {
					e.Logger.Error("channel_limit_tick_failed", zap.Error(err))
				}
			case <-e.stopCh:
				return
			}
		}
	}()
}

func (e *LimitEvaluator) Stop() { e.stopOnce.Do(func() { close(e.stopCh) }) }

// Tick 跑一轮评估。now 应为 UTC。
func (e *LimitEvaluator) Tick(now time.Time) error {
	ctx := dao.NewContext(e.App)
	q := dao.NewAdminQuery(ctx).Channel()
	m := dao.NewAdminMutation(ctx).Channel()

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
			calls, cost, e2 := q.ChannelWindowUsage(ch.ID, wf)
			if e2 != nil {
				return 0, e2
			}
			if r.Metric == models.LimitMetricCost {
				return cost, nil
			}
			return calls, nil
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
				_ = events.PublishChannelUpdate(context.Background(), e.Bus, *updated)
			}
		}
	}
	return nil
}
